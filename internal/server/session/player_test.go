package session

import (
	"context"
	"errors"
	"fmt"
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

func TestSessionManagerCookieTokenLookupRestoresAndRevokesWithoutPlayerID(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	original := sm.MustCreateSession("p1", "Player1")
	originalToken := original.ReconnectToken
	sm.SetOffline(original.PlayerID)

	restored, err := sm.RestoreSessionByToken(originalToken, "temporary")
	require.NoError(t, err)
	assert.Equal(t, original.PlayerID, restored.PlayerID)
	assert.NotEqual(t, originalToken, restored.ReconnectToken)
	_, err = sm.RestoreSessionByToken(originalToken, "another-temporary")
	assert.ErrorIs(t, err, ErrInvalidReconnect)
	assert.True(t, sm.CanReconnectToken(restored.ReconnectToken))
	assert.True(t, sm.RevokeSessionByToken(restored.ReconnectToken))
	assert.False(t, sm.CanReconnectToken(restored.ReconnectToken))
}

func TestSessionManagerCookieTokenLookupRejectsExpiredCredential(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	sm.now = func() time.Time { return now }
	original := sm.MustCreateSession("p1", "Player1")
	sm.SetOffline(original.PlayerID)
	now = now.Add(reconnectTimeout)

	assert.False(t, sm.CanReconnectToken(original.ReconnectToken))
	_, err := sm.RestoreSessionByToken(original.ReconnectToken, "temporary")
	assert.ErrorIs(t, err, ErrReconnectExpired)
}

func TestSessionManagerCookieTokenHasOnlyOneConcurrentConsumer(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	original := sm.MustCreateSession("p1", "Player1")
	originalToken := original.ReconnectToken
	sm.SetOffline(original.PlayerID)
	sm.MustCreateSession("temporary-1", "Temporary 1")
	sm.MustCreateSession("temporary-2", "Temporary 2")

	results := make(chan error, 2)
	for _, temporaryID := range []string{"temporary-1", "temporary-2"} {
		go func() {
			_, err := sm.RestoreSessionByToken(originalToken, temporaryID)
			results <- err
		}()
	}

	successes := 0
	invalid := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if errors.Is(err, ErrInvalidReconnect) {
			invalid++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, invalid)
}

func TestSessionManagerBrowserCommitObservationRetiresPredecessor(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	original := sm.MustCreateSession("player", "Player")
	predecessor := original.ReconnectToken
	sm.SetOffline(original.PlayerID)
	sm.MustCreateSession("temporary", "Temporary")
	restored, err := sm.RestoreSessionByToken(predecessor, "temporary")
	require.NoError(t, err)

	assert.False(t, sm.CanReconnectToken(predecessor), "an active pending predecessor is not a second consumer")
	assert.True(t, sm.ObserveWebSessionToken(restored.ReconnectToken))
	assert.False(t, sm.CanReconnectToken(predecessor))
	assert.True(t, sm.CanReconnectToken(restored.ReconnectToken))
	_, err = sm.RestoreSessionByToken(predecessor, "other-temporary")
	assert.ErrorIs(t, err, ErrInvalidReconnect)
}

func TestSessionManagerDelayedPredecessorRevokeKillsObservedSuccessor(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	original := sm.MustCreateSession("player", "Player")
	predecessor := original.ReconnectToken
	sm.SetOffline(original.PlayerID)
	sm.MustCreateSession("temporary", "Temporary")
	restored, err := sm.RestoreSessionByToken(predecessor, "temporary")
	require.NoError(t, err)
	require.True(t, sm.ObserveWebSessionToken(restored.ReconnectToken))

	playerID, revoked := sm.RevokeSessionByTokenWithPlayer(predecessor)
	assert.True(t, revoked)
	assert.Equal(t, original.PlayerID, playerID)
	assert.Nil(t, sm.GetSession(original.PlayerID))
	assert.False(t, sm.CanReconnectToken(restored.ReconnectToken))
}

func TestSessionManagerRevocationLineageIsUncappedByTombstoneCapacity(t *testing.T) {
	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	original := sm.MustCreateSession("player", "Player")
	token0 := original.ReconnectToken

	sm.MustCreateSession("temporary-1", "Temporary 1")
	first, err := sm.RestoreSessionByToken(token0, "temporary-1")
	require.NoError(t, err)
	token1 := first.ReconnectToken
	require.True(t, sm.ObserveWebSessionToken(token1))

	sm.MustCreateSession("temporary-2", "Temporary 2")
	second, err := sm.RestoreSessionByToken(token1, "temporary-2")
	require.NoError(t, err)
	token2 := second.ReconnectToken
	require.True(t, sm.ObserveWebSessionToken(token2))

	sm.mu.Lock()
	for i := range maxRevokedWebTokens {
		sm.revokedWebTokens[fmt.Sprintf("capacity-%d", i)] = time.Now().Add(time.Hour)
	}
	sm.mu.Unlock()

	playerID, lineage, revoked := sm.RevokeSessionByTokenWithLineage(token2)
	require.True(t, revoked)
	assert.Equal(t, original.PlayerID, playerID)
	assert.ElementsMatch(t, []string{token0, token1, token2}, lineage)
	assert.Len(t, sm.revokedWebTokens, maxRevokedWebTokens,
		"full tombstone storage must not truncate the returned live lineage")
}

func TestSessionManagerDiscardedSuccessorAliasCanRevokePredecessorWinner(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	original := sm.MustCreateSession("player", "Player")
	predecessor := original.ReconnectToken
	sm.SetOffline(original.PlayerID)
	sm.MustCreateSession("temporary", "Temporary")
	restored, err := sm.RestoreSessionByToken(predecessor, "temporary")
	require.NoError(t, err)
	require.True(t, sm.OrphanBrowserRestore(restored))
	require.True(t, sm.ObserveWebSessionToken(predecessor))

	playerID, revoked := sm.RevokeSessionByTokenWithPlayer(restored.ReconnectToken)
	assert.True(t, revoked)
	assert.Equal(t, original.PlayerID, playerID)
	assert.Nil(t, sm.GetSession(original.PlayerID))
}

func TestSessionManagerRevocationAliasExpiresWithoutRevokingCurrentSession(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	sm.now = func() time.Time { return now }
	original := sm.MustCreateSession("player", "Player")
	predecessor := original.ReconnectToken
	sm.SetOffline(original.PlayerID)
	sm.MustCreateSession("temporary", "Temporary")
	restored, err := sm.RestoreSessionByToken(predecessor, "temporary")
	require.NoError(t, err)
	require.True(t, sm.ObserveWebSessionToken(restored.ReconnectToken))
	require.True(t, sm.IsKnownWebSessionToken(predecessor))

	now = now.Add(webRevocationAliasTTL)
	playerID, revoked := sm.RevokeSessionByTokenWithPlayer(predecessor)
	assert.False(t, revoked)
	assert.Empty(t, playerID)
	assert.True(t, sm.CanReconnectToken(restored.ReconnectToken))
}

func TestSessionManagerBrowserResponseLossRecoversEitherStoredCookie(t *testing.T) {
	for _, outcome := range []string{"predecessor", "successor"} {
		t.Run(outcome, func(t *testing.T) {
			sm := NewSessionManager()
			t.Cleanup(func() { require.NoError(t, sm.Close()) })
			original := sm.MustCreateSession("player", "Player")
			predecessor := original.ReconnectToken
			sm.SetOffline(original.PlayerID)
			sm.MustCreateSession("temporary-1", "Temporary 1")
			restored, err := sm.RestoreSessionByToken(predecessor, "temporary-1")
			require.NoError(t, err)
			require.True(t, sm.OrphanBrowserRestore(restored))
			assert.True(t, sm.CanReconnectToken(predecessor))
			assert.True(t, sm.CanReconnectToken(restored.ReconnectToken))

			presented := predecessor
			other := restored.ReconnectToken
			if outcome == "successor" {
				presented, other = other, presented
			}
			sm.MustCreateSession("temporary-2", "Temporary 2")
			next, restoreErr := sm.RestoreSessionByToken(presented, "temporary-2")
			require.NoError(t, restoreErr)
			assert.Equal(t, original.PlayerID, next.PlayerID)

			sm.MustCreateSession("temporary-3", "Temporary 3")
			_, replayErr := sm.RestoreSessionByToken(other, "temporary-3")
			assert.ErrorIs(t, replayErr, ErrInvalidReconnect)
		})
	}
}

func TestSessionManagerBrowserAmbiguousOutcomeHasOneConcurrentResolver(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	original := sm.MustCreateSession("player", "Player")
	predecessor := original.ReconnectToken
	sm.SetOffline(original.PlayerID)
	sm.MustCreateSession("temporary-1", "Temporary 1")
	restored, err := sm.RestoreSessionByToken(predecessor, "temporary-1")
	require.NoError(t, err)
	require.True(t, sm.OrphanBrowserRestore(restored))
	sm.MustCreateSession("temporary-old", "Temporary Old")
	sm.MustCreateSession("temporary-new", "Temporary New")

	results := make(chan error, 2)
	go func() {
		_, restoreErr := sm.RestoreSessionByToken(predecessor, "temporary-old")
		results <- restoreErr
	}()
	go func() {
		_, restoreErr := sm.RestoreSessionByToken(restored.ReconnectToken, "temporary-new")
		results <- restoreErr
	}()

	successes := 0
	invalid := 0
	for range 2 {
		if result := <-results; result == nil {
			successes++
		} else if errors.Is(result, ErrInvalidReconnect) {
			invalid++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, invalid)
}

func TestSessionManagerOrphanedPredecessorKeepsFullDisconnectWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	sm.now = func() time.Time { return now }
	original := sm.MustCreateSession("player", "Player")
	predecessor := original.ReconnectToken
	sm.SetOffline(original.PlayerID)
	now = now.Add(reconnectTimeout - time.Second)
	sm.MustCreateSession("temporary", "Temporary")
	restored, err := sm.RestoreSessionByToken(predecessor, "temporary")
	require.NoError(t, err)

	now = now.Add(24 * time.Hour)
	require.True(t, sm.OrphanBrowserRestore(restored))
	sm.SetOffline(original.PlayerID)
	assert.Equal(t, now.Add(reconnectTimeout), original.ReconnectTokenExpiresAt)
	require.True(t, sm.ObserveWebSessionToken(predecessor))
	now = now.Add(reconnectTimeout - time.Second)
	assert.True(t, sm.CanReconnectToken(predecessor))
}

func TestSessionManagerLongLivedOnlineCredentialGetsFullReconnectWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	sm := NewSessionManager()
	sm.now = func() time.Time { return now }
	original := sm.MustCreateSession("p1", "Player1")
	originalToken := original.ReconnectToken
	assert.True(t, original.ReconnectTokenExpiresAt.IsZero())

	now = now.Add(24 * time.Hour)
	sm.cleanup()
	assert.Same(t, original, sm.GetSessionByToken(originalToken), "online credentials must not expire by wall-clock age")

	sm.SetOffline(original.PlayerID)
	assert.Equal(t, now.Add(reconnectTimeout), original.ReconnectTokenExpiresAt)
	now = now.Add(reconnectTimeout - time.Second)
	provisional := sm.MustCreateSession("temporary", "Temporary")
	restored, err := sm.RestoreSession(originalToken, original.PlayerID, provisional.PlayerID)
	require.NoError(t, err)
	assert.NotEqual(t, originalToken, restored.ReconnectToken)
	assert.True(t, original.ReconnectTokenExpiresAt.IsZero(), "successful reconnect returns the session to online validity")
}

