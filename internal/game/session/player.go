package session

import (
	"sync"
	"time"
)

const (
	// 会话过期时间
	sessionExpireTime = 10 * time.Minute
)

// PlayerSession 玩家会话（用于断线重连）
type PlayerSession struct {
	PlayerID     string
	PlayerName   string
	RoomCode     string
	LastRoomCode string
	LastRoomAt   time.Time

	DisconnectedAt time.Time // 断线时间
	IsOnline       bool      // 是否在线

	mu sync.RWMutex
}

// SessionManager 会话管理器
type SessionManager struct {
	retention time.Duration
	sessions  map[string]*PlayerSession // playerID -> session
	mu        sync.RWMutex
}

// NewSessionManager 创建会话管理器
func NewSessionManager(retention ...time.Duration) *SessionManager {
	sm := &SessionManager{
		retention: sessionExpireTime,
		sessions:  make(map[string]*PlayerSession),
	}

	for _, duration := range retention {
		sm.retention = max(sm.retention, duration+time.Minute)
	}
	// 启动会话清理协程
	go sm.cleanupLoop()

	return sm
}

// CreateSession 创建新会话
func (sm *SessionManager) CreateSession(playerID, playerName string) *PlayerSession {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := &PlayerSession{
		PlayerID:   playerID,
		PlayerName: playerName,
		IsOnline:   true,
	}

	sm.sessions[playerID] = session

	return session
}

// GetSession 获取会话
func (sm *SessionManager) GetSession(playerID string) *PlayerSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[playerID]
}

// SetOffline 设置玩家离线
func (sm *SessionManager) SetOffline(playerID string) {
	sm.mu.RLock()
	session, ok := sm.sessions[playerID]
	sm.mu.RUnlock()

	if ok {
		session.mu.Lock()
		session.IsOnline = false
		session.DisconnectedAt = time.Now()
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
		setSessionRoomLocked(session, roomCode)
		session.mu.Unlock()
	}
}

// ClearRoomIfCurrent fences cleanup from an old room or recovered placeholder
// against a newer game owned by the same persistent identity.
func (sm *SessionManager) ClearRoomIfCurrent(playerID, expectedRoom string) {
	sm.mu.RLock()
	session := sm.sessions[playerID]
	sm.mu.RUnlock()
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.RoomCode == expectedRoom {
		setSessionRoomLocked(session, "")
	}
}

// Caller holds session.mu.
func setSessionRoomLocked(session *PlayerSession, roomCode string) {
	if roomCode == "" && session.RoomCode != "" {
		session.LastRoomCode = session.RoomCode
		session.LastRoomAt = time.Now()
	}
	if roomCode != "" {
		session.LastRoomCode = ""
	}
	session.RoomCode = roomCode
}

// DeleteSession 删除会话
func (sm *SessionManager) DeleteSession(playerID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.sessions, playerID)
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
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sm.cleanup()
	}
}

// cleanup 清理过期会话
func (sm *SessionManager) cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for playerID, session := range sm.sessions {
		session.mu.RLock()
		// 清理离线超过会话过期时间的会话
		if !session.IsOnline && session.RoomCode == "" && now.Sub(session.DisconnectedAt) > sm.retention && now.Sub(session.LastRoomAt) > sm.retention {
			delete(sm.sessions, playerID)
		}
		session.mu.RUnlock()
	}
}

// Snapshot returns session metadata without sharing mutable fields with readers.
func (sm *SessionManager) Snapshot(playerID string) *PlayerSession {
	session := sm.GetSession(playerID)
	if session == nil {
		return nil
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return &PlayerSession{
		PlayerID: session.PlayerID, PlayerName: session.PlayerName,
		RoomCode: session.RoomCode, LastRoomCode: session.LastRoomCode,
		IsOnline: session.IsOnline, DisconnectedAt: session.DisconnectedAt,
	}
}

// EnsureSession restores volatile metadata only after persistent identity verification.
func (sm *SessionManager) EnsureSession(id, name string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.sessions[id] == nil {
		sm.sessions[id] = &PlayerSession{PlayerID: id, PlayerName: name, IsOnline: true}
	}
}
