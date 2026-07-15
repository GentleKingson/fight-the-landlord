package handler

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

type conditionalRemovalClient struct {
	*synchronizedClient
	entered chan struct{}
	release chan struct{}
	calls   int
}

func (client *conditionalRemovalClient) SendMessageIfRoom(expectedRoom string, message *protocol.Message) (bool, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.calls++
	if client.roomCode != expectedRoom {
		return false, nil
	}
	if client.entered != nil {
		close(client.entered)
		<-client.release
	}
	client.messages = append(client.messages, message)
	return true, nil
}

func (client *conditionalRemovalClient) conditionalCalls() int {
	client.mu.RLock()
	defer client.mu.RUnlock()
	return client.calls
}

func newRemovalSession(t *testing.T, code string) (*room.Room, *session.GameSession, []*testutil.SimpleClient) {
	t.Helper()
	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient(code+"-p1", "Player 1"),
		testutil.NewSimpleClient(code+"-p2", "Player 2"),
		testutil.NewSimpleClient(code+"-p3", "Player 3"),
	}
	gameRoom := room.NewMockRoom(code, clients[0])
	for index, client := range clients {
		client.SetRoom(code)
		if index > 0 {
			gameRoom.AddPlayerForTest(client, index, true)
		}
	}
	gameRoom.SetPlayerOrderForTest([]string{clients[0].GetID(), clients[1].GetID(), clients[2].GetID()})
	game := session.NewGameSession(gameRoom, nil, config.GameConfig{BidTimeout: 3600, TurnTimeout: 3600})
	return gameRoom, game, clients
}

func TestRemovedRoomRetiresOnlyItsExactRegisteredSession(t *testing.T) {
	rm := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	h := NewHandler(HandlerDeps{RoomManager: rm})

	oldRoom, oldGame, _ := newRemovalSession(t, "reused")
	rm.AddRoomForTest(oldRoom)
	require.True(t, h.SetGameSession(oldRoom.Code, oldGame))

	newRoom, newGame, _ := newRemovalSession(t, "reused")
	rm.AddRoomForTest(newRoom)
	require.True(t, h.SetGameSession(newRoom.Code, newGame))
	require.True(t, oldGame.IsRetiredForTest(), "replacing a registration must retire its old timers")

	h.handleRoomRemoved(room.RoomRemoval{Code: oldRoom.Code, Room: oldRoom, Reason: room.RoomRemovalRollback})
	require.Same(t, newGame, h.GetGameSession(newRoom.Code))
	require.False(t, newGame.IsRetiredForTest(), "a stale removal must not retire a reused room code")

	h.handleRoomRemoved(room.RoomRemoval{Code: newRoom.Code, Room: newRoom, Reason: room.RoomRemovalRollback})
	require.Nil(t, h.GetGameSession(newRoom.Code))
	require.True(t, newGame.IsRetiredForTest())
}

func TestSetGameSessionRejectsRemovedOrReusedRoomIdentity(t *testing.T) {
	rm := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	h := NewHandler(HandlerDeps{RoomManager: rm})

	oldRoom, staleGame, _ := newRemovalSession(t, "reused-before-register")
	replacement, replacementGame, _ := newRemovalSession(t, oldRoom.Code)
	rm.AddRoomForTest(oldRoom)
	rm.AddRoomForTest(replacement)

	require.False(t, h.SetGameSession(oldRoom.Code, staleGame))
	require.True(t, staleGame.IsRetiredForTest())
	require.Nil(t, h.GetGameSession(oldRoom.Code))
	require.True(t, h.SetGameSession(replacement.Code, replacementGame))
	require.Same(t, replacementGame, h.GetGameSession(replacement.Code))

	removedRoom, removedGame, _ := newRemovalSession(t, "removed-before-register")
	rm.AddRoomForTest(removedRoom)
	require.True(t, rm.RemoveRoom(removedRoom, room.RoomRemovalRollback))
	require.False(t, h.SetGameSession(removedRoom.Code, removedGame))
	require.True(t, removedGame.IsRetiredForTest())
	require.Nil(t, h.GetGameSession(removedRoom.Code))
}

