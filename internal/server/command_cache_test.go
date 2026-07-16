package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	serverhandler "github.com/palemoky/fight-the-landlord/internal/server/handler"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestCommandCacheCoordinatesInflightReplayAndRejectsConflicts(t *testing.T) {
	cache := newCommandCache(2, time.Minute)
	firstMessage := commandTestMessage("same", 1)
	first, err := cache.begin("player", "same", commandFingerprint(firstMessage))
	require.NoError(t, err)
	require.True(t, first.owner)

	duplicate, err := cache.begin("player", "same", commandFingerprint(firstMessage))
	require.NoError(t, err)
	require.NotNil(t, duplicate.wait)

	conflictMessage := commandTestMessage("same", 2)
	_, err = cache.begin("player", "same", commandFingerprint(conflictMessage))
	require.ErrorIs(t, err, errRequestConflict)

	response := codec.NewCommandAckMessage("same", protocol.MsgPing)
	cache.finish(first.entry, []*protocol.Message{response}, "player", "same")
	select {
	case <-duplicate.wait:
	case <-time.After(time.Second):
		t.Fatal("duplicate did not observe first command completion")
	}
	replayed := cache.responsesAfter(duplicate.entry)
	require.Len(t, replayed, 1)
	assert.Equal(t, protocol.MsgCommandAck, replayed[0].Type)
}

func TestCommandCacheEnforcesCapacityAndTTL(t *testing.T) {
	now := time.Unix(100, 0)
	cache := newCommandCache(1, time.Minute)
	cache.now = func() time.Time { return now }

	firstMessage := commandTestMessage("one", 1)
	first, err := cache.begin("player", "one", commandFingerprint(firstMessage))
	require.NoError(t, err)
	_, err = cache.begin("player", "two", commandFingerprint(commandTestMessage("two", 2)))
	require.ErrorIs(t, err, errCommandCacheFull, "an in-flight entry must never be evicted")

	cache.finish(first.entry, []*protocol.Message{codec.NewCommandAckMessage("one", protocol.MsgPing)}, "player", "one")
	second, err := cache.begin("player", "two", commandFingerprint(commandTestMessage("two", 2)))
	require.NoError(t, err, "a completed LRU entry may be evicted at capacity")
	cache.finish(second.entry, []*protocol.Message{codec.NewCommandAckMessage("two", protocol.MsgPing)}, "player", "two")

	now = now.Add(2 * time.Minute)
	third, err := cache.begin("player", "three", commandFingerprint(commandTestMessage("three", 3)))
	require.NoError(t, err, "expired entries must be pruned lazily")
	require.True(t, third.owner)
}

func TestClientCommandExecutionIsIdempotentAndCorrelated(t *testing.T) {
	client := newCommandExecutionTestClient(nil)
	frame := encodeCommandTestFrame(t, commandTestMessage("same-request", 10))

	require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))
	require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))

	messages := drainClientMessages(t, client)
	assert.Equal(t, []protocol.MessageType{
		protocol.MsgPong, protocol.MsgCommandAck,
		protocol.MsgPong, protocol.MsgCommandAck,
	}, messageTypes(messages))
	for _, message := range messages {
		if message.Type != protocol.MsgCommandAck {
			continue
		}
		payload, err := codec.ParsePayload[protocol.CommandAckPayload](message)
		require.NoError(t, err)
		assert.Equal(t, "same-request", payload.RequestID)
		assert.Equal(t, "same-request", message.Command.RequestID)
	}
}

func TestClientSameRequestReplaysTheFirstQueryResult(t *testing.T) {
	server := &Server{
		clients:      make(map[string]*Client),
		commandCache: newCommandCache(32, time.Minute),
	}
	server.handler = serverhandler.NewHandler(serverhandler.HandlerDeps{Server: server})
	client := NewClient(server, nil)
	server.clients[client.GetID()] = client
	message := codec.MustNewMessage(protocol.MsgGetOnlineCount, nil)
	message.Command = &protocol.CommandMeta{RequestID: "online-count-replay"}
	frame := encodeCommandTestFrame(t, message)

	require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))
	require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))

	messages := drainClientMessages(t, client)
	assert.Equal(t, []protocol.MessageType{
		protocol.MsgOnlineCount, protocol.MsgCommandAck,
		protocol.MsgOnlineCount, protocol.MsgCommandAck,
	}, messageTypes(messages))
	for _, message := range messages {
		require.NotNil(t, message.Command)
		assert.Equal(t, "online-count-replay", message.Command.RequestID)
	}
}

