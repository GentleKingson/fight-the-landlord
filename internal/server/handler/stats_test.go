package handler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

type handlerLeaderboardClock struct {
	mu  sync.RWMutex
	now time.Time
}

type leaderboardHandlerFixture struct {
	handler     *Handler
	leaderboard *storage.LeaderboardManager
	clock       *handlerLeaderboardClock
	redis       *miniredis.Miniredis
}

func (c *handlerLeaderboardClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *handlerLeaderboardClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

func newLeaderboardHandler(t *testing.T) leaderboardHandlerFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	clock := &handlerLeaderboardClock{now: time.Date(2026, time.January, 5, 10, 0, 0, 0, time.UTC)}
	leaderboard := storage.NewLeaderboardManager(client, storage.WithLeaderboardClock(clock.Now))
	return leaderboardHandlerFixture{
		handler:     NewHandler(HandlerDeps{Leaderboard: leaderboard}),
		leaderboard: leaderboard,
		clock:       clock,
		redis:       mr,
	}
}

func leaderboardRequest(payload protocol.GetLeaderboardPayload) *protocol.Message {
	return codec.MustNewMessage(protocol.MsgGetLeaderboard, payload)
}

func requireLeaderboardResult(t *testing.T, client *testutil.SimpleClient) *protocol.LeaderboardResultPayload {
	t.Helper()
	require.Len(t, client.Messages, 1)
	require.Equal(t, protocol.MsgLeaderboardResult, client.Messages[0].Type)
	payload, err := codec.ParsePayload[protocol.LeaderboardResultPayload](client.Messages[0])
	require.NoError(t, err)
	return payload
}

func TestHandleGetLeaderboardUsesRequestedTypeAndOffset(t *testing.T) {
	t.Parallel()
	fixture := newLeaderboardHandler(t)
	ctx := context.Background()
	monday := fixture.clock.Now()
	require.NoError(t, fixture.leaderboard.RecordGameResult(ctx, "game-1", "p1", "Player1", true, true))
	fixture.clock.Set(monday.Add(24 * time.Hour))
	require.NoError(t, fixture.leaderboard.RecordGameResult(ctx, "game-2", "p2", "Player2", false, true))

	tests := []struct {
		name            string
		leaderboardType string
		offset          int
		wantPlayers     []string
	}{
		{name: "total", leaderboardType: storage.LeaderboardTypeTotal, wantPlayers: []string{"p1", "p2"}},
		{name: "daily", leaderboardType: storage.LeaderboardTypeDaily, wantPlayers: []string{"p2"}},
		{name: "weekly offset", leaderboardType: storage.LeaderboardTypeWeekly, offset: 1, wantPlayers: []string{"p2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := testutil.NewSimpleClient("viewer", "Viewer")
			fixture.handler.handleGetLeaderboard(client, leaderboardRequest(protocol.GetLeaderboardPayload{
				Type: tt.leaderboardType, Offset: tt.offset, Limit: 10,
			}))
			result := requireLeaderboardResult(t, client)
			assert.Equal(t, tt.leaderboardType, result.Type)
			players := make([]string, len(result.Entries))
			for i := range result.Entries {
				players[i] = result.Entries[i].PlayerID
			}
			assert.Equal(t, tt.wantPlayers, players)
			if tt.offset > 0 {
				assert.Equal(t, tt.offset+1, result.Entries[0].Rank)
			}
		})
	}
}

func TestHandleGetLeaderboardNormalizesLimitAndOffset(t *testing.T) {
	t.Parallel()
	fixture := newLeaderboardHandler(t)
	ctx := context.Background()
	for i := range 55 {
		require.NoError(t, fixture.leaderboard.RecordGameResult(
			ctx, fmt.Sprintf("game-%d", i), fmt.Sprintf("p-%02d", i), "Player", false, true,
		))
	}

	client := testutil.NewSimpleClient("viewer", "Viewer")
	fixture.handler.handleGetLeaderboard(client, leaderboardRequest(protocol.GetLeaderboardPayload{
		Type: storage.LeaderboardTypeTotal, Offset: -3, Limit: 51,
	}))
	result := requireLeaderboardResult(t, client)
	assert.Len(t, result.Entries, storage.MaxLeaderboardLimit)
	assert.Equal(t, 1, result.Entries[0].Rank)
}

func TestHandleGetLeaderboardRejectsInvalidType(t *testing.T) {
	t.Parallel()
	fixture := newLeaderboardHandler(t)
	client := testutil.NewSimpleClient("viewer", "Viewer")
	fixture.handler.handleGetLeaderboard(client, leaderboardRequest(protocol.GetLeaderboardPayload{
		Type: "monthly", Limit: 10,
	}))

	require.Len(t, client.Messages, 1)
	require.Equal(t, protocol.MsgError, client.Messages[0].Type)
	payload, err := codec.ParsePayload[protocol.ErrorPayload](client.Messages[0])
	require.NoError(t, err)
	assert.Equal(t, protocol.ErrCodeInvalidMsg, payload.Code)
}

func TestHandleGetLeaderboardReportsRedisError(t *testing.T) {
	t.Parallel()
	fixture := newLeaderboardHandler(t)
	fixture.redis.Close()
	client := testutil.NewSimpleClient("viewer", "Viewer")
	fixture.handler.handleGetLeaderboard(client, leaderboardRequest(protocol.GetLeaderboardPayload{
		Type: storage.LeaderboardTypeTotal, Limit: 10,
	}))

	require.Len(t, client.Messages, 1)
	require.Equal(t, protocol.MsgError, client.Messages[0].Type)
	payload, err := codec.ParsePayload[protocol.ErrorPayload](client.Messages[0])
	require.NoError(t, err)
	assert.Equal(t, protocol.ErrCodeUnknown, payload.Code)
}
