package handler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

type synchronizedClient struct {
	mu       sync.RWMutex
	id       string
	name     string
	roomCode string
	messages []*protocol.Message
}

func (c *synchronizedClient) GetID() string   { return c.id }
func (c *synchronizedClient) GetName() string { return c.name }
func (c *synchronizedClient) GetRoom() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.roomCode
}
func (c *synchronizedClient) SetRoom(code string) {
	c.mu.Lock()
	c.roomCode = code
	c.mu.Unlock()
}
func (c *synchronizedClient) SendMessage(message *protocol.Message) error {
	c.mu.Lock()
	c.messages = append(c.messages, message)
	c.mu.Unlock()
	return nil
}
func (c *synchronizedClient) Close()      {}
func (c *synchronizedClient) IsBot() bool { return false }
func (c *synchronizedClient) sentMessages() []*protocol.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]*protocol.Message(nil), c.messages...)
}

func TestHandler_QuickMatchCanBeCancelled(t *testing.T) {
	server := new(testutil.MockServer)
	server.On("IsMaintenanceMode").Return(false)
	matcher := match.NewMatcher(match.MatcherDeps{})
	client := testutil.NewSimpleClient("p1", "Player1")
	h := NewHandler(HandlerDeps{Server: server, Matcher: matcher})

	started := time.Now()
	h.handleQuickMatch(client)
	require.Len(t, client.Messages, 1)
	assert.Equal(t, protocol.MsgMatchQueued, client.Messages[0].Type)
	queued, err := codec.ParsePayload[protocol.MatchQueuedPayload](client.Messages[0])
	require.NoError(t, err)
	assert.False(t, queued.Practice)
	assert.Greater(t, queued.DeadlineMS, started.UnixMilli())
	assert.Equal(t, 1, matcher.GetQueueLength())

	h.handleCancelMatch(client)
	require.Len(t, client.Messages, 2)
	assert.Equal(t, protocol.MsgMatchCancelled, client.Messages[1].Type)
	cancelled, err := codec.ParsePayload[protocol.MatchCancelledPayload](client.Messages[1])
	require.NoError(t, err)
	assert.Equal(t, "cancelled", cancelled.Reason)
	assert.Equal(t, 0, matcher.GetQueueLength())
}

func TestHandler_CancelMatchNotQueuedIsCorrelated(t *testing.T) {
	matcher := match.NewMatcher(match.MatcherDeps{})
	client := testutil.NewSimpleClient("p1", "Player1")
	h := NewHandler(HandlerDeps{Matcher: matcher})

	h.handleCancelMatch(client)

	require.Len(t, client.Messages, 1)
	assert.Equal(t, protocol.MsgError, client.Messages[0].Type)
	payload, err := codec.ParsePayload[protocol.ErrorPayload](client.Messages[0])
	require.NoError(t, err)
	assert.Equal(t, protocol.ErrCodeMatchNotQueued, payload.Code)
	assert.Equal(t, protocol.MsgCancelMatch, payload.CommandType)
}

type handlerBlockingAssembly struct {
	gameRoom    *room.Room
	joinStarted chan struct{}
	rollback    chan struct{}
	joinOnce    sync.Once
	rollOnce    sync.Once
}

func (a *handlerBlockingAssembly) Room() *room.Room { return a.gameRoom }
func (a *handlerBlockingAssembly) Join(ctx context.Context, _ types.ClientInterface) error {
	a.joinOnce.Do(func() { close(a.joinStarted) })
	<-ctx.Done()
	return ctx.Err()
}
func (a *handlerBlockingAssembly) Commit(context.Context) error { return nil }
func (a *handlerBlockingAssembly) Rollback() error {
	a.rollOnce.Do(func() { close(a.rollback) })
	return nil
}

