package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	gameroom "github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestHandler_HandleChat_InvalidPayloadIsCorrelated(t *testing.T) {
	client := testutil.NewSimpleClient("p1", "Player1")
	h := NewHandler(HandlerDeps{})

	h.handleChat(client, &protocol.Message{Type: protocol.MsgChat, Payload: []byte{0xff}})

	requireChatError(t, client, protocol.ErrCodeInvalidMsg)
}

func TestHandler_HandleChat_InvalidUTF8IsCorrelated(t *testing.T) {
	client := testutil.NewSimpleClient("p1", "Player1")
	h := NewHandler(HandlerDeps{})
	// ChatPayload.content contains one invalid UTF-8 byte. scope and message_id
	// are otherwise valid protobuf fields.
	payload := []byte{
		0x1a, 0x01, 0xff,
		0x22, 0x05, 'l', 'o', 'b', 'b', 'y',
		0x3a, 0x02, 'm', '1',
	}

	h.handleChat(client, &protocol.Message{Type: protocol.MsgChat, Payload: payload})

	requireChatError(t, client, protocol.ErrCodeInvalidMsg)
}

func TestHandler_HandleChat_RejectsInvalidFields(t *testing.T) {
	valid := protocol.ChatPayload{Content: "hello", Scope: "lobby", MessageID: "msg-1"}
	tests := []struct {
		name   string
		mutate func(*protocol.ChatPayload)
	}{
		{name: "missing scope", mutate: func(p *protocol.ChatPayload) { p.Scope = "" }},
		{name: "scope is case sensitive", mutate: func(p *protocol.ChatPayload) { p.Scope = "ROOM" }},
		{name: "unknown scope", mutate: func(p *protocol.ChatPayload) { p.Scope = "global" }},
		{name: "empty content", mutate: func(p *protocol.ChatPayload) { p.Content = "" }},
		{name: "whitespace content", mutate: func(p *protocol.ChatPayload) { p.Content = " \t\n　" }},
		{name: "content over Unicode limit", mutate: func(p *protocol.ChatPayload) { p.Content = strings.Repeat("界", 241) }},
		{name: "missing message id", mutate: func(p *protocol.ChatPayload) { p.MessageID = "" }},
		{name: "message id too long", mutate: func(p *protocol.ChatPayload) { p.MessageID = strings.Repeat("a", 129) }},
		{name: "message id contains whitespace", mutate: func(p *protocol.ChatPayload) { p.MessageID = "message 1" }},
		{name: "message id contains slash", mutate: func(p *protocol.ChatPayload) { p.MessageID = "message/1" }},
		{name: "message id is non ASCII", mutate: func(p *protocol.ChatPayload) { p.MessageID = "消息-1" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := valid
			tt.mutate(&payload)
			client := testutil.NewSimpleClient("p1", "Player1")
			h := NewHandler(HandlerDeps{})

			h.handleChat(client, chatMessage(payload))

			requireChatError(t, client, protocol.ErrCodeInvalidMsg)
		})
	}
}

func TestHandler_HandleChat_LobbyCanonicalizesAuthoritativeFields(t *testing.T) {
	server := new(testutil.MockServer)
	client := testutil.NewSimpleClient("p1", "Player1")
	var broadcast *protocol.Message
	server.On("BroadcastToLobby", mock.Anything).Run(func(args mock.Arguments) {
		broadcast = args.Get(0).(*protocol.Message)
	}).Once()
	h := NewHandler(HandlerDeps{Server: server})
	before := time.Now()

	h.handleChat(client, chatMessage(protocol.ChatPayload{
		SenderID:   "spoofed-id",
		SenderName: "Spoofed Name",
		Content:    "  你好　",
		Scope:      "lobby",
		Time:       1,
		IsSystem:   true,
		MessageID:  "3fcbf9f4-6e5b-4af3-866d-93fe49782afd",
		RoomCode:   "OTHER",
		GameID:     "other-game",
		ServerTime: 2,
	}))

	server.AssertExpectations(t)
	require.NotNil(t, broadcast)
	got := requireChatPayload(t, broadcast)
	assert.Equal(t, "p1", got.SenderID)
	assert.Equal(t, "Player1", got.SenderName)
	assert.Equal(t, "你好", got.Content)
	assert.Equal(t, "lobby", got.Scope)
	assert.False(t, got.IsSystem)
	assert.Equal(t, "3fcbf9f4-6e5b-4af3-866d-93fe49782afd", got.MessageID)
	assert.Empty(t, got.RoomCode)
	assert.Empty(t, got.GameID)
	assert.GreaterOrEqual(t, got.Time, before.Unix())
	assert.LessOrEqual(t, got.Time, time.Now().Unix())
	assert.GreaterOrEqual(t, got.ServerTime, before.UnixMilli())
	assert.LessOrEqual(t, got.ServerTime, time.Now().UnixMilli())
}

