package session

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestOnlineReconnectPreservesDeadline(t *testing.T) {
	gs := recordingGame(t)
	player := gs.players[gs.currentBidder].ID
	timer, start, turn := gs.turnTimer, gs.timerStartTime, gs.turnID
	for range 10 {
		gs.PlayerOnline(player)
	}
	require.Same(t, timer, gs.turnTimer)
	require.Equal(t, start, gs.timerStartTime)
	require.Equal(t, turn, gs.turnID)
}

func TestReconnectResumesOnlyPausedTimer(t *testing.T) {
	gs := recordingGame(t)
	player := gs.players[gs.currentBidder].ID
	gs.gameConfig.OfflineWaitTimeout = 600
	gs.PlayerOffline(player)
	require.Nil(t, gs.turnTimer)
	remaining := gs.remainingTime
	gs.PlayerOnline(player)
	require.NotNil(t, gs.turnTimer)
	require.Nil(t, gs.offlineWaitTimer)
	require.Equal(t, remaining, gs.remainingTime)
	timer, start, turn := gs.turnTimer, gs.timerStartTime, gs.turnID
	gs.PlayerOnline(player)
	require.Same(t, timer, gs.turnTimer)
	require.Equal(t, start, gs.timerStartTime)
	require.Equal(t, turn, gs.turnID)
}

// Block snapshot enqueue to force a game-ending broadcast to race with reconnect.
type reconnectClient struct {
	*testutil.SimpleClient
	snapshot chan struct{}
	release  chan struct{}
}

func (c *reconnectClient) SendMessage(msg *protocol.Message) {
	if msg.Type == protocol.MsgReconnected {
		close(c.snapshot)
		<-c.release
	}
	c.SimpleClient.SendMessage(msg)
}

func TestReconnectSnapshotPrecedesConcurrentGameOver(t *testing.T) {
	cfg := config.GameConfig{BidTimeout: 600, TurnTimeout: 600}
	manager := room.NewRoomManager(nil, cfg)
	old := testutil.NewSimpleClient("p1", "P1")
	r, err := manager.CreateRoom(old)
	require.NoError(t, err)
	for _, id := range []string{"p2", "p3"} {
		_, err = manager.JoinRoom(testutil.NewSimpleClient(id, id), r.Code)
		require.NoError(t, err)
	}
	gs := NewGameSession(r, nil, cfg)
	gs.Start()
	gs.finishDeal(gs.dealID)
	t.Cleanup(gs.StopAllTimers)
	client := &reconnectClient{SimpleClient: testutil.NewSimpleClient("p1", "P1"), snapshot: make(chan struct{}), release: make(chan struct{})}
	client.SetRoom(r.Code)
	reconnected := make(chan error, 1)
	go func() {
		reconnected <- gs.ReconnectClient(manager, client, NewSessionManager(), &protocol.ReconnectedPayload{PlayerID: "p1"})
	}()
	<-client.snapshot
	// The snapshot cannot be overtaken by any game mutation or broadcast.
	locked := gs.mu.TryLock()
	if locked {
		gs.mu.Unlock()
	}
	require.False(t, locked)
	ended := make(chan struct{})
	go func() {
		gs.mu.Lock()
		gs.endGame(gs.players[0])
		gs.mu.Unlock()
		close(ended)
	}()
	close(client.release)
	require.NoError(t, <-reconnected)
	<-ended
	require.Equal(t, protocol.MsgReconnected, client.Messages[0].Type)
	require.Equal(t, protocol.MsgGameOver, client.Messages[len(client.Messages)-1].Type)
}

func TestReconnectAfterGameOverReplaysSettlement(t *testing.T) {
	gs := recordingGame(t)
	gs.mu.Lock()
	gs.endGame(gs.players[0])
	gs.mu.Unlock()
	client := testutil.NewSimpleClient(gs.players[0].ID, "P1")
	err := gs.ReconnectClient(room.NewRoomManager(nil, gs.gameConfig), client, NewSessionManager(), &protocol.ReconnectedPayload{})
	require.NoError(t, err)
	require.Len(t, client.Messages, 2)
	require.Equal(t, protocol.MsgReconnected, client.Messages[0].Type)
	require.Equal(t, protocol.MsgGameOver, client.Messages[1].Type)
	require.Empty(t, client.GetRoom())
}