func TestRoomRemovalDeliveryIsAtomicWithReplacementRoomBinding(t *testing.T) {
	terminalRemoval := func(code string) room.RoomRemoval {
		return room.RoomRemoval{
			Code:   code,
			Room:   room.NewMockRoom(code, nil),
			Reason: room.RoomRemovalRollback,
		}
	}

	t.Run("terminal enqueue wins before replacement binding", func(t *testing.T) {
		client := &conditionalRemovalClient{
			synchronizedClient: &synchronizedClient{id: "atomic-terminal-first", name: "Player"},
			entered:            make(chan struct{}),
			release:            make(chan struct{}),
		}
		removal := terminalRemoval("old-room")
		removal.Players = []room.PlayerSnapshot{{ID: client.GetID(), Client: client}}
		handler := NewHandler(HandlerDeps{})
		handlerDone := make(chan struct{})
		go func() {
			handler.handleRoomRemoved(removal)
			close(handlerDone)
		}()

		waitHandlerSignal(t, client.entered, "conditional terminal delivery")
		bindStarted := make(chan struct{})
		bindDone := make(chan struct{})
		go func() {
			close(bindStarted)
			client.SetRoom("new-room")
			_ = client.SendMessage(codec.MustNewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
				RoomCode: "new-room",
			}))
			close(bindDone)
		}()
		waitHandlerSignal(t, bindStarted, "replacement room binding attempt")

		close(client.release)
		waitHandlerSignal(t, handlerDone, "old-room terminal delivery")
		waitHandlerSignal(t, bindDone, "replacement room result")

		messages := client.sentMessages()
		require.Len(t, messages, 2)
		require.Equal(t, protocol.MsgRoomLeft, messages[0].Type)
		require.Equal(t, protocol.MsgRoomJoined, messages[1].Type)
		require.Equal(t, "new-room", client.GetRoom())
	})

	t.Run("terminal delivery is skipped after replacement binding", func(t *testing.T) {
		client := &conditionalRemovalClient{
			synchronizedClient: &synchronizedClient{id: "atomic-binding-first", name: "Player"},
		}
		client.SetRoom("new-room")
		removal := terminalRemoval("old-room")
		removal.Players = []room.PlayerSnapshot{{ID: client.GetID(), Client: client}}

		NewHandler(HandlerDeps{}).handleRoomRemoved(removal)

		require.Equal(t, 1, client.conditionalCalls())
		require.Empty(t, client.sentMessages())
		require.Equal(t, "new-room", client.GetRoom())
	})
}

func TestRoomRemovalLoopKeepsGameRegistryAndGoroutinesBounded(t *testing.T) {
	rm := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	h := NewHandler(HandlerDeps{RoomManager: rm})
	baseline := runtime.NumGoroutine()

	const iterations = 250
	for index := range iterations {
		clients := []*testutil.SimpleClient{
			testutil.NewSimpleClient(fmt.Sprintf("loop-%06d-p1", index), "Player 1"),
			testutil.NewSimpleClient(fmt.Sprintf("loop-%06d-p2", index), "Player 2"),
			testutil.NewSimpleClient(fmt.Sprintf("loop-%06d-p3", index), "Player 3"),
		}
		gameRoom, err := rm.CreateRoom(clients[0])
		require.NoError(t, err)
		_, err = rm.JoinRoom(clients[1], gameRoom.Code)
		require.NoError(t, err)
		_, err = rm.JoinRoom(clients[2], gameRoom.Code)
		require.NoError(t, err)

		game := session.NewGameSession(gameRoom, nil, config.GameConfig{BidTimeout: 3600, TurnTimeout: 3600})
		require.True(t, h.SetGameSession(gameRoom.Code, game))
		game.Start()
		settlement := &protocol.GameSettlementDTO{
			WinnerID:         clients[0].GetID(),
			WinnerName:       clients[0].GetName(),
			WinnerIsLandlord: true,
			Multiplier:       1,
			Scores: []protocol.PlayerScore{
				{PlayerID: clients[0].GetID(), PlayerName: clients[0].GetName(), IsLandlord: true, Score: 2},
				{PlayerID: clients[1].GetID(), PlayerName: clients[1].GetName(), Score: -1},
				{PlayerID: clients[2].GetID(), PlayerName: clients[2].GetName(), Score: -1},
			},
			PlayerHands: []protocol.PlayerHand{
				{PlayerID: clients[0].GetID(), PlayerName: clients[0].GetName()},
				{PlayerID: clients[1].GetID(), PlayerName: clients[1].GetName()},
				{PlayerID: clients[2].GetID(), PlayerName: clients[2].GetName()},
			},
		}
		game.EndWithSettlementForTest(settlement)
		require.Equal(t, settlement, game.BuildGameStateDTO(clients[0].GetID(), nil).Settlement)
		require.True(t, rm.RemoveRoom(gameRoom, room.RoomRemovalShutdown))
		require.Nil(t, h.GetGameSession(gameRoom.Code))
		require.True(t, game.IsRetiredForTest())
	}

	h.gamesMu.RLock()
	require.Empty(t, h.games)
	h.gamesMu.RUnlock()
	runtime.GC()
	require.LessOrEqual(t, runtime.NumGoroutine(), baseline+4)
}