func TestCommandHandlerPanicIsNotConvertedIntoACachedSuccess(t *testing.T) {
	server := &Server{commandCache: newCommandCache(32, time.Minute)}
	// A production server always provides a leaderboard. Leaving it nil here
	// gives the test a deterministic handler panic without a recovery shim.
	server.handler = serverhandler.NewHandler(serverhandler.HandlerDeps{})
	client := NewClient(server, nil)
	message := codec.MustNewMessage(protocol.MsgGetStats, nil)
	message.Command = &protocol.CommandMeta{RequestID: "panic-must-unwind"}
	frame := encodeCommandTestFrame(t, message)

	assert.Panics(t, func() {
		client.handleIncomingFrame(websocket.BinaryMessage, frame)
	})

	lookup, err := server.commandCache.begin(client.GetID(), message.Command.RequestID, commandFingerprint(message))
	require.NoError(t, err)
	assert.True(t, lookup.owner, "panic must abort rather than cache a fabricated completion")
}

func TestClientDifferentRequestIDsExecuteIndependently(t *testing.T) {
	client := newCommandExecutionTestClient(nil)
	for index, requestID := range []string{"request-a", "request-b"} {
		frame := encodeCommandTestFrame(t, commandTestMessage(requestID, int64(index)))
		require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))
	}

	messages := drainClientMessages(t, client)
	assert.Equal(t, []protocol.MessageType{
		protocol.MsgPong, protocol.MsgCommandAck,
		protocol.MsgPong, protocol.MsgCommandAck,
	}, messageTypes(messages))
}

func TestConcurrentReplacementConnectionsExecuteSameRequestOnce(t *testing.T) {
	server := &Server{commandCache: newCommandCache(32, time.Minute)}
	server.handler = serverhandler.NewHandler(serverhandler.HandlerDeps{})
	first := NewClient(server, nil)
	second := NewClient(server, nil)
	first.rebindIdentity("shared-player", "Player", "")
	second.rebindIdentity("shared-player", "Player", "")
	frame := encodeCommandTestFrame(t, commandTestMessage("replacement-retry", 99))

	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	for _, client := range []*Client{first, second} {
		go func(client *Client) {
			defer workers.Done()
			<-start
			client.handleIncomingFrame(websocket.BinaryMessage, frame)
		}(client)
	}
	close(start)
	workers.Wait()

	messages := append(drainClientMessages(t, first), drainClientMessages(t, second)...)
	pongs := 0
	acks := 0
	for _, message := range messages {
		switch message.Type {
		case protocol.MsgPong:
			pongs++
		case protocol.MsgCommandAck:
			acks++
		}
	}
	assert.Equal(t, 2, pongs, "the duplicate receives the first Pong result from the cache")
	assert.Equal(t, 2, acks)
}

func TestClientErrorCarriesTheUniqueRequestID(t *testing.T) {
	client := newCommandExecutionTestClient(nil)
	message := codec.MustNewMessage(protocol.MsgBid, protocol.BidPayload{Bid: true})
	message.Command = &protocol.CommandMeta{
		RequestID: "bid-error", ExpectedGameID: "game-old", ExpectedTurnID: 7,
	}
	frame := encodeCommandTestFrame(t, message)
	require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))
	require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))

	messages := drainClientMessages(t, client)
	require.Len(t, messages, 2)
	for _, message := range messages {
		require.Equal(t, protocol.MsgError, message.Type)
		payload, err := codec.ParsePayload[protocol.ErrorPayload](message)
		require.NoError(t, err)
		assert.Equal(t, "bid-error", payload.RequestID)
		assert.Equal(t, protocol.MsgBid, payload.CommandType)
		assert.Equal(t, "bid-error", message.Command.RequestID)
	}
}

