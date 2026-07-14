package handler

import (
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/palemoky/fight-the-landlord/internal/config"
	r "github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestHandler_HandleChat_InvalidPayloadIsCorrelated(t *testing.T) {
	client := testutil.NewSimpleClient("p1", "Player1")
	h := NewHandler(HandlerDeps{})

	h.handleChat(client, &protocol.Message{Type: protocol.MsgChat, Payload: []byte{0xff}})

	if len(client.Messages) != 1 {
		t.Fatalf("expected one error response, got %d", len(client.Messages))
	}
	payload, err := codec.ParsePayload[protocol.ErrorPayload](client.Messages[0])
	if err != nil {
		t.Fatalf("parse error response: %v", err)
	}
	if payload.Code != protocol.ErrCodeInvalidMsg || payload.CommandType != protocol.MsgChat {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
}

func TestHandler_HandleChat_MissingRoomManagerIsCorrelated(t *testing.T) {
	client := testutil.NewSimpleClient("p1", "Player1")
	client.SetRoom("123")
	h := NewHandler(HandlerDeps{})
	msg := codec.MustNewMessage(protocol.MsgChat, protocol.ChatPayload{
		Content:   "Hello",
		Scope:     "room",
		MessageID: "m1",
	})

	h.handleChat(client, msg)

	if len(client.Messages) != 1 {
		t.Fatalf("expected one error response, got %d", len(client.Messages))
	}
	payload, err := codec.ParsePayload[protocol.ErrorPayload](client.Messages[0])
	if err != nil {
		t.Fatalf("parse error response: %v", err)
	}
	if payload.CommandType != protocol.MsgChat {
		t.Fatalf("expected chat correlation, got %+v", payload)
	}
}

func TestHandler_HandleChat_Lobby(t *testing.T) {
	// 1. Setup
	mockServer := new(testutil.MockServer)
	mockClient := new(testutil.MockClient)
	mockLimiter := new(testutil.MockChatLimiter)

	h := NewHandler(HandlerDeps{
		Server:      mockServer,
		ChatLimiter: mockLimiter,
	})

	// 2. Expectations
	// For Lobby chat:
	mockClient.On("GetID").Return("p1")
	mockClient.On("GetName").Return("Player1")
	mockLimiter.On("AllowChat", "p1").Return(true, "")

	// Expect BroadcastToLobby to be called with a MsgChat message
	mockServer.On("BroadcastToLobby", mock.MatchedBy(func(msg *protocol.Message) bool {
		return msg.Type == protocol.MsgChat
	})).Return()

	// 3. Execution
	payload := protocol.ChatPayload{
		Content: "Hello World",
		Scope:   "lobby",
	}
	msg := codec.MustNewMessage(protocol.MsgChat, payload)

	h.handleChat(mockClient, msg)

	// 4. Verification
	mockServer.AssertExpectations(t)
	mockClient.AssertExpectations(t)
	mockLimiter.AssertExpectations(t)
}

func TestHandler_HandleChat_RateLimited(t *testing.T) {
	// 1. Setup
	mockServer := new(testutil.MockServer)
	mockClient := new(testutil.MockClient)
	mockLimiter := new(testutil.MockChatLimiter)

	h := NewHandler(HandlerDeps{
		Server:      mockServer,
		ChatLimiter: mockLimiter,
	})

	// 2. Expectations
	mockClient.On("GetID").Return("p1")

	// Reject chat
	mockLimiter.On("AllowChat", "p1").Return(false, "Too fast")

	// Expect error message sent to client
	mockClient.On("SendMessage", mock.MatchedBy(func(msg *protocol.Message) bool {
		return msg.Type == protocol.MsgError
	})).Return()

	// 3. Execution
	payload := protocol.ChatPayload{Content: "Spam"}
	msg := codec.MustNewMessage(protocol.MsgChat, payload)

	h.handleChat(mockClient, msg)

	// 4. Verification
	mockServer.AssertExpectations(t)
	mockClient.AssertExpectations(t)
	mockLimiter.AssertExpectations(t)
}

func TestHandler_HandleChat_Room(t *testing.T) {
	// 1. Setup
	mockServer := new(testutil.MockServer)
	mockClient := new(testutil.MockClient)
	mockLimiter := new(testutil.MockChatLimiter)

	// Use NewMockRoom helper which returns a real *r.Room
	room := r.NewMockRoom("123", nil)

	// Create a real RoomManager and add the room
	rm := r.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	rm.AddRoomForTest(room)

	h := NewHandler(HandlerDeps{
		Server:      mockServer,
		ChatLimiter: mockLimiter,
		RoomManager: rm,
	})

	// Expectations
	mockClient.On("GetID").Return("p1")
	mockClient.On("GetName").Return("Player1")
	mockClient.On("GetRoom").Return("123")
	mockLimiter.On("AllowChat", "p1").Return(true, "")

	// Add p1 to room so broadcast works
	room.Players["p1"] = &r.RoomPlayer{
		Client: mockClient,
		Seat:   0,
		Ready:  true,
	}

	// Expect p1 (mockClient) to receive the chat message
	mockClient.On("SendMessage", mock.MatchedBy(func(msg *protocol.Message) bool {
		return msg.Type == protocol.MsgChat
	})).Return()

	// 2. Execution
	payload := protocol.ChatPayload{
		Content: "Hello Room",
		Scope:   "room",
	}
	msg := codec.MustNewMessage(protocol.MsgChat, payload)

	h.handleChat(mockClient, msg)

	// 3. Verification
	mockClient.AssertExpectations(t)
	mockLimiter.AssertExpectations(t)
}