func TestHandler_HandleChat_AcceptsUnicodeAndIDBoundaries(t *testing.T) {
	server := new(testutil.MockServer)
	client := testutil.NewSimpleClient("p1", "Player1")
	var broadcast *protocol.Message
	server.On("BroadcastToLobby", mock.Anything).Run(func(args mock.Arguments) {
		broadcast = args.Get(0).(*protocol.Message)
	}).Once()
	h := NewHandler(HandlerDeps{Server: server})

	h.handleChat(client, chatMessage(protocol.ChatPayload{
		Content:   strings.Repeat("界", 240),
		Scope:     "lobby",
		MessageID: strings.Repeat("a", 128),
	}))

	server.AssertExpectations(t)
	got := requireChatPayload(t, broadcast)
	assert.Equal(t, 240, len([]rune(got.Content)))
	assert.Len(t, got.MessageID, 128)
}

func TestHandler_HandleChat_LobbyRequiresLobbyMembership(t *testing.T) {
	server := new(testutil.MockServer)
	client := testutil.NewSimpleClient("p1", "Player1")
	client.SetRoom("ROOM-A")
	h := NewHandler(HandlerDeps{Server: server})

	h.handleChat(client, chatMessage(protocol.ChatPayload{
		Content: "hello", Scope: "lobby", MessageID: "m1",
	}))

	requireChatError(t, client, protocol.ErrCodeInvalidMsg)
	server.AssertNotCalled(t, "BroadcastToLobby", mock.Anything)
}

func TestHandler_HandleChat_RateLimitedIsCorrelated(t *testing.T) {
	client := testutil.NewSimpleClient("p1", "Player1")
	limiter := &stubChatLimiter{allowed: false, reason: "Too fast"}
	h := NewHandler(HandlerDeps{ChatLimiter: limiter})

	h.handleChat(client, chatMessage(protocol.ChatPayload{
		Content: "Spam", Scope: "lobby", MessageID: "m1",
	}))

	errorPayload := requireChatError(t, client, protocol.ErrCodeRateLimit)
	assert.Equal(t, "Too fast", errorPayload.Message)
	assert.Equal(t, 1, limiter.calls)
}

func TestHandler_HandleChat_RoomIsIsolatedAndMetadataIsAuthoritative(t *testing.T) {
	sender := testutil.NewSimpleClient("p1", "Player1")
	peer := testutil.NewSimpleClient("p2", "Player2")
	outsider := testutil.NewSimpleClient("p3", "Player3")
	sender.SetRoom("ROOM-A")
	peer.SetRoom("ROOM-A")
	outsider.SetRoom("ROOM-B")
	roomA := roomWithClients("ROOM-A", sender, peer)
	roomB := roomWithClients("ROOM-B", outsider)
	rm := gameroom.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	rm.AddRoomForTest(roomA)
	rm.AddRoomForTest(roomB)
	h := NewHandler(HandlerDeps{RoomManager: rm})

	h.handleChat(sender, chatMessage(protocol.ChatPayload{
		SenderID: "spoof", Content: " room hello ", Scope: "room", IsSystem: true,
		MessageID: "room-msg-1", RoomCode: "ROOM-B", GameID: "spoof-game",
	}))

	require.Len(t, sender.Messages, 1)
	require.Len(t, peer.Messages, 1)
	assert.Empty(t, outsider.Messages)
	got := requireChatPayload(t, sender.Messages[0])
	assert.Equal(t, "p1", got.SenderID)
	assert.Equal(t, "room hello", got.Content)
	assert.Equal(t, "room-msg-1", got.MessageID)
	assert.Equal(t, "ROOM-A", got.RoomCode)
	assert.Empty(t, got.GameID)
	assert.False(t, got.IsSystem)
	assert.Equal(t, got, requireChatPayload(t, peer.Messages[0]))
}

