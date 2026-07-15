package session

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

type deliveryBarrierClient struct {
	id   string
	name string

	mu        sync.RWMutex
	roomCode  string
	messages  []protocol.MessageType
	blockType protocol.MessageType
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
}

func newDeliveryBarrierClient(id string) *deliveryBarrierClient {
	return &deliveryBarrierClient{
		id:      id,
		name:    "Player " + id,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (c *deliveryBarrierClient) GetID() string   { return c.id }
func (c *deliveryBarrierClient) GetName() string { return c.name }
func (c *deliveryBarrierClient) GetRoom() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.roomCode
}
func (c *deliveryBarrierClient) SetRoom(code string) {
	c.mu.Lock()
	c.roomCode = code
	c.mu.Unlock()
}
func (c *deliveryBarrierClient) SendMessage(message *protocol.Message) error {
	c.mu.Lock()
	c.messages = append(c.messages, message.Type)
	shouldBlock := c.blockType == message.Type
	c.mu.Unlock()
	if shouldBlock {
		c.enterOnce.Do(func() { close(c.entered) })
		<-c.release
	}
	return nil
}
func (c *deliveryBarrierClient) SendMessageIfIdentity(expectedPlayerID, expectedRoom string, message *protocol.Message) (bool, error) {
	c.mu.Lock()
	if c.id != expectedPlayerID || c.roomCode != expectedRoom {
		c.mu.Unlock()
		return false, nil
	}
	c.messages = append(c.messages, message.Type)
	shouldBlock := c.blockType == message.Type
	c.mu.Unlock()
	if shouldBlock {
		c.enterOnce.Do(func() { close(c.entered) })
		<-c.release
	}
	return true, nil
}
func (c *deliveryBarrierClient) Close()      {}
func (c *deliveryBarrierClient) IsBot() bool { return false }

func (c *deliveryBarrierClient) rebindIdentityForTest(playerID string) {
	c.mu.Lock()
	c.id = playerID
	c.name = "Player " + playerID
	c.mu.Unlock()
}

func (c *deliveryBarrierClient) blockOn(messageType protocol.MessageType) {
	c.mu.Lock()
	c.blockType = messageType
	c.mu.Unlock()
}

func (c *deliveryBarrierClient) messageCount(messageType protocol.MessageType) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	count := 0
	for _, current := range c.messages {
		if current == messageType {
			count++
		}
	}
	return count
}

func newBarrierDeliveryRoom(t *testing.T, gameConfig config.GameConfig) (*room.RoomManager, *room.Room, []*deliveryBarrierClient) {
	t.Helper()
	manager := room.NewRoomManager(nil, gameConfig)
	clients := []*deliveryBarrierClient{
		newDeliveryBarrierClient("p1"),
		newDeliveryBarrierClient("p2"),
		newDeliveryBarrierClient("p3"),
	}
	gameRoom, err := manager.CreateRoom(clients[0])
	require.NoError(t, err)
	_, err = manager.JoinRoom(clients[1], gameRoom.Code)
	require.NoError(t, err)
	_, err = manager.JoinRoom(clients[2], gameRoom.Code)
	require.NoError(t, err)
	return manager, gameRoom, clients
}

func waitForSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}

func newOfflineDeliveryRoom(t *testing.T, gameConfig config.GameConfig) (*room.RoomManager, *room.Room, []*testutil.SimpleClient) {
	t.Helper()

	manager := room.NewRoomManager(nil, gameConfig)
	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient("p1", "Player1"),
		testutil.NewSimpleClient("p2", "Player2"),
		testutil.NewSimpleClient("p3", "Player3"),
	}
	gameRoom, err := manager.CreateRoom(clients[0])
	require.NoError(t, err)
	_, err = manager.JoinRoom(clients[1], gameRoom.Code)
	require.NoError(t, err)
	_, err = manager.JoinRoom(clients[2], gameRoom.Code)
	require.NoError(t, err)
	return manager, gameRoom, clients
}

