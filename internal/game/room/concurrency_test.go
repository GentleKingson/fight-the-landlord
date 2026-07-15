package room

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

type concurrencyClient struct {
	id   string
	name string

	mu       sync.RWMutex
	roomCode string
	sends    atomic.Int64

	blockSend atomic.Bool
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
}

func newConcurrencyClient(id string) *concurrencyClient {
	return &concurrencyClient{
		id:      id,
		name:    "Player " + id,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (c *concurrencyClient) GetID() string   { return c.id }
func (c *concurrencyClient) GetName() string { return c.name }
func (c *concurrencyClient) GetRoom() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.roomCode
}
func (c *concurrencyClient) SetRoom(code string) {
	c.mu.Lock()
	c.roomCode = code
	c.mu.Unlock()
}
func (c *concurrencyClient) SendMessage(*protocol.Message) error {
	c.sends.Add(1)
	if c.blockSend.Load() {
		c.enterOnce.Do(func() { close(c.entered) })
		<-c.release
	}
	return nil
}
func (c *concurrencyClient) Close()      {}
func (c *concurrencyClient) IsBot() bool { return false }

func newConcurrentMembershipRoom(t *testing.T) (*RoomManager, *Room, *concurrencyClient) {
	t.Helper()

	rm := &RoomManager{
		roomTimeout: time.Hour,
		gameConfig:  config.GameConfig{RoomTimeout: 10},
		rooms:       make(map[string]*Room),
	}
	creator := newConcurrencyClient("p1")
	observer := newConcurrencyClient("p2")
	toggled := newConcurrencyClient("p3")
	gameRoom, err := rm.CreateRoom(creator)
	require.NoError(t, err)
	_, err = rm.JoinRoom(observer, gameRoom.Code)
	require.NoError(t, err)
	return rm, gameRoom, toggled
}

func TestRoomBroadcastConcurrentWithLeaveRoom(t *testing.T) {
	rm, gameRoom, toggled := newConcurrentMembershipRoom(t)
	message := &protocol.Message{Type: protocol.MsgPlayerOnline}
	start := make(chan struct{})
	errs := make(chan error, 512)

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		for range 20_000 {
			gameRoom.Broadcast(message)
			runtime.Gosched()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range 512 {
			if _, err := rm.JoinRoom(toggled, gameRoom.Code); err != nil {
				errs <- err
				return
			}
			if !rm.LeaveRoom(toggled) {
				errs <- errors.New("LeaveRoom unexpectedly rejected a room member")
				return
			}
			runtime.Gosched()
		}
	}()

	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestRoomGetAllPlayersInfoConcurrentWithMemberDeletion(t *testing.T) {
	rm, gameRoom, toggled := newConcurrentMembershipRoom(t)
	start := make(chan struct{})
	errs := make(chan error, 512)

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		for range 20_000 {
			_ = gameRoom.GetAllPlayersInfo()
			runtime.Gosched()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range 512 {
			if _, err := rm.JoinRoom(toggled, gameRoom.Code); err != nil {
				errs <- err
				return
			}
			if !rm.LeaveRoom(toggled) {
				errs <- errors.New("LeaveRoom unexpectedly rejected a room member")
				return
			}
			runtime.Gosched()
		}
	}()

	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestRoomCleanupConcurrentWithReconnectHandlesOfflineMember(t *testing.T) {
	rm := &RoomManager{
		roomTimeout: time.Nanosecond,
		gameConfig:  config.GameConfig{RoomTimeout: 10},
		rooms:       make(map[string]*Room),
	}
	offline := newConcurrencyClient("p1")
	observer := newConcurrencyClient("p2")
	gameRoom, err := rm.CreateRoom(offline)
	require.NoError(t, err)
	_, err = rm.JoinRoom(observer, gameRoom.Code)
	require.NoError(t, err)
	rm.NotifyPlayerOffline(offline)

	gameRoom.mu.Lock()
	gameRoom.CreatedAt = time.Unix(0, 0)
	gameRoom.mu.Unlock()
	observer.blockSend.Store(true)

	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		rm.cleanup()
	}()
	<-observer.entered

	reconnectDone := make(chan error, 1)
	go func() {
		replacement := newConcurrencyClient("p1")
		reconnectDone <- rm.ReconnectPlayer("p1", gameRoom.Code, replacement)
	}()
	select {
	case err = <-reconnectDone:
		require.ErrorIs(t, err, apperrors.ErrRoomNotFound)
	case <-time.After(time.Second):
		t.Fatal("reconnect blocked behind cleanup client delivery")
	}
	close(observer.release)

	<-cleanupDone
}

func TestStaleDisconnectDoesNotDetachOrAnnounceReplacement(t *testing.T) {
	rm, gameRoom, _ := newConcurrentMembershipRoom(t)
	original, ok := gameRoom.PrivateRecipient("p1")
	require.True(t, ok)
	replacement := newConcurrencyClient("p1")
	require.NoError(t, rm.ReconnectPlayer("p1", gameRoom.Code, replacement))
	before := replacement.sends.Load()

	rm.NotifyPlayerOffline(original)

	recipient, ok := gameRoom.PrivateRecipient("p1")
	require.True(t, ok)
	require.Same(t, replacement, recipient)
	require.Equal(t, before, replacement.sends.Load(), "stale disconnect announced the active replacement as offline")
}

func TestStaleConnectionCannotRemoveReplacement(t *testing.T) {
	rm, gameRoom, _ := newConcurrentMembershipRoom(t)
	original, ok := gameRoom.PrivateRecipient("p1")
	require.True(t, ok)
	replacement := newConcurrencyClient("p1")
	require.NoError(t, rm.ReconnectPlayer("p1", gameRoom.Code, replacement))

	require.False(t, rm.LeaveRoom(original), "stale connection must not mutate shared room identity")
	recipient, ok := gameRoom.PrivateRecipient("p1")
	require.True(t, ok)
	require.Same(t, replacement, recipient)
	require.Equal(t, gameRoom.Code, original.GetRoom())
}

func TestStaleConnectionCannotChangeReplacementReadyState(t *testing.T) {
	rm, gameRoom, _ := newConcurrentMembershipRoom(t)
	original, ok := gameRoom.PrivateRecipient("p1")
	require.True(t, ok)
	replacement := newConcurrencyClient("p1")
	require.NoError(t, rm.ReconnectPlayer("p1", gameRoom.Code, replacement))

	require.ErrorIs(t, rm.SetPlayerReady(original, true), apperrors.ErrNotInRoom)
	info, ok := gameRoom.GetPlayerInfo("p1")
	require.True(t, ok)
	require.False(t, info.Ready)
}

func TestReadyStateCannotChangeAfterGameTransition(t *testing.T) {
	rm, gameRoom, third := newConcurrentMembershipRoom(t)
	_, err := rm.JoinRoom(third, gameRoom.Code)
	require.NoError(t, err)
	players := gameRoom.SnapshotPlayers()
	require.Len(t, players, 3)

	for _, player := range players {
		require.NoError(t, rm.SetPlayerReady(player.Client, true))
	}
	require.Equal(t, RoomStateReady, gameRoom.State())
	require.ErrorIs(t, rm.SetPlayerReady(players[0].Client, false), apperrors.ErrGameStarted)
	info, ok := gameRoom.GetPlayerInfo(players[0].ID)
	require.True(t, ok)
	require.True(t, info.Ready)
}

func TestLeaveRejectedAfterGameTransitionPreservesMembership(t *testing.T) {
	rm, gameRoom, third := newConcurrentMembershipRoom(t)
	_, err := rm.JoinRoom(third, gameRoom.Code)
	require.NoError(t, err)
	players := gameRoom.SnapshotPlayers()
	for _, player := range players {
		require.NoError(t, rm.SetPlayerReady(player.Client, true))
	}

	require.False(t, rm.LeaveRoom(third))
	require.Equal(t, gameRoom.Code, third.GetRoom())
	_, ok := gameRoom.GetPlayerInfo(third.GetID())
	require.True(t, ok)
}

func TestJoinRoomReusesLowestAvailableSeat(t *testing.T) {
	rm, gameRoom, third := newConcurrentMembershipRoom(t)
	_, err := rm.JoinRoom(third, gameRoom.Code)
	require.NoError(t, err)
	second, ok := gameRoom.PrivateRecipient("p2")
	require.True(t, ok)
	require.True(t, rm.LeaveRoom(second))

	replacement := newConcurrencyClient("p4")
	_, err = rm.JoinRoom(replacement, gameRoom.Code)
	require.NoError(t, err)
	players := gameRoom.SnapshotPlayers()
	require.Equal(t, []string{"p1", "p4", "p3"}, []string{players[0].ID, players[1].ID, players[2].ID})
	require.Equal(t, []int{0, 1, 2}, []int{players[0].Seat, players[1].Seat, players[2].Seat})
}

func TestSetPlayerReadyDoesNotHoldRoomLockDuringDelivery(t *testing.T) {
	rm, gameRoom, third := newConcurrentMembershipRoom(t)
	_, err := rm.JoinRoom(third, gameRoom.Code)
	require.NoError(t, err)
	players := gameRoom.SnapshotPlayers()
	require.Len(t, players, 3)
	first := players[0].Client.(*concurrencyClient)
	blocking := players[1].Client.(*concurrencyClient)
	require.NoError(t, rm.SetPlayerReady(first, true))
	require.NoError(t, rm.SetPlayerReady(blocking, true))

	blocking.blockSend.Store(true)
	readyDone := make(chan error, 1)
	go func() {
		readyDone <- rm.SetPlayerReady(third, true)
	}()
	<-blocking.entered

	stateDone := make(chan RoomState, 1)
	go func() { stateDone <- gameRoom.State() }()
	select {
	case state := <-stateDone:
		require.Equal(t, RoomStateReady, state)
	case <-time.After(time.Second):
		t.Fatal("room state read blocked behind client delivery")
	}

	close(blocking.release)
	require.NoError(t, <-readyDone)
}

func TestGameStartCallbackRunsWithoutRoomLock(t *testing.T) {
	rm, gameRoom, third := newConcurrentMembershipRoom(t)
	_, err := rm.JoinRoom(third, gameRoom.Code)
	require.NoError(t, err)
	players := gameRoom.SnapshotPlayers()
	require.Len(t, players, 3)

	callbackDone := make(chan struct{})
	rm.SetOnGameStart(func(startingRoom *Room, _ []PlayerSnapshot) {
		_ = startingRoom.State()
		close(callbackDone)
	})
	require.NoError(t, rm.SetPlayerReady(players[0].Client, true))
	require.NoError(t, rm.SetPlayerReady(players[1].Client, true))

	readyDone := make(chan error, 1)
	go func() { readyDone <- rm.SetPlayerReady(third, true) }()
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("game-start callback attempted to re-enter a locked room")
	}
	require.NoError(t, <-readyDone)
}

func TestGameStartCallbackReceivesCommittedMembershipSnapshot(t *testing.T) {
	rm, gameRoom, third := newConcurrentMembershipRoom(t)
	_, err := rm.JoinRoom(third, gameRoom.Code)
	require.NoError(t, err)
	players := gameRoom.SnapshotPlayers()
	require.Len(t, players, 3)

	callbackEntered := make(chan struct{})
	callbackRelease := make(chan struct{})
	callbackDone := make(chan []PlayerSnapshot, 1)
	rm.SetOnGameStart(func(_ *Room, startingPlayers []PlayerSnapshot) {
		close(callbackEntered)
		<-callbackRelease
		callbackDone <- startingPlayers
	})
	require.NoError(t, rm.SetPlayerReady(players[0].Client, true))
	require.NoError(t, rm.SetPlayerReady(players[1].Client, true))

	readyDone := make(chan error, 1)
	go func() { readyDone <- rm.SetPlayerReady(third, true) }()
	waitForRoomSignal(t, callbackEntered, "game-start callback was not invoked")
	require.False(t, rm.LeaveRoom(third))
	close(callbackRelease)
	require.NoError(t, <-readyDone)
	committedPlayers := <-callbackDone
	require.Len(t, committedPlayers, 3)
	require.Equal(t, []string{"p1", "p2", "p3"}, []string{
		committedPlayers[0].ID,
		committedPlayers[1].ID,
		committedPlayers[2].ID,
	})
}

func waitForRoomSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}

