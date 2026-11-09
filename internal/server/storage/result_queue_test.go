package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type blockedResultHook struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockedResultHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *blockedResultHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *blockedResultHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "watch" && cmd.Args()[1] == playerStatsKey+"blocked" {
			h.once.Do(func() { close(h.entered) })
			<-h.release
		}
		return next(ctx, cmd)
	}
}

func TestResultQueueIsolatesPlayersAndDeduplicatesRetries(t *testing.T) {
	lb, mr := newTestLeaderboardManager(t)
	defer mr.Close()
	defer lb.redis.Close()
	hook := &blockedResultHook{entered: make(chan struct{}), release: make(chan struct{})}
	lb.redis.AddHook(hook)
	var release sync.Once
	defer release.Do(func() { close(hook.release) })
	blocked := GameResult{GameID: "first", PlayerID: "blocked", PlayerName: "A", IsLandlord: true, IsWinner: true}
	lb.QueueGameResult(blocked)
	select {
	case <-hook.entered:
	case <-time.After(time.Second):
		t.Fatal("write did not reach Redis")
	}
	lb.QueueGameResult(blocked) // Duplicate delivery must remain idempotent.
	lb.QueueGameResult(GameResult{GameID: "second", PlayerID: "blocked", PlayerName: "A", IsLandlord: true, IsWinner: true})
	lb.QueueGameResult(GameResult{GameID: "other", PlayerID: "independent", PlayerName: "B", IsWinner: true})
	require.Eventually(t, func() bool {
		stats, err := lb.GetPlayerStats(context.Background(), "independent")
		return err == nil && stats != nil && stats.TotalGames == 1
	}, time.Second, time.Millisecond)
	release.Do(func() { close(hook.release) })
	require.Eventually(t, func() bool {
		lb.resultsMu.Lock()
		defer lb.resultsMu.Unlock()
		return len(lb.results) == 0
	}, time.Second, time.Millisecond)
	stats, err := lb.GetPlayerStats(context.Background(), "blocked")
	require.NoError(t, err)
	require.Equal(t, 2, stats.TotalGames)
	require.Equal(t, 60, stats.Score)
	require.Equal(t, 2, stats.CurrentStreak)
}