func TestClientRateWarningIsUncorrelatedAndCommandStillAcknowledged(t *testing.T) {
	client := newCommandExecutionTestClient(NewMessageRateLimiter(4))
	for index, requestID := range []string{"warning-a", "warning-b", "warning-c"} {
		frame := encodeCommandTestFrame(t, commandTestMessage(requestID, int64(index)))
		require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))
	}

	messages := drainClientMessages(t, client)
	var warning *protocol.Message
	ackCount := 0
	for _, message := range messages {
		if message.Type == protocol.MsgWarning {
			warning = message
		}
		if message.Type == protocol.MsgCommandAck {
			ackCount++
		}
	}
	require.NotNil(t, warning)
	assert.Nil(t, warning.Command)
	assert.Equal(t, 3, ackCount)
}

func TestConcurrentChatBroadcastCannotEnterAnotherCommandCache(t *testing.T) {
	server := &Server{commandCache: newCommandCache(32, time.Minute)}
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 3600})
	actor := NewClient(server, nil)
	actor.rebindIdentity("chat-actor", "Actor", "")
	peer := NewClient(server, nil)
	peer.rebindIdentity("chat-peer", "Peer", "")
	gameRoom, err := roomManager.CreateRoom(actor)
	require.NoError(t, err)
	_, err = roomManager.JoinRoom(peer, gameRoom.Code)
	require.NoError(t, err)
	_ = drainClientMessages(t, actor)

	command := codec.MustNewMessage(protocol.MsgChat, protocol.ChatPayload{Content: "actor", Scope: "room", MessageID: "actor-msg"})
	command.Command = &protocol.CommandMeta{RequestID: "chat-request-a"}
	entry, err := server.commandCache.begin(actor.GetID(), command.Command.RequestID, commandFingerprint(command))
	require.NoError(t, err)
	actor.beginCommandExecution(command.Command.RequestID, command.Type)

	require.True(t, gameRoom.BroadcastFromMember(actor, codec.MustNewMessage(protocol.MsgChat, protocol.ChatPayload{Content: "actor"})))
	require.True(t, gameRoom.BroadcastFromMember(peer, codec.MustNewMessage(protocol.MsgChat, protocol.ChatPayload{Content: "peer"})))

	responses := actor.endCommandExecution()
	require.Len(t, responses, 1)
	require.Equal(t, "chat-request-a", responses[0].Command.RequestID)
	ack := codec.NewCommandAckMessage(command.Command.RequestID, command.Type)
	responses = append(responses, ack)
	server.commandCache.finish(entry.entry, responses, actor.GetID(), command.Command.RequestID)

	delivered := drainClientMessages(t, actor)
	require.Len(t, delivered, 2)
	require.Equal(t, "chat-request-a", delivered[0].Command.RequestID)
	require.Nil(t, delivered[1].Command, "the peer Chat is an event, not actor A's result")

	duplicate, err := server.commandCache.begin(actor.GetID(), command.Command.RequestID, commandFingerprint(command))
	require.NoError(t, err)
	replayed := server.commandCache.responsesAfter(duplicate.entry)
	require.Equal(t, []protocol.MessageType{protocol.MsgChat, protocol.MsgCommandAck}, messageTypes(replayed))
	replayedChat, err := codec.ParsePayload[protocol.ChatPayload](replayed[0])
	require.NoError(t, err)
	require.Equal(t, "actor", replayedChat.Content)
}