func TestStartGameSkipsPrivateDealForOfflinePlayer(t *testing.T) {
	gameConfig := config.GameConfig{
		TurnTimeout: 30,
		BidTimeout:  15,
		RoomTimeout: 10,
	}
	manager, gameRoom, clients := newOfflineDeliveryRoom(t, gameConfig)
	manager.NotifyPlayerOffline(clients[1])

	gameSession := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), gameConfig)
	t.Cleanup(gameSession.StopAllTimers)

	require.NotPanics(t, gameSession.Start)
}

func TestPrivateDeliveryRejectsSameRoomPhysicalClientReboundToAnotherPlayer(t *testing.T) {
	manager, gameRoom, clients := newBarrierDeliveryRoom(t, config.GameConfig{RoomTimeout: 60})
	gameSession := NewGameSession(gameRoom, nil, config.GameConfig{})
	gameSession.SetRoomManager(manager)
	initialDeals := clients[0].messageCount(protocol.MsgDealCards)

	clients[0].rebindIdentityForTest("replacement-player")
	gameSession.dispatchPendingWork(pendingWork{deliveries: []pendingDelivery{{
		playerID: "p1",
		message:  &protocol.Message{Type: protocol.MsgDealCards},
	}}})

	require.Equal(t, initialDeals, clients[0].messageCount(protocol.MsgDealCards))
}

func TestDelayedGameStartRebuildsRosterAtPublication(t *testing.T) {
	gameConfig := config.GameConfig{TurnTimeout: 3600, BidTimeout: 3600, RoomTimeout: 60}
	manager, gameRoom, clients := newOfflineDeliveryRoom(t, gameConfig)
	gameSession := NewGameSession(gameRoom, nil, gameConfig)
	gameSession.SetRoomManager(manager)
	t.Cleanup(gameSession.StopAllTimers)

	gameSession.mu.Lock()
	gameSession.startBiddingRound()
	work := gameSession.takePendingWorkLocked()
	gameSession.mu.Unlock()
	manager.NotifyPlayerOffline(clients[1])
	gameSession.dispatchPendingWork(work)

	var gameStart *protocol.Message
	for _, message := range clients[0].SentMessages() {
		if message.Type == protocol.MsgGameStart {
			gameStart = message
		}
	}
	require.NotNil(t, gameStart)
	payload, err := codec.ParsePayload[protocol.GameStartPayload](gameStart)
	require.NoError(t, err)
	foundOffline := false
	for _, player := range payload.Players {
		if player.ID == clients[1].GetID() {
			foundOffline = true
			require.False(t, player.Online)
		}
	}
	require.True(t, foundOffline)
	for _, message := range clients[1].SentMessages() {
		require.NotEqual(t, protocol.MsgDealCards, message.Type)
	}
}

func TestLandlordPrivateHandUpdateSkipsOfflinePlayer(t *testing.T) {
	gameConfig := config.GameConfig{
		TurnTimeout: 30,
		BidTimeout:  15,
		RoomTimeout: 10,
	}
	manager, gameRoom, clients := newOfflineDeliveryRoom(t, gameConfig)
	gameSession := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), gameConfig)
	t.Cleanup(gameSession.StopAllTimers)
	gameSession.Start()

	manager.NotifyPlayerOffline(clients[0])
	require.NotPanics(t, func() {
		gameSession.mu.Lock()
		defer gameSession.mu.Unlock()
		gameSession.setLandlord(0)
	})
}

