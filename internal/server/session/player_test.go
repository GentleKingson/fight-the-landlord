package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingTokenReader struct{}

func (failingTokenReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestSessionManager_CRUD(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()

	// Create
	session := sm.MustCreateSession("p1", "Player1")
	assert.NotNil(t, session)
	assert.Equal(t, "p1", session.PlayerID)
	assert.Equal(t, "Player1", session.PlayerName)
	assert.NotEmpty(t, session.ReconnectToken)
	assert.True(t, session.IsOnline)

	// Get by ID
	s1 := sm.GetSession("p1")
	assert.Equal(t, session, s1)

	// Get by Token
	s2 := sm.GetSessionByToken(session.ReconnectToken)
	assert.Equal(t, session, s2)

	// Delete
	sm.DeleteSession("p1")
	assert.Nil(t, sm.GetSession("p1"))
	assert.Nil(t, sm.GetSessionByToken(session.ReconnectToken))
}

func TestSessionManagerTokenGenerationFailureDoesNotCreateOrRotateSession(t *testing.T) {
	t.Parallel()

	t.Run("create", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager()
		sm.tokenReader = failingTokenReader{}
		session, err := sm.CreateSession("p1", "Player1")
		assert.Nil(t, session)
		assert.ErrorIs(t, err, ErrTokenGeneration)
		assert.Nil(t, sm.GetSession("p1"))
	})

	t.Run("rotate", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager()
		original := sm.MustCreateSession("p1", "Player1")
		originalToken := original.ReconnectToken
		sm.SetOffline("p1")
		temporary := sm.MustCreateSession("temporary", "Temporary")
		sm.tokenReader = failingTokenReader{}

		restored, err := sm.RestoreSession(originalToken, "p1", "temporary")
		assert.Nil(t, restored)
		assert.ErrorIs(t, err, ErrTokenGeneration)
		assert.Same(t, original, sm.GetSessionByToken(originalToken))
		assert.Same(t, temporary, sm.GetSession("temporary"))
	})
}

func TestSessionManagerRevokeRejectsStaleRotatedToken(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()
	original := sm.MustCreateSession("p1", "Player1")
	oldToken := original.ReconnectToken
	sm.SetOffline("p1")
	sm.MustCreateSession("temporary", "Temporary")
	restored, err := sm.RestoreSession(oldToken, "p1", "temporary")
	assert.NoError(t, err)

	assert.False(t, sm.RevokeSession(oldToken, "p1"))
	assert.NotNil(t, sm.GetSession("p1"))
	assert.True(t, sm.RevokeSession(restored.ReconnectToken, "p1"))
	assert.Nil(t, sm.GetSession("p1"))
	assert.False(t, sm.CanReconnect(restored.ReconnectToken, "p1"))
}

func TestSessionManagerEnforcesCredentialTTLAndResetsItOnRotation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	sm := NewSessionManager()
	sm.now = func() time.Time { return now }
	original := sm.MustCreateSession("p1", "Player1")
	originalToken := original.ReconnectToken
	assert.Equal(t, now.Add(reconnectCredentialTTL), original.ReconnectTokenExpiresAt)

	// Browser-controlled localStorage metadata cannot extend the server deadline.
	now = now.Add(reconnectCredentialTTL)
	assert.False(t, sm.CanReconnect(originalToken, original.PlayerID))
	assert.Nil(t, sm.GetSessionByToken(originalToken))
	temporary := sm.MustCreateSession("temporary", "Temporary")
	restored, err := sm.RestoreSession(originalToken, original.PlayerID, temporary.PlayerID)
	assert.Nil(t, restored)
	assert.ErrorIs(t, err, ErrReconnectExpired)
	assert.Same(t, temporary, sm.GetSession(temporary.PlayerID))

	// A credential used before its deadline is single-use and receives a fresh TTL.
	now = now.Add(time.Second)
	fresh := sm.MustCreateSession("p2", "Player2")
	freshToken := fresh.ReconnectToken
	sm.SetOffline(fresh.PlayerID)
	provisional := sm.MustCreateSession("temporary-2", "Temporary 2")
	now = now.Add(2 * time.Minute)
	rotated, err := sm.RestoreSession(freshToken, fresh.PlayerID, provisional.PlayerID)
	assert.NoError(t, err)
	assert.Equal(t, now.Add(reconnectCredentialTTL), fresh.ReconnectTokenExpiresAt)
	assert.True(t, sm.CanReconnect(rotated.ReconnectToken, fresh.PlayerID))
}

