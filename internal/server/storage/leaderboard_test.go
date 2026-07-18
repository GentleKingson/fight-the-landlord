package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLeaderboardClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *fakeLeaderboardClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *fakeLeaderboardClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

func newTestLeaderboardManager(t *testing.T) (*LeaderboardManager, *miniredis.Miniredis, *fakeLeaderboardClock) {
	t.Helper()
	mr := miniredis.RunT(t)
	clock := &fakeLeaderboardClock{now: time.Date(2026, time.January, 5, 10, 0, 0, 0, time.UTC)}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewLeaderboardManager(client, WithLeaderboardClock(clock.Now)), mr, clock
}

func TestLeaderboardRecordGameResult(t *testing.T) {
	t.Parallel()
	lm, _, _ := newTestLeaderboardManager(t)
	ctx := context.Background()

	require.NoError(t, lm.RecordGameResult(ctx, "game-1", "p1", "Player1", true, true))
	stats, err := lm.GetPlayerStats(ctx, "p1")
	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.Equal(t, 1, stats.TotalGames)
	assert.Equal(t, 1, stats.Wins)
	assert.Equal(t, 1, stats.LandlordGames)
	assert.Equal(t, 1, stats.LandlordWins)
	assert.Equal(t, 30, stats.Score)
	assert.Equal(t, 1, stats.CurrentStreak)

	require.NoError(t, lm.RecordGameResult(ctx, "game-2", "p1", "Player1", true, false))
	stats, err = lm.GetPlayerStats(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, 2, stats.TotalGames)
	assert.Equal(t, 1, stats.Losses)
	assert.Equal(t, 10, stats.Score)
	assert.Equal(t, -1, stats.CurrentStreak)
}

func TestLeaderboardStreakBonus(t *testing.T) {
	t.Parallel()
	lm, _, _ := newTestLeaderboardManager(t)
	ctx := context.Background()

	for i := range 3 {
		require.NoError(t, lm.RecordGameResult(
			ctx, fmt.Sprintf("game-%d", i), "p1", "Player1", false, true,
		))
	}
	stats, err := lm.GetPlayerStats(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, 50, stats.Score)
	assert.Equal(t, 3, stats.CurrentStreak)
}

func TestLeaderboardPeriodicScoresAndClockBoundaries(t *testing.T) {
	t.Parallel()
	lm, _, clock := newTestLeaderboardManager(t)
	ctx := context.Background()
	monday := clock.Now()

	require.NoError(t, lm.RecordGameResult(ctx, "game-1", "p1", "Player1", false, true))
	require.NoError(t, lm.RecordGameResult(ctx, "game-2", "p2", "Player2", true, true))

	daily, err := lm.GetLeaderboard(ctx, LeaderboardTypeDaily, 0, 10)
	require.NoError(t, err)
	require.Len(t, daily, 2)
	assert.Equal(t, "p2", daily[0].PlayerID)
	assert.Equal(t, 30, daily[0].Score)
	assert.Equal(t, 15, daily[1].Score)

	clock.Set(monday.Add(24 * time.Hour))
	require.NoError(t, lm.RecordGameResult(ctx, "game-3", "p1", "Player1", true, true))
	daily, err = lm.GetLeaderboard(ctx, LeaderboardTypeDaily, 0, 10)
	require.NoError(t, err)
	require.Len(t, daily, 1)
	assert.Equal(t, 30, daily[0].Score, "daily score must not contain lifetime points")

	weekly, err := lm.GetLeaderboard(ctx, LeaderboardTypeWeekly, 0, 10)
	require.NoError(t, err)
	require.Len(t, weekly, 2)
	assert.Equal(t, 45, weekly[0].Score)

	clock.Set(monday.Add(7 * 24 * time.Hour))
	require.NoError(t, lm.RecordGameResult(ctx, "game-4", "p1", "Player1", false, false))
	weekly, err = lm.GetLeaderboard(ctx, LeaderboardTypeWeekly, 0, 10)
	require.NoError(t, err)
	require.Len(t, weekly, 1)
	assert.Equal(t, -10, weekly[0].Score, "weekly score must contain only this week's change")

	total, err := lm.GetLeaderboard(ctx, LeaderboardTypeTotal, 0, 10)
	require.NoError(t, err)
	require.Len(t, total, 2)
	assert.Equal(t, 35, total[0].Score)

	clock.Set(monday)
	daily, err = lm.GetLeaderboard(ctx, LeaderboardTypeDaily, 0, 10)
	require.NoError(t, err)
	require.Len(t, daily, 2)
	assert.Equal(t, 15, daily[1].Score)
}

