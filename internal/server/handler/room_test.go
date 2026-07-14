package handler

import (
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
func (c *synchronizedClient) SendMessage(message *protocol.Message) {
	c.mu.Lock()
	c.messages = append(c.messages, message)
	c.mu.Unlock()
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

func TestHandler_CancelMatchAfterPracticeAcceptanceIsTooLate(t *testing.T) {
	server := new(testutil.MockServer)
	server.On("IsMaintenanceMode").Return(false)
	rm := room.NewRoomManager(nil, config.GameConfig{
		TurnTimeout: 3600,
		BidTimeout:  3600,
		RoomTimeout: 10,
	})
	matcher := match.NewMatcher(match.MatcherDeps{
		RoomManager: rm,
		GameConfig: config.GameConfig{
			TurnTimeout: 3600,
			BidTimeout:  3600,
		},
	})
	client := &synchronizedClient{id: "p1", name: "Player1"}
	h := NewHandler(HandlerDeps{Server: server, RoomManager: rm, Matcher: matcher})

	h.handlePracticeMatch(client)
	h.handleCancelMatch(client)

	messages := client.sentMessages()
	require.NotEmpty(t, messages)
	assert.Equal(t, protocol.MsgMatchQueued, messages[0].Type)
	var cancellationError *protocol.ErrorPayload
	for _, message := range messages {
		if message.Type != protocol.MsgError {
			continue
		}
		payload, err := codec.ParsePayload[protocol.ErrorPayload](message)
		require.NoError(t, err)
		if payload.CommandType == protocol.MsgCancelMatch {
			cancellationError = payload
			break
		}
	}
	require.NotNil(t, cancellationError)
	assert.Equal(t, protocol.ErrCodeMatchNotQueued, cancellationError.Code)
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
	assert.NotContains(t, created.Players, host.ID)
}
