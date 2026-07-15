package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	// 重连等待时间
	reconnectTimeout = 2 * time.Minute
	// 会话过期时间
	sessionExpireTime = 10 * time.Minute
	// 浏览器可持久化凭证的最长有效期。每次成功重连都会旋转并重置。
	reconnectCredentialTTL = 10 * time.Minute
)

var (
	ErrInvalidReconnect = errors.New("invalid reconnect credentials")
	ErrReconnectExpired = errors.New("reconnect window expired")
	ErrTokenGeneration  = errors.New("reconnect token generation failed")
)

// PlayerSession 玩家会话（用于断线重连）
type PlayerSession struct {
	PlayerID       string
	PlayerName     string
	ReconnectToken string
	// ReconnectTokenExpiresAt is enforced by the server; browser metadata is
	// only an early cleanup hint and cannot extend this deadline.
	ReconnectTokenExpiresAt time.Time
	RoomCode                string

	DisconnectedAt time.Time // 断线时间
	IsOnline       bool      // 是否在线

	mu sync.RWMutex
}

// RestoredSession is an immutable snapshot returned after a reconnect token is
// consumed and rotated.
type RestoredSession struct {
	PlayerID               string
	PlayerName             string
	ReconnectToken         string
	RoomCode               string
	previousToken          string
	previousTokenExpiresAt time.Time
	wasOnline              bool
	disconnectedAt         time.Time
}

// SessionManager 会话管理器
type SessionManager struct {
	sessions    map[string]*PlayerSession // playerID -> session
	tokens      map[string]string         // token -> playerID
	tokenReader io.Reader
	now         func() time.Time
	mu          sync.RWMutex

	ctx             context.Context
	cancel          context.CancelFunc
	cleanupInterval time.Duration
	workers         sync.WaitGroup
	closeOnce       sync.Once
}

// NewSessionManager 创建会话管理器
func NewSessionManager() *SessionManager {
	return NewSessionManagerWithContext(context.Background())
}

// NewSessionManagerWithContext creates a manager whose cleanup worker stops
// when either the parent context is canceled or Close is called.
func NewSessionManagerWithContext(ctx context.Context) *SessionManager {
	return newSessionManager(ctx, time.Minute)
}

func newSessionManager(parent context.Context, cleanupInterval time.Duration) *SessionManager {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent) //nolint:gosec // Close owns cancellation and waits for the cleanup worker.
	sm := &SessionManager{
		sessions:        make(map[string]*PlayerSession),
		tokens:          make(map[string]string),
		tokenReader:     rand.Reader,
		now:             time.Now,
		ctx:             ctx,
		cancel:          cancel,
		cleanupInterval: cleanupInterval,
	}

	sm.workers.Add(1)
	go func() {
		defer sm.workers.Done()
		sm.cleanupLoop()
	}()

	return sm
}

// Close stops the cleanup worker and waits for it to exit. It is idempotent.
func (sm *SessionManager) Close() error {
	if sm == nil {
		return nil
	}
	sm.closeOnce.Do(func() {
		if sm.cancel != nil {
			sm.cancel()
		}
		sm.workers.Wait()
	})
	return nil
}

// CreateSession 创建新会话
func (sm *SessionManager) CreateSession(playerID, playerName string) (*PlayerSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	token, err := sm.generateUniqueTokenLocked()
	if err != nil {
		return nil, err
	}

	session := &PlayerSession{
		PlayerID:                playerID,
		PlayerName:              playerName,
		ReconnectToken:          token,
		ReconnectTokenExpiresAt: sm.now().Add(reconnectCredentialTTL),
		IsOnline:                true,
	}

	sm.sessions[playerID] = session
	sm.tokens[token] = playerID

	return session, nil
}

// MustCreateSession keeps tests concise while production call sites handle the
// cryptographic failure explicitly.
func (sm *SessionManager) MustCreateSession(playerID, playerName string) *PlayerSession {
	session, err := sm.CreateSession(playerID, playerName)
	if err != nil {
		panic(err)
	}
	return session
}

// GetSession 获取会话
func (sm *SessionManager) GetSession(playerID string) *PlayerSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[playerID]
}