func TestConcurrentReadyBroadcastCannotEnterAnotherCommandCache(t *testing.T) {
	server := &Server{commandCache: newCommandCache(32, time.Minute)}
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 3600})
	actor := NewClient(server, nil)
	actor.rebindIdentity("ready-actor", "Actor", "")
	peer := NewClient(server, nil)
	peer.rebindIdentity("ready-peer", "Peer", "")
	gameRoom, err := roomManager.CreateRoom(actor)
	require.NoError(t, err)
	_, err = roomManager.JoinRoom(peer, gameRoom.Code)
	require.NoError(t, err)
	_ = drainClientMessages(t, actor)

	command := codec.MustNewMessage(protocol.MsgReady, nil)
	command.Command = &protocol.CommandMeta{RequestID: "ready-request-a"}
	entry, err := server.commandCache.begin(actor.GetID(), command.Command.RequestID, commandFingerprint(command))
	require.NoError(t, err)
	actor.beginCommandExecution(command.Command.RequestID, command.Type)

	require.NoError(t, roomManager.SetPlayerReady(actor, true))
	require.NoError(t, roomManager.SetPlayerReady(peer, true))

	responses := actor.endCommandExecution()
	require.Len(t, responses, 1)
	require.Equal(t, "ready-request-a", responses[0].Command.RequestID)
	ack := codec.NewCommandAckMessage(command.Command.RequestID, command.Type)
	responses = append(responses, ack)
	server.commandCache.finish(entry.entry, responses, actor.GetID(), command.Command.RequestID)

	delivered := drainClientMessages(t, actor)
	require.Len(t, delivered, 2)
	require.Equal(t, "ready-request-a", delivered[0].Command.RequestID)
	require.Nil(t, delivered[1].Command, "the peer Ready is an event, not actor A's result")
	peerReady, err := codec.ParsePayload[protocol.PlayerReadyPayload](delivered[1])
	require.NoError(t, err)
	require.Equal(t, peer.GetID(), peerReady.PlayerID)

	duplicate, err := server.commandCache.begin(actor.GetID(), command.Command.RequestID, commandFingerprint(command))
	require.NoError(t, err)
	replayed := server.commandCache.responsesAfter(duplicate.entry)
	require.Equal(t, []protocol.MessageType{protocol.MsgPlayerReady, protocol.MsgCommandAck}, messageTypes(replayed))
	replayedReady, err := codec.ParsePayload[protocol.PlayerReadyPayload](replayed[0])
	require.NoError(t, err)
	require.Equal(t, actor.GetID(), replayedReady.PlayerID)
}

func TestLegacyChatFixtureIsCountedAndIdempotent(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", "legacy_chat_payload.json"))
	require.NoError(t, err)
	legacyEnvelope := &protocol.Message{Type: protocol.MsgChat, Payload: fixture}
	frame := encodeCommandTestFrame(t, legacyEnvelope)

	broadcastServer := new(testutil.MockServer)
	broadcastServer.On("BroadcastToLobby", mock.Anything).Once()
	server := &Server{commandCache: newCommandCache(32, time.Minute)}
	server.handler = serverhandler.NewHandler(serverhandler.HandlerDeps{Server: broadcastServer})
	client := NewClient(server, nil)

	require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))
	require.True(t, client.handleIncomingFrame(websocket.BinaryMessage, frame))

	broadcastServer.AssertExpectations(t)
	assert.Equal(t, int64(1), server.LegacyChatMessages())
	messages := drainClientMessages(t, client)
	assert.Equal(t, []protocol.MessageType{protocol.MsgCommandAck, protocol.MsgCommandAck}, messageTypes(messages))
	first, err := codec.ParsePayload[protocol.CommandAckPayload](messages[0])
	require.NoError(t, err)
	second, err := codec.ParsePayload[protocol.CommandAckPayload](messages[1])
	require.NoError(t, err)
	assert.Equal(t, first.RequestID, second.RequestID)
	assert.Contains(t, first.RequestID, "legacy-chat:")
}

func commandTestMessage(requestID string, timestamp int64) *protocol.Message {
	message := codec.MustNewMessage(protocol.MsgPing, protocol.PingPayload{Timestamp: timestamp})
	message.Command = &protocol.CommandMeta{RequestID: requestID}
	return message
}

func encodeCommandTestFrame(t *testing.T, message *protocol.Message) []byte {
	t.Helper()
	frame, err := codec.Encode(message)
	require.NoError(t, err)
	return frame
}

func newCommandExecutionTestClient(limiter *MessageRateLimiter) *Client {
	server := &Server{
		messageLimiter: limiter,
		commandCache:   newCommandCache(32, time.Minute),
	}
	server.handler = serverhandler.NewHandler(serverhandler.HandlerDeps{})
	return NewClient(server, nil)
}

func drainClientMessages(t *testing.T, client *Client) []*protocol.Message {
	t.Helper()
	messages := make([]*protocol.Message, 0, len(client.send))
	for len(client.send) > 0 {
		frame := <-client.send
		message, err := codec.Decode(frame)
		require.NoError(t, err)
		messages = append(messages, message)
	}
	t.Cleanup(func() {
		for _, message := range messages {
			codec.PutMessage(message)
		}
	})
	return messages
}

func messageTypes(messages []*protocol.Message) []protocol.MessageType {
	types := make([]protocol.MessageType, len(messages))
	for index, message := range messages {
		types[index] = message.Type
	}
	return types
}