func TestCallerDisconnectBeforeLandlordDecisionSkipsPrivateHand(t *testing.T) {
	gameConfig := config.GameConfig{TurnTimeout: 30, BidTimeout: 15, RoomTimeout: 10, OfflineWaitTimeout: 30}
	manager, gameRoom, clients := newBarrierDeliveryRoom(t, gameConfig)
	gameSession := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), gameConfig)
	t.Cleanup(gameSession.StopAllTimers)
	gameSession.Start()

	callerIndex := gameSession.currentBidder
	caller := gameSession.players[callerIndex]
	initialDeals := clients[callerIndex].messageCount(protocol.MsgDealCards)
	require.NoError(t, gameSession.HandleBid(caller.ID, true))
	manager.NotifyPlayerOffline(clients[callerIndex])
	for range 2 {
		grabber := gameSession.players[gameSession.currentBidder]
		require.NoError(t, gameSession.HandleBid(grabber.ID, false))
	}

	require.Equal(t, GameStatePlaying, gameSession.GetStateForSerialization())
	require.True(t, caller.IsLandlord)
	require.Equal(t, initialDeals, clients[callerIndex].messageCount(protocol.MsgDealCards))
}

func TestPlayerDisconnectDuringDealSerializesPublicationWithoutBlockingState(t *testing.T) {
	gameConfig := config.GameConfig{TurnTimeout: 30, BidTimeout: 15, RoomTimeout: 10, OfflineWaitTimeout: 30}
	manager, gameRoom, clients := newBarrierDeliveryRoom(t, gameConfig)
	clients[0].blockOn(protocol.MsgGameStart)
	gameSession := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), gameConfig)
	t.Cleanup(gameSession.StopAllTimers)

	startDone := make(chan struct{})
	go func() {
		gameSession.Start()
		close(startDone)
	}()
	waitForSignal(t, clients[0].entered, "game start delivery did not reach the barrier")

	offlineDone := make(chan struct{})
	go func() {
		manager.NotifyPlayerOffline(clients[1])
		close(offlineDone)
	}()
	stateRead := make(chan struct{})
	go func() {
		_ = gameSession.GetPlayerCardsCount("p1")
		close(stateRead)
	}()
	waitForSignal(t, stateRead, "game session lock was held during client delivery")

	close(clients[0].release)
	waitForSignal(t, startDone, "game start did not complete after releasing delivery")
	waitForSignal(t, offlineDone, "offline publication did not complete after game start")

	dealsAfterOffline := clients[1].messageCount(protocol.MsgDealCards)
	gameSession.dispatchPendingWork(pendingWork{deliveries: []pendingDelivery{{
		playerID: "p2",
		message:  &protocol.Message{Type: protocol.MsgDealCards},
	}}})
	require.Equal(t, dealsAfterOffline, clients[1].messageCount(protocol.MsgDealCards))
}

func TestLandlordDisconnectDuringAnnouncementSkipsPrivateHandUpdate(t *testing.T) {
	gameConfig := config.GameConfig{TurnTimeout: 30, BidTimeout: 15, RoomTimeout: 10, OfflineWaitTimeout: 30}
	manager, gameRoom, clients := newBarrierDeliveryRoom(t, gameConfig)
	gameSession := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), gameConfig)
	t.Cleanup(gameSession.StopAllTimers)
	gameSession.Start()
	initialDeals := clients[0].messageCount(protocol.MsgDealCards)
	clients[1].blockOn(protocol.MsgLandlord)

	gameSession.mu.Lock()
	gameSession.setLandlord(0)
	work := gameSession.takePendingWorkLocked()
	gameSession.mu.Unlock()
	deliveryDone := make(chan struct{})
	go func() {
		gameSession.dispatchPendingWork(work)
		close(deliveryDone)
	}()
	waitForSignal(t, clients[1].entered, "landlord announcement did not reach the barrier")

	offlineDone := make(chan struct{})
	go func() {
		manager.NotifyPlayerOffline(clients[0])
		close(offlineDone)
	}()
	close(clients[1].release)
	waitForSignal(t, deliveryDone, "landlord delivery did not complete")
	waitForSignal(t, offlineDone, "landlord offline publication did not complete")
	dealsAfterOffline := clients[0].messageCount(protocol.MsgDealCards)
	require.GreaterOrEqual(t, dealsAfterOffline, initialDeals)
	gameSession.dispatchPendingWork(pendingWork{deliveries: []pendingDelivery{{
		playerID: "p1",
		message:  &protocol.Message{Type: protocol.MsgDealCards},
	}}})
	require.Equal(t, dealsAfterOffline, clients[0].messageCount(protocol.MsgDealCards))
}