func TestSessionManager_OnlineStatus(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()
	session := sm.MustCreateSession("p1", "Player1")

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

func TestSessionManager_RestoreSessionRotatesTokenAndDeletesTemporarySession(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	original := sm.MustCreateSession("p1", "Player1")
	sm.SetRoom("p1", "123456")
	sm.SetOffline("p1")
	temporary := sm.MustCreateSession("temporary", "Temporary")
	originalToken := original.ReconnectToken
	temporaryToken := temporary.ReconnectToken

	restored, err := sm.RestoreSession(originalToken, "p1", "temporary")
	assert.NoError(t, err)
	assert.Equal(t, "p1", restored.PlayerID)
	assert.Equal(t, "Player1", restored.PlayerName)
	assert.Equal(t, "123456", restored.RoomCode)
	assert.NotEmpty(t, restored.ReconnectToken)
	assert.NotEqual(t, originalToken, restored.ReconnectToken)
	assert.True(t, sm.IsOnline("p1"))
	assert.Nil(t, sm.GetSessionByToken(originalToken))
	assert.Equal(t, original, sm.GetSessionByToken(restored.ReconnectToken))
	assert.Nil(t, sm.GetSession("temporary"))
	assert.Nil(t, sm.GetSessionByToken(temporaryToken))
}

func TestSessionManager_RestoreSessionRejectsExpiredTokenWithoutDeletingTemporarySession(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	original := sm.MustCreateSession("p1", "Player1")
	sm.SetOffline("p1")
	original.mu.Lock()
	original.DisconnectedAt = time.Now().Add(-3 * time.Minute)
	original.mu.Unlock()
	temporary := sm.MustCreateSession("temporary", "Temporary")

	restored, err := sm.RestoreSession(original.ReconnectToken, "p1", "temporary")
	assert.Nil(t, restored)
	assert.ErrorIs(t, err, ErrReconnectExpired)
	assert.Equal(t, original, sm.GetSessionByToken(original.ReconnectToken))
	assert.Equal(t, temporary, sm.GetSession("temporary"))
}

func TestSessionManager_RestoreSessionConsumesTokenOnce(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	original := sm.MustCreateSession("p1", "Player1")
	sm.SetOffline("p1")
	sm.MustCreateSession("temporary-1", "Temporary 1")
	sm.MustCreateSession("temporary-2", "Temporary 2")
	originalToken := original.ReconnectToken

	results := make(chan error, 2)
	go func() {
		_, err := sm.RestoreSession(originalToken, "p1", "temporary-1")
		results <- err
	}()
	go func() {
		_, err := sm.RestoreSession(originalToken, "p1", "temporary-2")
		results <- err
	}()

	successes := 0
	failures := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if assert.ErrorIs(t, err, ErrInvalidReconnect) {
			failures++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, failures)
}

func TestSessionManager_RestoreSessionSupportsConsecutiveReconnects(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	original := sm.MustCreateSession("p1", "Player1")
	sm.SetOffline("p1")
	sm.MustCreateSession("temporary-1", "Temporary 1")

	first, err := sm.RestoreSession(original.ReconnectToken, "p1", "temporary-1")
	assert.NoError(t, err)
	sm.SetOffline("p1")
	sm.MustCreateSession("temporary-2", "Temporary 2")

	second, err := sm.RestoreSession(first.ReconnectToken, "p1", "temporary-2")
	assert.NoError(t, err)
	assert.NotEqual(t, first.ReconnectToken, second.ReconnectToken)
	assert.Nil(t, sm.GetSessionByToken(first.ReconnectToken))
	assert.NotNil(t, sm.GetSessionByToken(second.ReconnectToken))
	assert.Nil(t, sm.GetSession("temporary-2"))
}

func TestSessionManager_RollbackRestoreMakesOriginalTokenRetryable(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	original := sm.MustCreateSession("p1", "Player1")
	sm.SetOffline("p1")
	original.mu.RLock()
	disconnectedAt := original.DisconnectedAt
	originalToken := original.ReconnectToken
	original.mu.RUnlock()
	sm.MustCreateSession("temporary-1", "Temporary 1")

	restored, err := sm.RestoreSession(originalToken, "p1", "temporary-1")
	assert.NoError(t, err)
	assert.True(t, sm.RollbackRestore(restored))
	assert.False(t, sm.RollbackRestore(restored), "a restore can only be rolled back once")

	rolledBack := sm.GetSession("p1")
	assert.Equal(t, originalToken, rolledBack.ReconnectToken)
	assert.False(t, rolledBack.IsOnline)
	assert.Equal(t, disconnectedAt, rolledBack.DisconnectedAt)
	assert.Nil(t, sm.GetSessionByToken(restored.ReconnectToken))
	assert.Same(t, rolledBack, sm.GetSessionByToken(originalToken))

	sm.MustCreateSession("temporary-2", "Temporary 2")
	retried, err := sm.RestoreSession(originalToken, "p1", "temporary-2")
	assert.NoError(t, err)
	assert.NotEqual(t, originalToken, retried.ReconnectToken)
}

func TestSessionManager_CanReconnect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setup     func(sm *SessionManager) (token, playerID string)
		wantAllow bool
	}{
		{
			name: "valid reconnection (online)",
			setup: func(sm *SessionManager) (string, string) {
				session := sm.MustCreateSession("p1", "Player1")
				return session.ReconnectToken, "p1"
			},
			wantAllow: true,
		},
		{
			name: "valid reconnection (offline)",
			setup: func(sm *SessionManager) (string, string) {
				session := sm.MustCreateSession("p1", "Player1")
				sm.SetOffline("p1")
				return session.ReconnectToken, "p1"
			},
			wantAllow: true,
		},
		{
			name: "invalid token",
			setup: func(sm *SessionManager) (string, string) {
				sm.MustCreateSession("p1", "Player1")
				return "wrong-token", "p1"
			},
			wantAllow: false,
		},
		{
			name: "wrong player ID",
			setup: func(sm *SessionManager) (string, string) {
				session := sm.MustCreateSession("p1", "Player1")
				return session.ReconnectToken, "p2"
			},
			wantAllow: false,
		},
		{
			name: "expired session",
			setup: func(sm *SessionManager) (string, string) {
				session := sm.MustCreateSession("p1", "Player1")
				sm.SetOffline("p1")
				// Hack internal time for testing
				session.mu.Lock()
				session.DisconnectedAt = time.Now().Add(-3 * time.Minute)
				session.mu.Unlock()
				return session.ReconnectToken, "p1"
			},
			wantAllow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm := NewSessionManager()
			token, playerID := tt.setup(sm)
			assert.Equal(t, tt.wantAllow, sm.CanReconnect(token, playerID))
		})
	}
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
				sm.MustCreateSession("p1", "Player1")
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
				sm.MustCreateSession("p1", "Player1")
			},
			playerID:   "p1",
			wantOnline: true,
		},
		{
			name: "offline player",
			setup: func(sm *SessionManager) {
				sm.MustCreateSession("p1", "Player1")
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

func TestSessionManager_GetSessionByToken_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("invalid token returns nil", func(t *testing.T) {
		t.Parallel()
		sm := NewSessionManager()
		sm.MustCreateSession("p1", "Player1")
		assert.Nil(t, sm.GetSessionByToken("invalid-token"))
	})

	t.Run("empty token returns nil", func(t *testing.T) {
		t.Parallel()
		sm := NewSessionManager()
		sm.MustCreateSession("p1", "Player1")
		assert.Nil(t, sm.GetSessionByToken(""))
	})
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

func TestSessionManagerCloseStopsCleanupWorker(t *testing.T) {
	t.Parallel()

	sm := newSessionManager(context.Background(), 5*time.Millisecond)
	playerSession := sm.MustCreateSession("expired", "Expired Player")
	sm.SetOffline(playerSession.PlayerID)
	playerSession.mu.Lock()
	playerSession.DisconnectedAt = time.Now().Add(-sessionExpireTime - time.Minute)
	playerSession.mu.Unlock()

	require.Eventually(t, func() bool {
		return sm.GetSession(playerSession.PlayerID) == nil
	}, time.Second, time.Millisecond)
	require.NoError(t, sm.Close())
	require.NoError(t, sm.Close(), "Close must be idempotent")
}

func TestSessionManagerCloseWaitsAfterParentCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	sm := NewSessionManagerWithContext(ctx)
	cancel()

	done := make(chan error, 1)
	go func() { done <- sm.Close() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("SessionManager.Close did not wait for a canceled worker")
	}
}
