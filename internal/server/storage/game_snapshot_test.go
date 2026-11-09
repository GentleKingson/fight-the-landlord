package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testGameRecord(id string, revision int64) *GameRecord {
	return &GameRecord{Version: 2, ID: id, RoomCode: "123456", Revision: revision, SavedAt: time.Now(), State: json.RawMessage(`{}`)}
}

func TestGameSnapshotRejectsStaleAndConflictingWrites(t *testing.T) {
	store, mr := newTestRedisStore(t)
	defer mr.Close()
	ctx := context.Background()
	first := testGameRecord("game", 1)
	require.NoError(t, store.SaveGame(ctx, first))
	require.NoError(t, store.SaveGame(ctx, first), "lost responses can retry the identical candidate")
	next := testGameRecord("game", 2)
	require.NoError(t, store.SaveGame(ctx, next))
	require.ErrorIs(t, store.SaveGame(ctx, first), ErrStaleGame)
	conflicting := *next
	conflicting.State = json.RawMessage(`{"different":true}`)
	require.ErrorIs(t, store.SaveGame(ctx, &conflicting), ErrStaleGame)
	loaded, err := store.LoadGame(ctx, "game")
	require.NoError(t, err)
	require.Equal(t, next.State, loaded.State)
	mr.SetError("ERR unavailable")
	require.Error(t, store.SaveGame(ctx, testGameRecord("game", 3)))
	mr.SetError("")
	loaded, err = store.LoadGame(ctx, "game")
	require.NoError(t, err)
	require.EqualValues(t, 2, loaded.Revision)
}

func TestDurableSettlementOutboxSurvivesRepeatedRecovery(t *testing.T) {
	store, mr := newTestRedisStore(t)
	defer mr.Close()
	lb := NewLeaderboardManager(store.client)
	ctx := context.Background()
	for i, win := range []bool{false, true} {
		id := []string{"first", "second"}[i]
		record := testGameRecord(id, 1)
		record.Ended, record.ExpiresAt = true, time.Now().Add(-time.Minute)
		record.Results = []GameResult{{GameID: id, PlayerID: "player", PlayerName: "Player", IsLandlord: true, IsWinner: win}}
		require.NoError(t, store.SaveGame(ctx, record))
		require.NoError(t, store.DeleteExpiredGame(ctx, record))
		kept, err := store.LoadGame(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, kept, "room expiration must not discard a pending settlement")
	}
	mr.SetError("ERR unavailable")
	require.Error(t, store.DrainSettlements(ctx, lb))
	mr.SetError("")
	require.NoError(t, store.DrainSettlements(ctx, lb))
	stats, err := lb.GetPlayerStats(ctx, "player")
	require.NoError(t, err)
	require.Equal(t, 30, stats.Score)
	require.Equal(t, 1, stats.CurrentStreak)
	// Simulate a crash after committing statistics but before acknowledging the
	// outbox, then a much later recovery. Deduplication must not expire after 24h.
	mr.FastForward(48 * time.Hour)
	_, err = store.client.Do(ctx, "ZADD", pendingGamesKey, 1, "first", 2, "second").Result()
	require.NoError(t, err)
	for range 3 {
		require.NoError(t, store.DrainSettlements(ctx, NewLeaderboardManager(store.client)))
	}
	after, err := lb.GetPlayerStats(ctx, "player")
	require.NoError(t, err)
	require.Equal(t, stats, after)
}
