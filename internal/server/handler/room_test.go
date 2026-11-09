package handler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/palemoky/fight-the-landlord/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRoomOperationsCannotRemoveActivePlayer(t *testing.T) {
	rm := room.NewRoomManager(nil, config.GameConfig{})
	clients := []types.ClientInterface{testutil.NewSimpleClient("a", "A"), testutil.NewSimpleClient("b", "B"), testutil.NewSimpleClient("c", "C")}
	r, err := rm.CreateMatchedRoom(clients)
	require.NoError(t, err)
	server := new(testutil.MockServer)
	server.On("IsMaintenanceMode").Return(false)
	h := NewHandler(HandlerDeps{Server: server, RoomManager: rm})
	// Matcher is deliberately nil: none of these requests may reach it.
	for _, msg := range []*protocol.Message{
		{Type: protocol.MsgLeaveRoom}, {Type: protocol.MsgCreateRoom},
		codec.MustNewMessage(protocol.MsgJoinRoom, protocol.JoinRoomPayload{RoomCode: "123456"}),
		{Type: protocol.MsgQuickMatch}, {Type: protocol.MsgPracticeMatch},
	} {
		h.handlers[msg.Type](clients[0], msg)
		require.Equal(t, r.Code, clients[0].GetRoom())
		require.Len(t, r.Players, 3)
		messages := clients[0].(*testutil.SimpleClient).SentMessages()
		require.Equal(t, protocol.MsgError, messages[len(messages)-1].Type)
	}
}

func TestAllOfflineRetentionThenRemovesGameRegistryAndClosesRecording(t *testing.T) {
	cfg := config.GameConfig{BidTimeout: 600, TurnTimeout: 600, OfflineRoomRetention: 1, CollectTrainingData: true, TrainingDataDir: t.TempDir()}
	rm := room.NewRoomManager(nil, cfg)
	clients := []types.ClientInterface{testutil.NewSimpleClient("a", "A"), testutil.NewSimpleClient("b", "B"), testutil.NewSimpleClient("c", "C")}
	r, err := rm.CreateMatchedRoom(clients)
	require.NoError(t, err)
	h := NewHandler(HandlerDeps{RoomManager: rm})
	gs := session.NewGameSession(r, nil, cfg)
	h.SetGameSession(r.Code, gs)
	gs.Start()
	t.Cleanup(gs.StopAllTimers)
	for _, c := range clients {
		rm.NotifyPlayerOffline(c)
	}
	require.Eventually(t, func() bool { return rm.GetRoom(r.Code) == nil }, 3*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return h.GetGameSession(r.Code) == nil }, time.Second, time.Millisecond)
	var files []string
	require.Eventually(t, func() bool {
		files, _ = filepath.Glob(filepath.Join(cfg.TrainingDataDir, "*", "*.jsonl"))
		return len(files) == 1
	}, time.Second, time.Millisecond)
	data, err := os.ReadFile(files[0])
	require.NoError(t, err)
	require.Contains(t, string(data), `"status":"aborted"`)
	require.Error(t, gs.HandleBid(clients[0].GetID(), true))
	// A delayed game-start callback cannot register or revive a removed room.
	h.SetGameSession(r.Code, gs)
	gs.Start()
	require.Nil(t, h.GetGameSession(r.Code))
	require.Equal(t, session.GameStateEnded, gs.GetStateForSerialization())
}

func TestDelayedCleanupCannotRemoveReusedRoomCode(t *testing.T) {
	rm := room.NewRoomManager(nil, config.GameConfig{})
	h := NewHandler(HandlerDeps{RoomManager: rm})
	oldRoom := room.NewMockRoom("123456", nil)
	newRoom := room.NewMockRoom("123456", nil)
	oldGame := session.NewGameSession(oldRoom, nil, config.GameConfig{})
	newGame := session.NewGameSession(newRoom, nil, config.GameConfig{})
	rm.AddRoomForTest(newRoom)
	h.SetGameSession(newRoom.Code, newGame)
	h.removeGameSession(oldRoom)
	require.Same(t, newGame, h.GetGameSession(newRoom.Code))
	h.SetGameSession(oldRoom.Code, oldGame)
	require.Same(t, newGame, h.GetGameSession(newRoom.Code))
}