func TestHandler_CancelMatchCancelsInflightPracticeTransaction(t *testing.T) {
	server := new(testutil.MockServer)
	server.On("IsMaintenanceMode").Return(false)
	rm := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	assembly := &handlerBlockingAssembly{
		joinStarted: make(chan struct{}),
		rollback:    make(chan struct{}),
	}
	matcher := match.NewMatcher(match.MatcherDeps{
		RoomManager:  rm,
		QueueTimeout: time.Hour,
		BeginRoom: func(_ context.Context, first types.ClientInterface) (match.RoomAssembly, error) {
			assembly.gameRoom = room.NewMockRoom("practice", first)
			return assembly, nil
		},
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })
	client := &synchronizedClient{id: "p1", name: "Player1"}
	h := NewHandler(HandlerDeps{Server: server, RoomManager: rm, Matcher: matcher})

	h.handlePracticeMatch(client)
	select {
	case <-assembly.joinStarted:
	case <-time.After(time.Second):
		t.Fatal("practice transaction did not reach inflight Join")
	}
	h.handleCancelMatch(client)
	select {
	case <-assembly.rollback:
	case <-time.After(time.Second):
		t.Fatal("practice transaction was not rolled back")
	}

	messages := client.sentMessages()
	require.NotEmpty(t, messages)
	assert.Equal(t, protocol.MsgMatchQueued, messages[0].Type)
	var cancellation *protocol.MatchCancelledPayload
	for _, message := range messages {
		if message.Type != protocol.MsgMatchCancelled {
			continue
		}
		payload, err := codec.ParsePayload[protocol.MatchCancelledPayload](message)
		require.NoError(t, err)
		cancellation = payload
		break
	}
	require.NotNil(t, cancellation)
	assert.Equal(t, "cancelled", cancellation.Reason)
}

func TestHandler_QuickMatchDuplicateIsRejected(t *testing.T) {
	server := new(testutil.MockServer)
	server.On("IsMaintenanceMode").Return(false)
	matcher := match.NewMatcher(match.MatcherDeps{})
	client := testutil.NewSimpleClient("p1", "Player1")
	h := NewHandler(HandlerDeps{Server: server, Matcher: matcher})

	h.handleQuickMatch(client)
	h.handleQuickMatch(client)

	require.Len(t, client.Messages, 2)
	assert.Equal(t, protocol.MsgMatchQueued, client.Messages[0].Type)
	errorPayload, err := codec.ParsePayload[protocol.ErrorPayload](client.Messages[1])
	require.NoError(t, err)
	assert.Equal(t, protocol.MsgQuickMatch, errorPayload.CommandType)
	assert.Equal(t, 1, matcher.GetQueueLength())
}

func TestHandler_LeaveRoomAcknowledgesCallerAndNotifiesPeer(t *testing.T) {
	rm := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	host := testutil.NewSimpleClient("p1", "Player1")
	peer := testutil.NewSimpleClient("p2", "Player2")
	created, err := rm.CreateRoom(host)
	require.NoError(t, err)
	_, err = rm.JoinRoom(peer, created.Code)
	require.NoError(t, err)
	host.Messages = nil
	peer.Messages = nil

	h := NewHandler(HandlerDeps{RoomManager: rm})
	h.handleLeaveRoom(host)

	assert.Empty(t, host.GetRoom())
	require.Len(t, host.Messages, 1)
	assert.Equal(t, protocol.MsgRoomLeft, host.Messages[0].Type)
	left, err := codec.ParsePayload[protocol.RoomLeftPayload](host.Messages[0])
	require.NoError(t, err)
	assert.Equal(t, created.Code, left.RoomCode)

	require.Len(t, peer.Messages, 1)
	assert.Equal(t, protocol.MsgPlayerLeft, peer.Messages[0].Type)
	peerNotice, err := codec.ParsePayload[protocol.PlayerLeftPayload](peer.Messages[0])
	require.NoError(t, err)
	assert.Equal(t, host.ID, peerNotice.PlayerID)
	_, exists := created.PlayerForTest(host.ID)
	assert.False(t, exists)
}