type publicationBarrierClient struct {
	id   string
	name string

	mu        sync.Mutex
	roomCode  string
	messages  []*protocol.Message
	blockType protocol.MessageType
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
}

func newPublicationBarrierClient(id string) *publicationBarrierClient {
	return &publicationBarrierClient{id: id, name: "Player " + id}
}

func (c *publicationBarrierClient) GetID() string   { return c.id }
func (c *publicationBarrierClient) GetName() string { return c.name }
func (c *publicationBarrierClient) GetRoom() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.roomCode
}
func (c *publicationBarrierClient) SetRoom(code string) {
	c.mu.Lock()
	c.roomCode = code
	c.mu.Unlock()
}
func (c *publicationBarrierClient) SendMessage(message *protocol.Message) error {
	_, err := c.SendMessageIfIdentity(c.id, c.GetRoom(), message)
	return err
}
func (c *publicationBarrierClient) SendMessageIfIdentity(expectedPlayerID, expectedRoom string, message *protocol.Message) (bool, error) {
	c.mu.Lock()
	if c.id != expectedPlayerID || c.roomCode != expectedRoom {
		c.mu.Unlock()
		return false, nil
	}
	c.messages = append(c.messages, message)
	shouldBlock := c.blockType == message.Type
	entered, release := c.entered, c.release
	c.mu.Unlock()
	if shouldBlock {
		c.enterOnce.Do(func() { close(entered) })
		<-release
	}
	return true, nil
}
func (c *publicationBarrierClient) Close()      {}
func (c *publicationBarrierClient) IsBot() bool { return false }

