package session

import (
	"testing"

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