func TestReconnectSnapshotContainsOnlyOwnHandAndPublicHistory(t *testing.T) {
	gs := recordingGame(t)
	gs.mu.Lock()
	gs.stopTimer()
	gs.setLandlord(0)
	gs.bidMultiplier = 4
	gs.bombCount = 2
	gs.mu.Unlock()
	sessions := NewSessionManager()
	for _, p := range gs.players {
		sessions.CreateSession(p.ID, p.Name)
	}
	first := gs.players[0]
	require.NoError(t, gs.HandlePlayCards(first.ID, convert.CardsToInfos(first.Hand[:1])))
	require.NoError(t, gs.HandlePass(gs.players[1].ID))
	require.NoError(t, gs.HandlePass(gs.players[2].ID))
	for _, player := range gs.players {
		dto := gs.BuildGameStateDTO(player.ID, sessions)
		require.Equal(t, 16, dto.Multiplier)
		require.Equal(t, convert.CardsToInfos(player.Hand), dto.Hand)
		require.Len(t, dto.PublicPlays, 3)
		require.Equal(t, "play", dto.PublicPlays[0].Type)
		require.Equal(t, "pass", dto.PublicPlays[1].Type)
		require.True(t, dto.PublicPlays[0].NewTrick)
		require.Greater(t, dto.TurnDeadline, dto.ServerTime)
		require.InDelta(t, 600_000, dto.TurnDeadline-dto.ServerTime, 1000)
		require.Empty(t, dto.LastPlayed, "two passes clear the active trick, not public history")
		wire := codec.MustNewMessage(protocol.MsgReconnected, protocol.ReconnectedPayload{GameState: dto})
		decoded, err := codec.ParsePayload[protocol.ReconnectedPayload](wire)
		require.NoError(t, err)
		require.Equal(t, dto, decoded.GameState)
		dto.PublicPlays[0].Cards[0].Rank = 999
	}
	require.Nil(t, gs.BuildGameStateDTO("outsider", sessions))
}

func TestReturningAfterAutoPlayAndAllOfflineSettlement(t *testing.T) {
	gs := recordingGame(t)
	gs.mu.Lock()
	gs.stopTimer()
	gs.setLandlord(0)
	gs.gameConfig.OfflineWaitTimeout = 600
	gs.mu.Unlock()
	sessions := NewSessionManager()
	manager := room.NewRoomManager(nil, gs.gameConfig)
	manager.AddRoomForTest(gs.room)
	for _, p := range gs.players {
		sessions.CreateSession(p.ID, p.Name)
		c := gs.room.Players[p.ID].Client
		c.SetRoom(gs.room.Code)
		sessions.SetOffline(p.ID)
		gs.PlayerOffline(p.ID)
		manager.NotifyPlayerOffline(c)
	}
	require.Same(t, gs.room, manager.GetRoom(gs.room.Code))
	gs.handleOfflineTimeout(gs.players[0].ID)
	require.NotEmpty(t, gs.publicPlays)
	returning := testutil.NewSimpleClient(gs.players[0].ID, gs.players[0].Name)
	returning.SetRoom(gs.room.Code)
	sessions.SetOnline(returning.ID)
	require.NoError(t, gs.ReconnectClient(manager, returning, sessions, &protocol.ReconnectedPayload{}))
	require.False(t, gs.players[0].IsOffline)
	// Everyone leaves again; timeout auto-play is still allowed to finish the retained game.
	gs.PlayerOffline(returning.ID)
	manager.NotifyPlayerOffline(returning)
	for turn := 0; gs.state != GameStateEnded && turn < 300; turn++ {
		gs.handlePlayTimeout()
	}
	require.Equal(t, GameStateEnded, gs.state)
	result := gs.lastResult
	returning.SetRoom(gs.room.Code)
	require.NoError(t, gs.ReconnectClient(manager, returning, sessions, &protocol.ReconnectedPayload{}))
	messages := returning.SentMessages()
	require.Equal(t, protocol.MsgGameOver, messages[len(messages)-1].Type)
	require.Equal(t, result, messages[len(messages)-1])
	require.Empty(t, returning.GetRoom())
}

func TestSettlementRetriesStatisticsAfterRedisRecovers(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer redisClient.Close()
	gs := recordingGame(t)
	gs.leaderboard = storage.NewLeaderboardManager(redisClient)
	redisServer.SetError("ERR unavailable")
	gs.mu.Lock()
	gs.stopTimer()
	gs.setLandlord(0)
	gs.endGame(gs.players[0])
	gs.mu.Unlock()
	require.NotNil(t, gs.lastResult, "settlement must still be available while Redis is down")
	redisServer.SetError("")
	require.Eventually(t, func() bool {
		for _, p := range gs.players {
			stats, err := gs.leaderboard.GetPlayerStats(context.Background(), p.ID)
			if err != nil || stats == nil || stats.TotalGames != 1 {
				return false
			}
		}
		return true
	}, 5*time.Second, 20*time.Millisecond)
}
