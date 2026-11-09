package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type blockedStatsHook struct {
	key     string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockedStatsHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *blockedStatsHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *blockedStatsHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "get" && cmd.Args()[1] == h.key {
			h.once.Do(func() { close(h.entered) })
			<-h.release
		}
		return next(ctx, cmd)
	}
}

func TestSlowRedisQueryDoesNotBlockOtherIdentity(t *testing.T) {
	for _, query := range []protocol.MessageType{protocol.MsgGetStats, protocol.MsgGetLeaderboard} {
		t.Run(string(query), func(t *testing.T) {
			s, web := identityServer(t, miniredis.RunT(t))
			a, aCookies := dialIdentity(t, web.URL, nil, true)
			identityA := receivePayload[protocol.ReconnectedPayload](t, a, protocol.MsgReconnected)
			oldA := s.GetClientByID(identityA.PlayerID).(*Client)
			b, cookies := dialIdentity(t, web.URL, nil, true)
			identityB := receivePayload[protocol.ReconnectedPayload](t, b, protocol.MsgReconnected)
			require.NoError(t, s.leaderboard.RecordGameResultOnce(context.Background(), "seed", identityA.PlayerID, "A", true, true))
			hook := &blockedStatsHook{key: "player:stats:" + identityA.PlayerID, entered: make(chan struct{}), release: make(chan struct{})}
			s.redis.AddHook(hook)
			defer close(hook.release)
			var payload any
			if query == protocol.MsgGetLeaderboard {
				payload = protocol.GetLeaderboardPayload{Type: "total", Limit: 10}
			}
			sendIdentityMessage(t, a, query, payload)
			select {
			case <-hook.entered:
			case <-time.After(time.Second):
				t.Fatal("query never reached Redis")
			}
			// A's Redis call stays blocked for all of B's operations, including
			// replacement and disconnect, not merely a read-only heartbeat.
			sendIdentityMessage(t, b, protocol.MsgPing, protocol.PingPayload{Timestamp: 123})
			require.NoError(t, b.SetReadDeadline(time.Now().Add(time.Second)))
			_, data, err := b.ReadMessage()
			require.NoError(t, err, "another identity must not wait for the query")
			msg, err := codec.Decode(data)
			require.NoError(t, err)
			require.Equal(t, protocol.MsgPong, msg.Type)
			codec.PutMessage(msg)
			sendIdentityMessage(t, b, protocol.MsgCreateRoom, nil)
			created := receivePayload[protocol.RoomCreatedPayload](t, b, protocol.MsgRoomCreated)
			next, _ := dialIdentity(t, web.URL, cookies[0], true)
			restored := receivePayload[protocol.ReconnectedPayload](t, next, protocol.MsgReconnected)
			require.Equal(t, created.RoomCode, restored.RoomCode)
			require.NoError(t, next.Close())
			require.Eventually(t, func() bool { return !s.sessionManager.IsOnline(identityB.PlayerID) }, time.Second, time.Millisecond)
			// Even the querying identity can refresh while its old query is blocked.
			// The displaced socket must still be unable to mutate room/match state.
			replacement, _ := dialIdentity(t, web.URL, aCookies[0], true)
			recovered := receivePayload[protocol.ReconnectedPayload](t, replacement, protocol.MsgReconnected)
			require.Equal(t, identityA.PlayerID, recovered.PlayerID)
			oldA.handleMessage(codec.MustNewMessage(protocol.MsgCreateRoom, nil))
			oldA.handleMessage(codec.MustNewMessage(protocol.MsgQuickMatch, nil))
			require.Empty(t, s.GetClientByID(identityA.PlayerID).GetRoom())
			require.Zero(t, s.matcher.GetQueueLength())
		})
	}
}
