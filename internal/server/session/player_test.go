package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSessionManager_CRUD(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()

	// Create
	session := sm.CreateSession("p1", "Player1")
	assert.NotNil(t, session)
	assert.Equal(t, "p1", session.PlayerID)
	assert.Equal(t, "Player1", session.PlayerName)
	assert.True(t, session.IsOnline)

	// Get by ID
	s1 := sm.GetSession("p1")
	assert.Equal(t, session, s1)

	// Get by Token

	// Delete
	sm.DeleteSession("p1")
	assert.Nil(t, sm.GetSession("p1"))
}

func TestSessionManager_OnlineStatus(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()
	session := sm.CreateSession("p1", "Player1")

	// Initial state: online
	assert.True(t, session.IsOnline)
	assert.True(t, session.DisconnectedAt.IsZero())

	// Set Offline
	sm.SetOffline("p1")
	assert.False(t, sm.GetSession("p1").IsOnline)
	assert.False(t, sm.GetSession("p1").DisconnectedAt.IsZero())

	// Set Online again
	sm.SetOnline("p1")
	assert.True(t, sm.GetSession("p1").IsOnline)
	assert.True(t, sm.GetSession("p1").DisconnectedAt.IsZero())
}

func TestSessionManager_SetRoom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		playerID     string
		roomCode     string
		shouldCreate bool
	}{
		{"set room for existing player", "p1", "123456", true},
		{"set room for non-existent player", "p999", "123456", false},
		{"clear room", "p1", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm := NewSessionManager()
			if tt.shouldCreate {
				sm.CreateSession("p1", "Player1")
			}

			sm.SetRoom(tt.playerID, tt.roomCode)

			if tt.shouldCreate && tt.playerID == "p1" {
				session := sm.GetSession("p1")
				assert.Equal(t, tt.roomCode, session.RoomCode)
			}
		})
	}
}

func TestSessionManager_IsOnline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(sm *SessionManager)
		playerID   string
		wantOnline bool
	}{
		{
			name: "online player",
			setup: func(sm *SessionManager) {
				sm.CreateSession("p1", "Player1")
			},
			playerID:   "p1",
			wantOnline: true,
		},
		{
			name: "offline player",
			setup: func(sm *SessionManager) {
				sm.CreateSession("p1", "Player1")
				sm.SetOffline("p1")
			},
			playerID:   "p1",
			wantOnline: false,
		},
		{
			name:       "non-existent player",
			setup:      func(_ *SessionManager) {},
			playerID:   "p999",
			wantOnline: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm := NewSessionManager()
			tt.setup(sm)
			assert.Equal(t, tt.wantOnline, sm.IsOnline(tt.playerID))
		})
	}
}

func TestSessionManager_SetOffline_NonExistent(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()
	// Should not panic
	sm.SetOffline("non-existent")
}

func TestSessionManager_SetOnline_NonExistent(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()
	// Should not panic
	sm.SetOnline("non-existent")
}

func TestSessionManager_DeleteSession_NonExistent(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()
	// Should not panic
	sm.DeleteSession("non-existent")
}

func TestLongOfflineGameRetainsNewSettlementMetadata(t *testing.T) {
	sm := NewSessionManager()
	sm.EnsureSession("p1", "P1")
	sm.SetRoom("p1", "123456")
	sm.SetOffline("p1")
	saved := sm.GetSession("p1")
	saved.mu.Lock()
	saved.DisconnectedAt = time.Now().Add(-24 * time.Hour)
	saved.mu.Unlock()
	sm.cleanup()
	assert.NotNil(t, sm.Snapshot("p1"), "a living game keeps its reconnect association")
	sm.SetRoom("p1", "")
	sm.cleanup()
	assert.Equal(t, "123456", sm.Snapshot("p1").LastRoomCode, "new settlement retention starts at completion")
}
