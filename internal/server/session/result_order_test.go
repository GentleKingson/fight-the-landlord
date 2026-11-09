package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type failedResultHook struct {
	key    string
	failed chan struct{}
	once   sync.Once
}

func (h *failedResultHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *failedResultHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *failedResultHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if err != nil && cmd.Name() == "watch" && cmd.Args()[1] == h.key {
			h.once.Do(func() { close(h.failed) })
		}
		return err
	}
}

func TestSettlementRetryPreservesGameCompletionOrder(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	lb := storage.NewLeaderboardManager(rdb)
	first, second := recordingGame(t), recordingGame(t)
	first.leaderboard, second.leaderboard = lb, lb
	playerID := first.players[0].ID
	hook := &failedResultHook{key: "player:stats:" + playerID, failed: make(chan struct{})}
	rdb.AddHook(hook)
	mr.SetError("ERR unavailable")
	first.mu.Lock()
	first.setLandlord(0)
	first.endGame(first.players[1]) // The landlord loses first, with a zero score floor.
	first.mu.Unlock()
	require.NotNil(t, first.lastResult)
	select {
	case <-hook.failed:
	case <-time.After(time.Second):
		t.Fatal("first result did not attempt its initial write")
	}
	mr.SetError("")
	second.mu.Lock()
	second.setLandlord(0)
	second.endGame(second.players[0]) // A later win must stay behind the pending loss.
	second.mu.Unlock()
	require.Eventually(t, func() bool {
		for _, p := range first.players {
			stats, err := lb.GetPlayerStats(context.Background(), p.ID)
			if err != nil || stats == nil || stats.TotalGames != 2 {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond)
	stats, err := lb.GetPlayerStats(context.Background(), playerID)
	require.NoError(t, err)
	require.Equal(t, 30, stats.Score)
	require.Equal(t, 1, stats.CurrentStreak)
	require.Equal(t, 1, stats.MaxWinStreak)
	require.Equal(t, 1, stats.Wins)
	require.Equal(t, 1, stats.Losses)
}