// GetSessionByToken 通过 token 获取会话
func (sm *SessionManager) GetSessionByToken(token string) *PlayerSession {
	sm.mu.RLock()
	playerID, ok := sm.tokens[token]
	if !ok {
		sm.mu.RUnlock()
		return nil
	}
	session := sm.sessions[playerID]
	if session != nil {
		session.mu.RLock()
		valid := session.ReconnectToken == token && sm.now().Before(session.ReconnectTokenExpiresAt)
		session.mu.RUnlock()
		if !valid {
			session = nil
		}
	}
	sm.mu.RUnlock()
	return session
}

// SetOffline 设置玩家离线
func (sm *SessionManager) SetOffline(playerID string) {
	sm.mu.RLock()
	session, ok := sm.sessions[playerID]
	sm.mu.RUnlock()

	if ok {
		session.mu.Lock()
		session.IsOnline = false
		session.DisconnectedAt = sm.now()
		session.mu.Unlock()
	}
}

// SetOnline 设置玩家上线
func (sm *SessionManager) SetOnline(playerID string) {
	sm.mu.RLock()
	session, ok := sm.sessions[playerID]
	sm.mu.RUnlock()

	if ok {
		session.mu.Lock()
		session.IsOnline = true
		session.DisconnectedAt = time.Time{}
		session.mu.Unlock()
	}
}

// SetRoom 设置玩家所在房间
func (sm *SessionManager) SetRoom(playerID, roomCode string) {
	sm.mu.RLock()
	session, ok := sm.sessions[playerID]
	sm.mu.RUnlock()

	if ok {
		session.mu.Lock()
		session.RoomCode = roomCode
		session.mu.Unlock()
	}
}

// DeleteSession 删除会话
func (sm *SessionManager) DeleteSession(playerID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, ok := sm.sessions[playerID]; ok {
		delete(sm.tokens, session.ReconnectToken)
		delete(sm.sessions, playerID)
	}
}

