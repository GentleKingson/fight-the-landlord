package storage

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func newTestLeaderboardManager(t *testing.T) (*LeaderboardManager, *miniredis.Miniredis) {
	t.Helper()
	store, mr := newTestRedisStore(t)
	return NewLeaderboardManager(store.client), mr
}

func TestLeaderboard_RecordGameResult_NewPlayer(t *testing.T) {
	t.Parallel()

	lm, mr := newTestLeaderboardManager(t)
	defer mr.Close()
	ctx := context.Background()

	// Record result for new player
	// Landlord, Win
	err := lm.RecordGameResult(ctx, "p1", "Player1", true, true)
	assert.NoError(t, err)

	stats, err := lm.GetPlayerStats(ctx, "p1")
	assert.NoError(t, err)

	assert.Equal(t, "p1", stats.PlayerID)
	assert.Equal(t, 1, stats.TotalGames)
	assert.Equal(t, 1, stats.Wins)
	assert.Equal(t, 1, stats.LandlordGames)
	assert.Equal(t, 1, stats.LandlordWins)
	assert.Equal(t, 30, stats.Score)
	assert.Equal(t, 1, stats.CurrentStreak)
}

func TestLeaderboard_RecordGameResult_Update(t *testing.T) {
	t.Parallel()

	lm, mr := newTestLeaderboardManager(t)
	defer mr.Close()
	ctx := context.Background()

	// Initial record (Farmer Win) -> Score 15
	err := lm.RecordGameResult(ctx, "p1", "Player1", false, true)
	assert.NoError(t, err)

	// Second record (Landlord Loss) -> Score 15 - 20 = -5 -> 0 (min 0)
	err = lm.RecordGameResult(ctx, "p1", "Player1", true, false)
	assert.NoError(t, err)

	stats, err := lm.GetPlayerStats(ctx, "p1")
	assert.NoError(t, err)

	assert.Equal(t, 2, stats.TotalGames)
	assert.Equal(t, 1, stats.Wins)
	assert.Equal(t, 1, stats.Losses)
	assert.Equal(t, 0, stats.Score)
	assert.Equal(t, -1, stats.CurrentStreak)
}

func TestLeaderboard_StreakBonus(t *testing.T) {
	t.Parallel()

	lm, mr := newTestLeaderboardManager(t)
	defer mr.Close()
	ctx := context.Background()

	// Win 3 times as Farmer (15 * 3 = 45)
	// Bonus: 3rd win gets StreakBonus3 (5)
	// Expected score: 15 + 15 + (15 + 5) = 50?
	// Let's check logic:
	// currentStreak increased BEFORE bonus check.
	// 1st: streak 1.
	// 2nd: streak 2.
	// 3rd: streak 3. -> check streak >= 3 -> add bonus.

	for i := 0; i < 3; i++ {
		err := lm.RecordGameResult(ctx, "p1", "Player1", false, true)
		assert.NoError(t, err)
	}

	stats, _ := lm.GetPlayerStats(ctx, "p1")

	// 1st: 15, streak 1
	// 2nd: 30, streak 2
	// 3rd: 30 + 15 + 5 = 50, streak 3
	assert.Equal(t, 50, stats.Score)
	assert.Equal(t, 3, stats.CurrentStreak)
}

func TestLeaderboard_GetLeaderboard(t *testing.T) {
	t.Parallel()

	lm, mr := newTestLeaderboardManager(t)
	defer mr.Close()
	ctx := context.Background()

	// Create p1: Score 30 (present in total/daily/weekly)
	err := lm.RecordGameResult(ctx, "p1", "Player1", true, true)
	assert.NoError(t, err)
	// Create p2: Score 15 (present in total/daily/weekly)
	err = lm.RecordGameResult(ctx, "p2", "Player2", false, true)
	assert.NoError(t, err)

	// Seed a historical player only on total leaderboard (not active today/this week)
	err = lm.SavePlayerStats(ctx, &PlayerStats{
		PlayerID:   "p0",
		PlayerName: "Legend",
		TotalGames: 10,
		Wins:       10,
		Score:      100,
	})
	assert.NoError(t, err)
	assert.NoError(t, lm.redis.ZAdd(ctx, leaderboardKey, redis.Z{Score: 100, Member: "p0"}).Err())

	entries, err := lm.GetLeaderboard(ctx, "total", 0, 10)
	assert.NoError(t, err)
	assert.Len(t, entries, 3)
	assert.Equal(t, "p0", entries[0].PlayerID)
	assert.Equal(t, 1, entries[0].Rank)
	assert.Equal(t, "p1", entries[1].PlayerID)
	assert.Equal(t, 2, entries[1].Rank)
	assert.Equal(t, "p2", entries[2].PlayerID)
	assert.Equal(t, 3, entries[2].Rank)

	// Offset pagination preserves global Rank (offset + i + 1)
	paged, err := lm.GetLeaderboard(ctx, "total", 1, 2)
	assert.NoError(t, err)
	assert.Len(t, paged, 2)
	assert.Equal(t, "p1", paged[0].PlayerID)
	assert.Equal(t, 2, paged[0].Rank)
	assert.Equal(t, "p2", paged[1].PlayerID)
	assert.Equal(t, 3, paged[1].Rank)

	// Daily & weekly leaderboards only include players updated in that period
	daily, err := lm.GetLeaderboard(ctx, "daily", 0, 10)
	assert.NoError(t, err)
	assert.Len(t, daily, 2)
	assert.Equal(t, "p1", daily[0].PlayerID)
	assert.Equal(t, 1, daily[0].Rank)

	weekly, err := lm.GetLeaderboard(ctx, "weekly", 1, 1)
	assert.NoError(t, err)
	assert.Len(t, weekly, 1)
	assert.Equal(t, "p2", weekly[0].PlayerID)
	assert.Equal(t, 2, weekly[0].Rank)
}

func TestLeaderboard_GetPlayerRank(t *testing.T) {
	t.Parallel()

	lm, mr := newTestLeaderboardManager(t)
	defer mr.Close()
	ctx := context.Background()

	err := lm.RecordGameResult(ctx, "p1", "Player1", true, true) // Score 30
	assert.NoError(t, err)
	err = lm.RecordGameResult(ctx, "p2", "Player2", false, true) // Score 15
	assert.NoError(t, err)

	rank, err := lm.GetPlayerRank(ctx, "p1")
	assert.NoError(t, err)
	assert.Equal(t, int64(1), rank)

	rank, err = lm.GetPlayerRank(ctx, "p2")
	assert.NoError(t, err)
	assert.Equal(t, int64(2), rank)

	_, err = lm.GetPlayerRank(ctx, "p3")
	assert.NoError(t, err) // Returns -1, nil if not exists (based on implementation)
	// Wait, let's verify GetPlayerRank implementation returns -1 or error for nil
	// Implementation: if err == redis.Nil return -1, nil.
}