func TestLeaderboardWholeGameStaysInSettlementTimeBuckets(t *testing.T) {
	t.Parallel()
	lm, _, clock := newTestLeaderboardManager(t)
	ctx := context.Background()
	settledAt := time.Date(2026, time.January, 11, 23, 59, 59, 0, time.UTC)
	afterBoundary := settledAt.Add(2 * time.Second)

	require.NoError(t, lm.RecordGameResultAt(
		ctx, "boundary-game", settledAt, "p1", "Player1", true, true,
	))
	clock.Set(afterBoundary)
	require.NoError(t, lm.RecordGameResultAt(
		ctx, "boundary-game", settledAt, "p2", "Player2", false, false,
	))

	clock.Set(settledAt)
	daily, err := lm.GetLeaderboard(ctx, LeaderboardTypeDaily, 0, 10)
	require.NoError(t, err)
	require.Len(t, daily, 2)
	weekly, err := lm.GetLeaderboard(ctx, LeaderboardTypeWeekly, 0, 10)
	require.NoError(t, err)
	require.Len(t, weekly, 2)

	clock.Set(afterBoundary)
	daily, err = lm.GetLeaderboard(ctx, LeaderboardTypeDaily, 0, 10)
	require.NoError(t, err)
	assert.Empty(t, daily, "one game's players must not cross the midnight bucket")
	weekly, err = lm.GetLeaderboard(ctx, LeaderboardTypeWeekly, 0, 10)
	require.NoError(t, err)
	assert.Empty(t, weekly, "one game's players must not cross the ISO week bucket")
}

func TestLeaderboardPeriodicScoreUsesAppliedDelta(t *testing.T) {
	t.Parallel()
	lm, _, _ := newTestLeaderboardManager(t)
	ctx := context.Background()

	require.NoError(t, lm.RecordGameResult(ctx, "game-1", "p1", "Player1", false, false))
	stats, err := lm.GetPlayerStats(ctx, "p1")
	require.NoError(t, err)
	assert.Zero(t, stats.Score)

	daily, err := lm.GetLeaderboard(ctx, LeaderboardTypeDaily, 0, 10)
	require.NoError(t, err)
	require.Len(t, daily, 1)
	assert.Zero(t, daily[0].Score, "period score must reflect the score actually applied after the zero floor")
}

func TestLeaderboardPaginationAndLimitBounds(t *testing.T) {
	t.Parallel()
	lm, _, _ := newTestLeaderboardManager(t)
	ctx := context.Background()

	for i := range 55 {
		id := fmt.Sprintf("p-%02d", i)
		stats := &PlayerStats{PlayerID: id, PlayerName: id, Score: i, TotalGames: 1}
		require.NoError(t, lm.SavePlayerStats(ctx, stats))
		require.NoError(t, lm.redis.ZAdd(ctx, leaderboardKey, redis.Z{Score: float64(i), Member: id}).Err())
	}

	entries, err := lm.GetLeaderboard(ctx, LeaderboardTypeTotal, 10, 5)
	require.NoError(t, err)
	require.Len(t, entries, 5)
	assert.Equal(t, 11, entries[0].Rank)
	assert.Equal(t, "p-44", entries[0].PlayerID)

	entries, err = lm.GetLeaderboard(ctx, LeaderboardTypeTotal, -5, 100)
	require.NoError(t, err)
	assert.Len(t, entries, MaxLeaderboardLimit)
	assert.Equal(t, 1, entries[0].Rank)

	entries, err = lm.GetLeaderboard(ctx, LeaderboardTypeTotal, 0, 0)
	require.NoError(t, err)
	assert.Len(t, entries, DefaultLeaderboardLimit)
}

func TestLeaderboardRejectsInvalidType(t *testing.T) {
	t.Parallel()
	lm, _, _ := newTestLeaderboardManager(t)
	_, err := lm.GetLeaderboard(context.Background(), "monthly", 0, 10)
	assert.ErrorIs(t, err, ErrInvalidLeaderboardType)
}

func TestLeaderboardDeduplicatesSettlement(t *testing.T) {
	t.Parallel()
	lm, mr, clock := newTestLeaderboardManager(t)
	ctx := context.Background()

	require.NoError(t, lm.RecordGameResult(ctx, "game-1", "p1", "Player1", false, true))
	settlementTTL, err := lm.redis.TTL(ctx, settledGameKey+"game-1").Result()
	require.NoError(t, err)
	assert.Greater(t, settlementTTL, 29*24*time.Hour)
	assert.LessOrEqual(t, settlementTTL, settlementMarkerTTL)
	mr.FastForward(29 * 24 * time.Hour)
	clock.Set(clock.Now().Add(29 * 24 * time.Hour))
	require.NoError(t, lm.RecordGameResult(ctx, "game-1", "p1", "Renamed", true, false))
	require.NoError(t, lm.RecordGameResult(ctx, "game-1", "p2", "Player2", true, false))

	stats, err := lm.GetPlayerStats(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TotalGames)
	assert.Equal(t, 15, stats.Score)
	assert.Equal(t, "Player1", stats.PlayerName)

	stats, err = lm.GetPlayerStats(ctx, "p2")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TotalGames, "different players in one game must each settle")

	weekly, err := lm.GetLeaderboard(ctx, LeaderboardTypeWeekly, 0, 10)
	require.NoError(t, err)
	require.Len(t, weekly, 1)
	assert.Equal(t, "p2", weekly[0].PlayerID)
}