func TestHandler_HandleChat_RoomRejectsSpoofedMembership(t *testing.T) {
	member := testutil.NewSimpleClient("member", "Member")
	ghost := testutil.NewSimpleClient("ghost", "Ghost")
	member.SetRoom("ROOM-A")
	ghost.SetRoom("ROOM-A")
	room := roomWithClients("ROOM-A", member)
	rm := gameroom.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	rm.AddRoomForTest(room)
	h := NewHandler(HandlerDeps{RoomManager: rm})

	h.handleChat(ghost, chatMessage(protocol.ChatPayload{
		Content: "hello", Scope: "room", MessageID: "m1",
	}))

	requireChatError(t, ghost, protocol.ErrCodeNotInRoom)
	assert.Empty(t, member.Messages)
}

func TestHandler_HandleChat_RoomRejectsReplacedConnection(t *testing.T) {
	original := testutil.NewSimpleClient("p1", "Original")
	replacement := testutil.NewSimpleClient("p1", "Replacement")
	peer := testutil.NewSimpleClient("p2", "Peer")
	original.SetRoom("ROOM-A")
	peer.SetRoom("ROOM-A")
	gameRoom := roomWithClients("ROOM-A", original, peer)
	rm := gameroom.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	rm.AddRoomForTest(gameRoom)
	require.NoError(t, rm.ReconnectPlayer("p1", gameRoom.Code, replacement))
	replacement.Messages = nil
	peer.Messages = nil
	h := NewHandler(HandlerDeps{RoomManager: rm})

	h.handleChat(original, chatMessage(protocol.ChatPayload{
		Content: "stale", Scope: "room", MessageID: "m1",
	}))

	requireChatError(t, original, protocol.ErrCodeNotInRoom)
	assert.Empty(t, replacement.Messages)
	assert.Empty(t, peer.Messages)
}

func TestHandler_HandleChat_RoomRequiresRoomService(t *testing.T) {
	client := testutil.NewSimpleClient("p1", "Player1")
	client.SetRoom("ROOM-A")
	h := NewHandler(HandlerDeps{})

	h.handleChat(client, chatMessage(protocol.ChatPayload{
		Content: "hello", Scope: "room", MessageID: "m1",
	}))

	requireChatError(t, client, protocol.ErrCodeUnknown)
}

func TestHandler_HandleChat_GameIsIsolatedAndUsesCurrentGameID(t *testing.T) {
	room, game, clients := runningGameChatFixture(t)
	outsider := testutil.NewSimpleClient("outside", "Outside")
	outsider.SetRoom("OTHER")
	otherRoom := roomWithClients("OTHER", outsider)
	rm := gameroom.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	rm.AddRoomForTest(room)
	rm.AddRoomForTest(otherRoom)
	h := NewHandler(HandlerDeps{RoomManager: rm})
	h.SetGameSession(room.Code, game)
	gameID, state, member := game.CurrentGameContext(clients[0].GetID())
	require.True(t, member)
	require.Equal(t, session.GameStateBidding, state)

	h.handleChat(clients[0], chatMessage(protocol.ChatPayload{
		SenderID: "spoof", Content: "game hello", Scope: "game", IsSystem: true,
		MessageID: "game-msg-1", RoomCode: "OTHER", GameID: "old-game",
	}))

	for _, client := range clients {
		require.Len(t, client.Messages, 1)
		got := requireChatPayload(t, client.Messages[0])
		assert.Equal(t, room.Code, got.RoomCode)
		assert.Equal(t, gameID, got.GameID)
		assert.Equal(t, "game-msg-1", got.MessageID)
		assert.False(t, got.IsSystem)
	}
	assert.Empty(t, outsider.Messages)
}