func (c *publicationBarrierClient) resetMessagesAndBlock(messageType protocol.MessageType) (entered <-chan struct{}, release chan<- struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = nil
	c.blockType = messageType
	c.entered = make(chan struct{})
	c.release = make(chan struct{})
	c.enterOnce = sync.Once{}
	return c.entered, c.release
}

func (c *publicationBarrierClient) messageTypes() []protocol.MessageType {
	c.mu.Lock()
	defer c.mu.Unlock()
	types := make([]protocol.MessageType, len(c.messages))
	for index, message := range c.messages {
		types[index] = message.Type
	}
	return types
}

func TestCreateResponsePrecedesFirstPlayerJoined(t *testing.T) {
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	creator := newPublicationBarrierClient("creator")
	entered, release := creator.resetMessagesAndBlock(protocol.MsgRoomCreated)
	created := make(chan *Room, 1)
	createErr := make(chan error, 1)
	go func() {
		gameRoom, err := rm.CreateRoomWithResponse(creator)
		created <- gameRoom
		createErr <- err
	}()
	waitForRoomSignal(t, entered, "RoomCreated did not reach publication barrier")

	gameRoom := rm.GetRoom(creator.GetRoom())
	require.NotNil(t, gameRoom)
	joiner := newPublicationBarrierClient("joiner")
	joinDone := make(chan error, 1)
	go func() {
		_, err := rm.JoinRoomWithResponse(joiner, gameRoom.Code)
		joinDone <- err
	}()
	require.Len(t, gameRoom.SnapshotPlayers(), 1)

	close(release)
	require.NoError(t, <-createErr)
	require.Same(t, gameRoom, <-created)
	require.NoError(t, <-joinDone)
	require.Equal(t, []protocol.MessageType{
		protocol.MsgRoomCreated,
		protocol.MsgPlayerJoined,
	}, creator.messageTypes())
}