func TestLeaderboardSettlementMarkerExpires(t *testing.T) {
	t.Parallel()
	lm, mr, _ := newTestLeaderboardManager(t)
	ctx := context.Background()

	require.NoError(t, lm.RecordGameResult(ctx, "game-1", "p1", "Player1", false, true))
	mr.FastForward(settlementMarkerTTL + time.Second)

	exists, err := lm.redis.Exists(ctx, settledGameKey+"game-1").Result()
	require.NoError(t, err)
	assert.Zero(t, exists)
}

func TestLeaderboardConcurrentSettlementsDoNotLoseUpdates(t *testing.T) {
	t.Parallel()
	lm, _, _ := newTestLeaderboardManager(t)
	ctx := context.Background()

	const games = 20
	errs := make(chan error, games)
	var wg sync.WaitGroup
	for i := range games {
		wg.Add(1)
		go func(game int) {
			defer wg.Done()
			errs <- lm.RecordGameResult(
				ctx, fmt.Sprintf("game-%d", game), "p1", "Player1", false, true,
			)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	stats, err := lm.GetPlayerStats(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, games, stats.TotalGames)
	assert.Equal(t, games, stats.Wins)

	total, err := lm.GetLeaderboard(ctx, LeaderboardTypeTotal, 0, 10)
	require.NoError(t, err)
	daily, err := lm.GetLeaderboard(ctx, LeaderboardTypeDaily, 0, 10)
	require.NoError(t, err)
	require.Len(t, total, 1)
	require.Len(t, daily, 1)
	assert.Equal(t, total[0].Score, daily[0].Score)
}

func TestLeaderboardRedisErrorsAndAtomicValidation(t *testing.T) {
	t.Parallel()
	lm, mr, clock := newTestLeaderboardManager(t)
	ctx := context.Background()

	require.NoError(t, lm.redis.Set(ctx, playerStatsKey+"p1", "not-json", 0).Err())
	err := lm.RecordGameResult(ctx, "game-1", "p1", "Player1", false, true)
	require.Error(t, err)
	totalCount, countErr := lm.redis.ZCard(ctx, leaderboardKey).Result()
	require.NoError(t, countErr)
	dailyCount, countErr := lm.redis.ZCard(ctx, dailyLeaderboardKey(clock.Now())).Result()
	require.NoError(t, countErr)
	settled, settledErr := lm.redis.SIsMember(ctx, settledGameKey+"game-1", "p1").Result()
	require.NoError(t, settledErr)
	assert.Zero(t, totalCount)
	assert.Zero(t, dailyCount)
	assert.False(t, settled)

	mr.Close()
	_, err = lm.GetLeaderboard(ctx, LeaderboardTypeTotal, 0, 10)
	assert.Error(t, err)
	err = lm.RecordGameResult(ctx, "game-2", "p2", "Player2", false, true)
	assert.Error(t, err)
}

func TestLeaderboardGetPlayerRank(t *testing.T) {
	t.Parallel()
	lm, _, _ := newTestLeaderboardManager(t)
	ctx := context.Background()
	require.NoError(t, lm.RecordGameResult(ctx, "game-1", "p1", "Player1", true, true))
	require.NoError(t, lm.RecordGameResult(ctx, "game-2", "p2", "Player2", false, true))

	rank, err := lm.GetPlayerRank(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), rank)
	rank, err = lm.GetPlayerRank(ctx, "p2")
	require.NoError(t, err)
	assert.Equal(t, int64(2), rank)
	rank, err = lm.GetPlayerRank(ctx, "missing")
	require.NoError(t, err)
	assert.Equal(t, int64(-1), rank)
}

func TestLeaderboardRequiresSettlementIdentity(t *testing.T) {
	t.Parallel()
	lm, _, _ := newTestLeaderboardManager(t)
	err := lm.RecordGameResult(context.Background(), "", "p1", "Player1", false, true)
	assert.True(t, errors.Is(err, ErrInvalidGameResult))
	err = lm.RecordGameResultAt(context.Background(), "game-1", time.Time{}, "p1", "Player1", false, true)
	assert.ErrorIs(t, err, ErrInvalidGameResult)
}