func TestHandler_HandleChat_GameRequiresSession(t *testing.T) {
	client := testutil.NewSimpleClient("p1", "Player1")
	client.SetRoom("ROOM-A")
	room := roomWithClients("ROOM-A", client)
	rm := gameroom.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	rm.AddRoomForTest(room)
	h := NewHandler(HandlerDeps{RoomManager: rm})

	h.handleChat(client, chatMessage(protocol.ChatPayload{
		Content: "hello", Scope: "game", MessageID: "m1",
	}))

	requireChatError(t, client, protocol.ErrCodeGameNotStart)
}

func TestHandler_HandleChat_GameRequiresSessionMembership(t *testing.T) {
	room, game, clients := runningGameChatFixture(t)
	intruder := testutil.NewSimpleClient("intruder", "Intruder")
	intruder.SetRoom(room.Code)
	room.AddPlayerForTest(intruder, 3, false)
	rm := gameroom.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	rm.AddRoomForTest(room)
	h := NewHandler(HandlerDeps{RoomManager: rm})
	h.SetGameSession(room.Code, game)

	h.handleChat(intruder, chatMessage(protocol.ChatPayload{
		Content: "hello", Scope: "game", MessageID: "m1",
	}))

	requireChatError(t, intruder, protocol.ErrCodeNotInRoom)
	for _, client := range clients {
		assert.Empty(t, client.Messages)
	}
}

func chatMessage(payload protocol.ChatPayload) *protocol.Message {
	return codec.MustNewMessage(protocol.MsgChat, payload)
}

func requireChatError(t *testing.T, client *testutil.SimpleClient, code int) *protocol.ErrorPayload {
	t.Helper()
	require.Len(t, client.Messages, 1)
	payload, err := codec.ParsePayload[protocol.ErrorPayload](client.Messages[0])
	require.NoError(t, err)
	assert.Equal(t, code, payload.Code)
	assert.Equal(t, protocol.MsgChat, payload.CommandType)
	return payload
}

func requireChatPayload(t *testing.T, msg *protocol.Message) *protocol.ChatPayload {
	t.Helper()
	require.NotNil(t, msg)
	require.Equal(t, protocol.MsgChat, msg.Type)
	payload, err := codec.ParsePayload[protocol.ChatPayload](msg)
	require.NoError(t, err)
	return payload
}

func roomWithClients(code string, clients ...*testutil.SimpleClient) *gameroom.Room {
	room := gameroom.NewMockRoom(code, nil)
	playerOrder := make([]string, 0, len(clients))
	for seat, client := range clients {
		room.AddPlayerForTest(client, seat, false)
		playerOrder = append(playerOrder, client.GetID())
	}
	room.SetPlayerOrderForTest(playerOrder)
	return room
}

func runningGameChatFixture(t *testing.T) (*gameroom.Room, *session.GameSession, []*testutil.SimpleClient) {
	t.Helper()
	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient("p1", "Player1"),
		testutil.NewSimpleClient("p2", "Player2"),
		testutil.NewSimpleClient("p3", "Player3"),
	}
	for _, client := range clients {
		client.SetRoom("GAME-A")
	}
	room := roomWithClients("GAME-A", clients...)
	for _, client := range clients {
		require.True(t, room.SetPlayerReadyForTest(client.GetID(), true))
	}
	game := session.NewGameSession(room, nil, config.GameConfig{BidTimeout: 300, TurnTimeout: 300})
	game.Start()
	t.Cleanup(game.StopAllTimers)
	for _, client := range clients {
		client.Messages = nil
	}
	return room, game, clients
}

type stubChatLimiter struct {
	allowed bool
	reason  string
	calls   int
}

func (l *stubChatLimiter) AllowChat(string) (bool, string) {
	l.calls++
	return l.allowed, l.reason
}

func (l *stubChatLimiter) ClearRateLimit(string) {}