func TestRedealSkipsOfflinePlayerPrivateHand(t *testing.T) {
	gameConfig := config.GameConfig{TurnTimeout: 30, BidTimeout: 15, RoomTimeout: 10, OfflineWaitTimeout: 30}
	manager, gameRoom, clients := newBarrierDeliveryRoom(t, gameConfig)
	gameSession := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), gameConfig)
	t.Cleanup(gameSession.StopAllTimers)
	gameSession.Start()
	initialDeals := clients[1].messageCount(protocol.MsgDealCards)
	manager.NotifyPlayerOffline(clients[1])

	for range 3 {
		gameSession.mu.RLock()
		bidderID := gameSession.players[gameSession.currentBidder].ID
		gameSession.mu.RUnlock()
		require.NoError(t, gameSession.HandleBid(bidderID, false))
	}
	require.Equal(t, initialDeals, clients[1].messageCount(protocol.MsgDealCards))
}

func TestGameOverDeliveryConcurrentWithReconnect(t *testing.T) {
	gameConfig := config.GameConfig{TurnTimeout: 30, BidTimeout: 15, RoomTimeout: 10, OfflineWaitTimeout: 30}
	manager, gameRoom, clients := newBarrierDeliveryRoom(t, gameConfig)
	gameSession := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), gameConfig)
	t.Cleanup(gameSession.StopAllTimers)
	gameSession.Start()
	clients[1].blockOn(protocol.MsgGameOver)

	gameSession.mu.Lock()
	gameSession.endGame(gameSession.players[0])
	work := gameSession.takePendingWorkLocked()
	gameSession.mu.Unlock()
	deliveryDone := make(chan struct{})
	go func() {
		gameSession.dispatchPendingWork(work)
		close(deliveryDone)
	}()
	waitForSignal(t, clients[1].entered, "game-over delivery did not reach the barrier")

	replacement := newDeliveryBarrierClient("p1")
	reconnectDone := make(chan error, 1)
	go func() {
		reconnectDone <- manager.ReconnectPlayer("p1", gameRoom.Code, replacement)
	}()
	stateRead := make(chan struct{})
	go func() {
		_ = gameSession.BuildGameStateDTO("p1", nil)
		close(stateRead)
	}()
	waitForSignal(t, stateRead, "game state snapshot blocked during game-over delivery")

	close(clients[1].release)
	waitForSignal(t, deliveryDone, "game-over delivery did not complete")
	select {
	case err := <-reconnectDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("reconnect did not complete after game-over publication")
	}
	recipient, ok := gameRoom.PrivateRecipient("p1")
	require.True(t, ok)
	require.Same(t, replacement, recipient)
}

func TestGameOverKeepsRoomEndedUntilDeliveryCompletes(t *testing.T) {
	gameConfig := config.GameConfig{TurnTimeout: 30, BidTimeout: 15, RoomTimeout: 10, OfflineWaitTimeout: 30}
	_, gameRoom, clients := newBarrierDeliveryRoom(t, gameConfig)
	gameSession := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), gameConfig)
	t.Cleanup(gameSession.StopAllTimers)
	gameSession.Start()
	clients[1].blockOn(protocol.MsgGameOver)

	gameSession.mu.Lock()
	gameSession.endGame(gameSession.players[0])
	work := gameSession.takePendingWorkLocked()
	gameSession.mu.Unlock()
	deliveryDone := make(chan struct{})
	go func() {
		gameSession.dispatchPendingWork(work)
		close(deliveryDone)
	}()
	waitForSignal(t, clients[1].entered, "game-over delivery did not reach the barrier")
	require.Equal(t, room.RoomStateEnded, gameRoom.State())

	close(clients[1].release)
	waitForSignal(t, deliveryDone, "game-over delivery did not complete")
	require.Equal(t, room.RoomStateWaiting, gameRoom.State())
}
