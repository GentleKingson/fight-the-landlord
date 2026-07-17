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
	// reconnectTimeout is the recovery window that starts on the first physical
	// disconnect. Online credentials do not expire by wall-clock age.
	reconnectTimeout = 2 * time.Minute
	// deadSessionRetention only bounds how long expired offline metadata remains
	// in memory. It is not a reconnect credential TTL.
	deadSessionRetention = 10 * time.Minute
	// Retain the losing side of an ambiguous browser rotation only long enough
	// to linearize concurrent refresh/revoke requests. Browser tabs share the
	// cookie jar, so this is not a long-lived recovery credential.
	webRevocationAliasTTL = 2 * time.Minute
	// Revocation tombstones need only cover delayed response/request races. An
	// older dead cookie may create a new anonymous identity but can never recover
	// the revoked player.
	webRevokedTokenTTL  = 2 * time.Minute
	maxRevokedWebTokens = 65536
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
	// ReconnectTokenExpiresAt is the offline recovery deadline. It is zero while
	// the owning connection is online and is reset to a full reconnect window
	// when that connection disconnects.
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
	browserPending         bool
}

// browserSessionRotation keeps both outcomes recoverable while a browser may
// or may not have received the Set-Cookie response. The predecessor is an
// index alias, not a second active consumer: another restore is rejected until
// the owning connection closes and marks the rotation orphaned.
type browserSessionRotation struct {
	restored *RestoredSession
	orphaned bool
}

type webRevocationAlias struct {
	playerID  string
	expiresAt time.Time
}

// SessionManager 会话管理器
type SessionManager struct {
	sessions                map[string]*PlayerSession // playerID -> session
	tokens                  map[string]string         // token -> playerID
	browserSessionRotations map[string]*browserSessionRotation
	webRevocationAliases    map[string]webRevocationAlias
	webAliasesByPlayer      map[string]map[string]struct{}
	revokedWebTokens        map[string]time.Time
	tokenReader             io.Reader
	now                     func() time.Time
	mu                      sync.RWMutex

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
		sessions:                make(map[string]*PlayerSession),
		tokens:                  make(map[string]string),
		browserSessionRotations: make(map[string]*browserSessionRotation),
		webRevocationAliases:    make(map[string]webRevocationAlias),
		webAliasesByPlayer:      make(map[string]map[string]struct{}),
		revokedWebTokens:        make(map[string]time.Time),
		tokenReader:             rand.Reader,
		now:                     time.Now,
		ctx:                     ctx,
		cancel:                  cancel,
		cleanupInterval:         cleanupInterval,
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
		PlayerID:       playerID,
		PlayerName:     playerName,
		ReconnectToken: token,
		IsOnline:       true,
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
		valid := session.ReconnectToken == token && reconnectCredentialValidLocked(session, sm.now())
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
		if session.IsOnline {
			now := sm.now()
			session.IsOnline = false
			session.DisconnectedAt = now
			session.ReconnectTokenExpiresAt = now.Add(reconnectTimeout)
		}
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
		session.ReconnectTokenExpiresAt = time.Time{}
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
		sm.deleteSessionTokensLocked(playerID, session)
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
	return sm.revokeSessionLocked(token, playerID)
}

// RevokeSessionByToken is the cookie-backed counterpart to RevokeSession. The
// player identity is resolved exclusively from the server-side token index.
func (sm *SessionManager) RevokeSessionByToken(token string) bool {
	_, revoked := sm.RevokeSessionByTokenWithPlayer(token)
	return revoked
}

// RevokeSessionByTokenWithPlayer atomically resolves current, pending, or
// recently finalized browser lineage and revokes the whole live session.
func (sm *SessionManager) RevokeSessionByTokenWithPlayer(token string) (string, bool) {
	playerID, _, revoked := sm.RevokeSessionByTokenWithLineage(token)
	return playerID, revoked
}