// RevokeSession consumes the exact browser-held credential and removes its
// session. A stale or already-rotated token cannot revoke the current session.
func (sm *SessionManager) RevokeSession(token, playerID string) bool {
	if token == "" || playerID == "" {
		return false
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.tokens[token] != playerID {
		return false
	}
	session := sm.sessions[playerID]
	if session == nil {
		delete(sm.tokens, token)
		return false
	}
	session.mu.RLock()
	currentToken := session.ReconnectToken
	session.mu.RUnlock()
	if currentToken != token {
		return false
	}
	delete(sm.tokens, token)
	delete(sm.sessions, playerID)
	return true
}

// RestoreSession atomically consumes a reconnect token, rotates it, marks the
// restored session online, and removes the provisional session created for the
// new physical connection. Only one concurrent caller can consume a token.
func (sm *SessionManager) RestoreSession(token, playerID, temporaryPlayerID string) (*RestoredSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	storedPlayerID, ok := sm.tokens[token]
	if !ok || storedPlayerID != playerID {
		return nil, ErrInvalidReconnect
	}

	playerSession, ok := sm.sessions[playerID]
	if !ok {
		return nil, ErrInvalidReconnect
	}

	playerSession.mu.Lock()
	defer playerSession.mu.Unlock()

	if playerSession.ReconnectToken != token {
		return nil, ErrInvalidReconnect
	}
	now := sm.now()
	if !now.Before(playerSession.ReconnectTokenExpiresAt) {
		return nil, ErrReconnectExpired
	}
	if !playerSession.IsOnline && now.Sub(playerSession.DisconnectedAt) > reconnectTimeout {
		return nil, ErrReconnectExpired
	}
	wasOnline := playerSession.IsOnline
	disconnectedAt := playerSession.DisconnectedAt
	previousTokenExpiresAt := playerSession.ReconnectTokenExpiresAt

	newToken, err := sm.generateUniqueTokenLocked()
	if err != nil {
		return nil, err
	}
	delete(sm.tokens, token)
	playerSession.ReconnectToken = newToken
	playerSession.ReconnectTokenExpiresAt = now.Add(reconnectCredentialTTL)
	playerSession.IsOnline = true
	playerSession.DisconnectedAt = time.Time{}
	sm.tokens[newToken] = playerID

	if temporaryPlayerID != "" && temporaryPlayerID != playerID {
		if temporarySession, exists := sm.sessions[temporaryPlayerID]; exists {
			temporarySession.mu.RLock()
			temporaryToken := temporarySession.ReconnectToken
			temporarySession.mu.RUnlock()
			delete(sm.tokens, temporaryToken)
			delete(sm.sessions, temporaryPlayerID)
		}
	}

	return &RestoredSession{
		PlayerID:               playerSession.PlayerID,
		PlayerName:             playerSession.PlayerName,
		ReconnectToken:         playerSession.ReconnectToken,
		RoomCode:               playerSession.RoomCode,
		previousToken:          token,
		previousTokenExpiresAt: previousTokenExpiresAt,
		wasOnline:              wasOnline,
		disconnectedAt:         disconnectedAt,
	}, nil
}

// RollbackRestore restores the consumed credential when the server cannot
// finish rebinding the physical connection. It only succeeds while the rotated
// token still belongs to this restore, so it cannot undo a later reconnect.
func (sm *SessionManager) RollbackRestore(restored *RestoredSession) bool {
	if restored == nil {
		return false
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()
	playerSession, ok := sm.sessions[restored.PlayerID]
	if !ok || sm.tokens[restored.ReconnectToken] != restored.PlayerID {
		return false
	}

	playerSession.mu.Lock()
	defer playerSession.mu.Unlock()
	if playerSession.ReconnectToken != restored.ReconnectToken {
		return false
	}

	delete(sm.tokens, restored.ReconnectToken)
	playerSession.ReconnectToken = restored.previousToken
	playerSession.ReconnectTokenExpiresAt = restored.previousTokenExpiresAt
	playerSession.IsOnline = restored.wasOnline
	playerSession.DisconnectedAt = restored.disconnectedAt
	sm.tokens[restored.previousToken] = restored.PlayerID
	return true
}

// CanReconnect 检查玩家是否可以重连
func (sm *SessionManager) CanReconnect(token, playerID string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	storedPlayerID, ok := sm.tokens[token]
	if !ok || storedPlayerID != playerID {
		return false
	}

	session, ok := sm.sessions[playerID]
	if !ok {
		return false
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	if session.ReconnectToken != token || !sm.now().Before(session.ReconnectTokenExpiresAt) {
		return false
	}
	// 检查是否在重连时限内
	if !session.IsOnline && sm.now().Sub(session.DisconnectedAt) > reconnectTimeout {
		return false
	}

	return true
}

func (sm *SessionManager) generateUniqueTokenLocked() (string, error) {
	for range 16 {
		token, err := generateToken(sm.tokenReader)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrTokenGeneration, err)
		}
		if _, exists := sm.tokens[token]; !exists {
			return token, nil
		}
	}
	return "", fmt.Errorf("%w: repeated token collision", ErrTokenGeneration)
}

// IsOnline 检查玩家是否在线
func (sm *SessionManager) IsOnline(playerID string) bool {
	sm.mu.RLock()
	session, ok := sm.sessions[playerID]
	sm.mu.RUnlock()

	if !ok {
		return false
	}

	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.IsOnline
}

// cleanupLoop 定期清理过期会话
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(sm.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sm.cleanup()
		case <-sm.ctx.Done():
			return
		}
	}
}

// cleanup 清理过期会话
func (sm *SessionManager) cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := sm.now()
	for playerID, session := range sm.sessions {
		session.mu.RLock()
		if !now.Before(session.ReconnectTokenExpiresAt) {
			delete(sm.tokens, session.ReconnectToken)
		}
		// 清理离线超过会话过期时间的会话
		if !session.IsOnline && now.Sub(session.DisconnectedAt) > sessionExpireTime {
			delete(sm.tokens, session.ReconnectToken)
			delete(sm.sessions, playerID)
		}
		session.mu.RUnlock()
	}
}

// generateToken generates a token only after the cryptographic reader filled
// the complete buffer. Partial reads and entropy failures are never accepted.
func generateToken(reader io.Reader) (string, error) {
	bytes := make([]byte, 32)
	if _, err := io.ReadFull(reader, bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