func TestSessionManagerDisconnectNearFormerTTLStillGetsFullWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	sm := NewSessionManager()
	sm.now = func() time.Time { return now }
	original := sm.MustCreateSession("p1", "Player1")
	originalToken := original.ReconnectToken

	now = now.Add(10*time.Minute - 10*time.Second)
	sm.SetOffline(original.PlayerID)
	disconnectedAt := now
	now = now.Add(reconnectTimeout - time.Second)
	assert.True(t, sm.CanReconnect(originalToken, original.PlayerID))
	assert.Equal(t, disconnectedAt.Add(reconnectTimeout), original.ReconnectTokenExpiresAt)

	provisional := sm.MustCreateSession("temporary", "Temporary")
	restored, err := sm.RestoreSession(originalToken, original.PlayerID, provisional.PlayerID)
	require.NoError(t, err)
	assert.Equal(t, original.PlayerID, restored.PlayerID)
}

func TestSessionManagerRepeatedOfflineSignalDoesNotExtendReconnectWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	sm := NewSessionManager()
	sm.now = func() time.Time { return now }
	original := sm.MustCreateSession("p1", "Player1")

	sm.SetOffline(original.PlayerID)
	disconnectedAt := original.DisconnectedAt
	deadline := original.ReconnectTokenExpiresAt
	now = now.Add(time.Minute)
	sm.SetOffline(original.PlayerID)

	assert.Equal(t, disconnectedAt, original.DisconnectedAt)
	assert.Equal(t, deadline, original.ReconnectTokenExpiresAt)
}

func TestSessionManagerCleanupSeparatesReconnectWindowFromDeadSessionRetention(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	sm := NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sm.Close()) })
	sm.now = func() time.Time { return now }
	playerSession := sm.MustCreateSession("p1", "Player1")
	token := playerSession.ReconnectToken
	sm.SetOffline(playerSession.PlayerID)

	now = now.Add(reconnectTimeout)
	sm.cleanup()
	assert.False(t, sm.CanReconnect(token, playerSession.PlayerID), "the credential expires at the recovery deadline")
	assert.NotNil(t, sm.GetSession(playerSession.PlayerID), "dead metadata retention must not be mistaken for credential validity")

	now = playerSession.DisconnectedAt.Add(deadSessionRetention + time.Second)
	sm.cleanup()
	assert.Nil(t, sm.GetSession(playerSession.PlayerID))
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
	assert.Equal(t, original, sm.GetSession(original.PlayerID))
	assert.Nil(t, sm.GetSessionByToken(original.ReconnectToken))
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
	playerSession.DisconnectedAt = time.Now().Add(-deadSessionRetention - time.Minute)
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