func TestJoinResponsePrecedesLaterRoomMutation(t *testing.T) {
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	host := newPublicationBarrierClient("host")
	gameRoom, err := rm.CreateRoom(host)
	require.NoError(t, err)
	joining := newPublicationBarrierClient("joining")
	entered, release := joining.resetMessagesAndBlock(protocol.MsgRoomJoined)
	joinDone := make(chan error, 1)
	go func() {
		_, joinErr := rm.JoinRoomWithResponse(joining, gameRoom.Code)
		joinDone <- joinErr
	}()
	waitForRoomSignal(t, entered, "RoomJoined did not reach publication barrier")

	third := newPublicationBarrierClient("third")
	thirdDone := make(chan error, 1)
	go func() {
		_, joinErr := rm.JoinRoomWithResponse(third, gameRoom.Code)
		thirdDone <- joinErr
	}()
	require.Len(t, gameRoom.SnapshotPlayers(), 2)

	close(release)
	require.NoError(t, <-joinDone)
	require.NoError(t, <-thirdDone)
	require.Equal(t, []protocol.MessageType{
		protocol.MsgRoomJoined,
		protocol.MsgPlayerJoined,
	}, joining.messageTypes())
}

func TestOfflineReconnectPublishesInCommitOrder(t *testing.T) {
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	host := newPublicationBarrierClient("host")
	observer := newPublicationBarrierClient("observer")
	disconnected := newPublicationBarrierClient("reconnecting")
	gameRoom, err := rm.CreateRoom(host)
	require.NoError(t, err)
	_, err = rm.JoinRoom(observer, gameRoom.Code)
	require.NoError(t, err)
	_, err = rm.JoinRoom(disconnected, gameRoom.Code)
	require.NoError(t, err)
	entered, release := observer.resetMessagesAndBlock(protocol.MsgPlayerOffline)

	offlineDone := make(chan struct{})
	go func() {
		rm.NotifyPlayerOffline(disconnected)
		close(offlineDone)
	}()
	waitForRoomSignal(t, entered, "PlayerOffline did not reach publication barrier")
	replacement := newPublicationBarrierClient(disconnected.GetID())
	reconnectDone := make(chan error, 1)
	go func() {
		reconnectDone <- rm.ReconnectPlayer(disconnected.GetID(), gameRoom.Code, replacement)
	}()

	close(release)
	waitForRoomSignal(t, offlineDone, "offline publication did not complete")
	require.NoError(t, <-reconnectDone)
	require.Equal(t, []protocol.MessageType{
		protocol.MsgPlayerOffline,
		protocol.MsgPlayerOnline,
	}, observer.messageTypes())
	recipient, online := gameRoom.PrivateRecipient(disconnected.GetID())
	require.True(t, online)
	require.Same(t, replacement, recipient)

	before := observer.messageTypes()
	rm.NotifyPlayerOffline(disconnected)
	require.Equal(t, before, observer.messageTypes(), "stale disconnect emitted PlayerOffline after replacement won")
}