// RevokeSessionByTokenWithLineage also returns every credential deleted from
// the live lineage. Callers use this uncapped set for in-flight response
// barriers; unlike revokedWebTokens it must not be truncated by tombstone
// capacity.
func (sm *SessionManager) RevokeSessionByTokenWithLineage(token string) (string, []string, bool) {
	if token == "" {
		return "", nil, false
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	playerID, direct := sm.tokens[token]
	if !direct {
		alias, aliasOK := sm.webRevocationAliases[token]
		if !aliasOK || !sm.now().Before(alias.expiresAt) {
			if aliasOK {
				sm.deleteWebRevocationAliasLocked(token, alias.playerID)
			}
			return "", nil, false
		}
		playerID = alias.playerID
	}
	if sm.sessions[playerID] == nil {
		if direct {
			delete(sm.tokens, token)
		} else {
			sm.deleteWebRevocationAliasLocked(token, playerID)
		}
		return "", nil, false
	}
	lineage := sm.revokeBrowserLineageLocked(playerID)
	return playerID, lineage, true
}

func (sm *SessionManager) revokeBrowserLineageLocked(playerID string) []string {
	session := sm.sessions[playerID]
	if session == nil {
		return nil
	}
	tokens := make(map[string]struct{})
	session.mu.RLock()
	tokens[session.ReconnectToken] = struct{}{}
	session.mu.RUnlock()
	if rotation := sm.browserSessionRotations[playerID]; rotation != nil {
		tokens[rotation.restored.previousToken] = struct{}{}
		tokens[rotation.restored.ReconnectToken] = struct{}{}
	}
	for token := range sm.webAliasesByPlayer[playerID] {
		tokens[token] = struct{}{}
	}
	sm.deleteSessionTokensLocked(playerID, session)
	delete(sm.sessions, playerID)
	expiresAt := sm.now().Add(webRevokedTokenTTL)
	for token := range tokens {
		if token != "" && len(sm.revokedWebTokens) < maxRevokedWebTokens {
			sm.revokedWebTokens[token] = expiresAt
		}
	}
	lineage := make([]string, 0, len(tokens))
	for token := range tokens {
		if token != "" {
			lineage = append(lineage, token)
		}
	}
	return lineage
}

func (sm *SessionManager) revokeSessionLocked(token, playerID string) bool {
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
	sm.deleteSessionTokensLocked(playerID, session)
	delete(sm.sessions, playerID)
	return true
}

// RestoreSession atomically consumes a reconnect token, rotates it, marks the
// restored session online, and removes the provisional session created for the
// new physical connection. Only one concurrent caller can consume a token.
func (sm *SessionManager) RestoreSession(token, playerID, temporaryPlayerID string) (*RestoredSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.browserSessionRotations[playerID] != nil {
		return nil, ErrInvalidReconnect
	}
	return sm.restoreSessionLocked(token, playerID, temporaryPlayerID, false)
}

// RestoreSessionByToken restores a browser session without trusting a
// JavaScript-supplied player identifier. The token index lookup and token
// consumption occur under the same manager lock.
func (sm *SessionManager) RestoreSessionByToken(token, temporaryPlayerID string) (*RestoredSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	playerID, ok := sm.tokens[token]
	if !ok {
		return nil, ErrInvalidReconnect
	}
	if rotation := sm.browserSessionRotations[playerID]; rotation != nil {
		if !rotation.orphaned || !sm.resolveBrowserRotationLocked(rotation, token) {
			return nil, ErrInvalidReconnect
		}
	}
	return sm.restoreSessionLocked(token, playerID, temporaryPlayerID, true)
}

func (sm *SessionManager) restoreSessionLocked(
	token, playerID, temporaryPlayerID string,
	browserPending bool,
) (*RestoredSession, error) {

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
	if !reconnectCredentialValidLocked(playerSession, now) {
		return nil, ErrReconnectExpired
	}
	wasOnline := playerSession.IsOnline
	disconnectedAt := playerSession.DisconnectedAt
	previousTokenExpiresAt := playerSession.ReconnectTokenExpiresAt

	newToken, err := sm.generateUniqueTokenLocked()
	if err != nil {
		return nil, err
	}
	if !browserPending {
		delete(sm.tokens, token)
	}
	playerSession.ReconnectToken = newToken
	playerSession.ReconnectTokenExpiresAt = time.Time{}
	playerSession.IsOnline = true
	playerSession.DisconnectedAt = time.Time{}
	sm.tokens[newToken] = playerID

	if temporaryPlayerID != "" && temporaryPlayerID != playerID {
		if temporarySession, exists := sm.sessions[temporaryPlayerID]; exists {
			sm.deleteSessionTokensLocked(temporaryPlayerID, temporarySession)
			delete(sm.sessions, temporaryPlayerID)
		}
	}

	restored := &RestoredSession{
		PlayerID:               playerSession.PlayerID,
		PlayerName:             playerSession.PlayerName,
		ReconnectToken:         playerSession.ReconnectToken,
		RoomCode:               playerSession.RoomCode,
		previousToken:          token,
		previousTokenExpiresAt: previousTokenExpiresAt,
		wasOnline:              wasOnline,
		disconnectedAt:         disconnectedAt,
		browserPending:         browserPending,
	}
	if browserPending {
		sm.browserSessionRotations[playerID] = &browserSessionRotation{restored: restored}
	}
	return restored, nil
}

// CanReconnectToken validates an opaque browser credential without accepting
// a player identifier from the caller.
func (sm *SessionManager) CanReconnectToken(token string) bool {
	if token == "" {
		return false
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	playerID, ok := sm.tokens[token]
	if !ok {
		return false
	}
	session := sm.sessions[playerID]
	if session == nil {
		return false
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	if session.ReconnectToken != token {
		rotation := sm.browserSessionRotations[playerID]
		if rotation == nil || !rotation.orphaned || rotation.restored.previousToken != token {
			return false
		}
	}
	return reconnectCredentialValidLocked(session, sm.now())
}

// IsKnownWebSessionToken distinguishes an active or ambiguous lineage from a
// dead cookie. Revoked tombstones are intentionally excluded: they cannot
// restore their player and may bootstrap a new anonymous identity.
func (sm *SessionManager) IsKnownWebSessionToken(token string) bool {
	if token == "" {
		return false
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if _, ok := sm.tokens[token]; ok {
		return true
	}
	alias, ok := sm.webRevocationAliases[token]
	if ok && sm.now().Before(alias.expiresAt) {
		return true
	}
	return false
}

// IsCurrentBrowserRestore revalidates the exact pending generation before a
// rebound browser identity is published or authorized.
func (sm *SessionManager) IsCurrentBrowserRestore(restored *RestoredSession) bool {
	if restored == nil || !restored.browserPending {
		return false
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	rotation := sm.browserSessionRotations[restored.PlayerID]
	if rotation == nil || rotation.restored != restored {
		return false
	}
	session := sm.sessions[restored.PlayerID]
	if session == nil || sm.tokens[restored.ReconnectToken] != restored.PlayerID {
		return false
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.ReconnectToken == restored.ReconnectToken && reconnectCredentialValidLocked(session, sm.now())
}

// ObserveWebSessionToken resolves a browser rotation after the server receives
// a follow-up request carrying whichever Cookie the browser actually stored.
// Observing the successor retires the predecessor. An orphaned predecessor
// instead becomes current while preserving the physical connection's latest
// online/offline deadline.
func (sm *SessionManager) ObserveWebSessionToken(token string) bool {
	if token == "" {
		return false
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	playerID, ok := sm.tokens[token]
	if !ok {
		return false
	}
	if rotation := sm.browserSessionRotations[playerID]; rotation != nil {
		if token == rotation.restored.ReconnectToken {
			sm.finalizeBrowserRotationLocked(rotation)
		} else if !rotation.orphaned || !sm.resolveBrowserRotationLocked(rotation, token) {
			return false
		}
	}
	session := sm.sessions[playerID]
	if session == nil {
		return false
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.ReconnectToken == token && reconnectCredentialValidLocked(session, sm.now())
}

// OrphanBrowserRestore releases the active-owner guard after an ambiguous
// commit's WebSocket closes. Both credentials remain aliases until one is
// observed, but the next restore still has exactly one serialized consumer.
func (sm *SessionManager) OrphanBrowserRestore(restored *RestoredSession) bool {
	if restored == nil || !restored.browserPending {
		return false
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	rotation := sm.browserSessionRotations[restored.PlayerID]
	if rotation == nil || rotation.restored != restored {
		return false
	}
	rotation.orphaned = true
	return true
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
	if restored.browserPending {
		rotation := sm.browserSessionRotations[restored.PlayerID]
		if rotation == nil || rotation.restored != restored || rotation.orphaned {
			return false
		}
	}

	playerSession.mu.Lock()
	defer playerSession.mu.Unlock()
	if playerSession.ReconnectToken != restored.ReconnectToken {
		return false
	}

	delete(sm.tokens, restored.ReconnectToken)
	playerSession.ReconnectToken = restored.previousToken
	playerSession.ReconnectTokenExpiresAt = restored.previousTokenExpiresAt
	playerSession.RoomCode = restored.RoomCode
	playerSession.IsOnline = restored.wasOnline
	playerSession.DisconnectedAt = restored.disconnectedAt
	sm.tokens[restored.previousToken] = restored.PlayerID
	if restored.browserPending {
		delete(sm.browserSessionRotations, restored.PlayerID)
	}
	return true
}

func (sm *SessionManager) finalizeBrowserRotationLocked(rotation *browserSessionRotation) {
	delete(sm.tokens, rotation.restored.previousToken)
	sm.addWebRevocationAliasLocked(rotation.restored.previousToken, rotation.restored.PlayerID)
	delete(sm.browserSessionRotations, rotation.restored.PlayerID)
}

func (sm *SessionManager) resolveBrowserRotationLocked(rotation *browserSessionRotation, token string) bool {
	restored := rotation.restored
	session := sm.sessions[restored.PlayerID]
	if session == nil {
		return false
	}
	switch token {
	case restored.ReconnectToken:
		sm.finalizeBrowserRotationLocked(rotation)
		return true
	case restored.previousToken:
		if !rotation.orphaned {
			return false
		}
		session.mu.Lock()
		if session.ReconnectToken != restored.ReconnectToken {
			session.mu.Unlock()
			return false
		}
		session.ReconnectToken = restored.previousToken
		session.mu.Unlock()
		delete(sm.tokens, restored.ReconnectToken)
		sm.addWebRevocationAliasLocked(restored.ReconnectToken, restored.PlayerID)
		delete(sm.browserSessionRotations, restored.PlayerID)
		return true
	default:
		return false
	}
}

func (sm *SessionManager) deleteSessionTokensLocked(playerID string, session *PlayerSession) {
	if session != nil {
		session.mu.RLock()
		delete(sm.tokens, session.ReconnectToken)
		session.mu.RUnlock()
	}
	if rotation := sm.browserSessionRotations[playerID]; rotation != nil {
		delete(sm.tokens, rotation.restored.previousToken)
		delete(sm.tokens, rotation.restored.ReconnectToken)
		delete(sm.browserSessionRotations, playerID)
	}
	for token := range sm.webAliasesByPlayer[playerID] {
		delete(sm.webRevocationAliases, token)
	}
	delete(sm.webAliasesByPlayer, playerID)
}

func (sm *SessionManager) addWebRevocationAliasLocked(token, playerID string) {
	if token == "" || playerID == "" {
		return
	}
	sm.webRevocationAliases[token] = webRevocationAlias{
		playerID:  playerID,
		expiresAt: sm.now().Add(webRevocationAliasTTL),
	}
	if sm.webAliasesByPlayer[playerID] == nil {
		sm.webAliasesByPlayer[playerID] = make(map[string]struct{})
	}
	sm.webAliasesByPlayer[playerID][token] = struct{}{}
}

func (sm *SessionManager) deleteWebRevocationAliasLocked(token, playerID string) {
	delete(sm.webRevocationAliases, token)
	aliases := sm.webAliasesByPlayer[playerID]
	delete(aliases, token)
	if len(aliases) == 0 {
		delete(sm.webAliasesByPlayer, playerID)
	}
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

	now := sm.now()
	if session.ReconnectToken != token || !reconnectCredentialValidLocked(session, now) {
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
		_, active := sm.tokens[token]
		_, alias := sm.webRevocationAliases[token]
		_, revoked := sm.revokedWebTokens[token]
		if !active && !alias && !revoked {
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
	for token, expiresAt := range sm.revokedWebTokens {
		if !now.Before(expiresAt) {
			delete(sm.revokedWebTokens, token)
		}
	}
	for token, alias := range sm.webRevocationAliases {
		if !now.Before(alias.expiresAt) {
			sm.deleteWebRevocationAliasLocked(token, alias.playerID)
		}
	}
	for playerID, session := range sm.sessions {
		session.mu.RLock()
		expired := !session.IsOnline && !reconnectCredentialValidLocked(session, now)
		dead := !session.IsOnline && now.Sub(session.DisconnectedAt) > deadSessionRetention
		currentToken := session.ReconnectToken
		session.mu.RUnlock()
		if expired {
			delete(sm.tokens, currentToken)
			if rotation := sm.browserSessionRotations[playerID]; rotation != nil {
				delete(sm.tokens, rotation.restored.previousToken)
			}
		}
		// Retain dead metadata briefly for cleanup bookkeeping after the reconnect
		// credential has already become unusable.
		if dead {
			sm.deleteSessionTokensLocked(playerID, session)
			delete(sm.sessions, playerID)
		}
	}
}

func reconnectCredentialValidLocked(session *PlayerSession, now time.Time) bool {
	if session.IsOnline {
		return true
	}
	return !session.ReconnectTokenExpiresAt.IsZero() && now.Before(session.ReconnectTokenExpiresAt) &&
		now.Sub(session.DisconnectedAt) <= reconnectTimeout
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
