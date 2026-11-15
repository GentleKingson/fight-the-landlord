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

func TestHandler_HandleChat_SanitizesAndRejectsInvalidPayloads(t *testing.T) {
	t.Run("forces IsSystem=false and strips control chars", func(t *testing.T) {
		mockServer := new(testutil.MockServer)
		mockClient := new(testutil.MockClient)
		mockLimiter := new(testutil.MockChatLimiter)

		h := NewHandler(HandlerDeps{
			Server:      mockServer,
			ChatLimiter: mockLimiter,
		})

		mockClient.On("GetID").Return("p1")
		mockClient.On("GetName").Return("Player1")
		mockLimiter.On("AllowChat", "p1").Return(true, "")

		var captured *protocol.ChatPayload
		mockServer.On("BroadcastToLobby", mock.MatchedBy(func(msg *protocol.Message) bool {
			if msg.Type != protocol.MsgChat {
				return false
			}
			p, err := codec.ParsePayload[protocol.ChatPayload](msg)
			if err != nil {
				return false
			}
			captured = p
			return true
		})).Return()

		msg := codec.MustNewMessage(protocol.MsgChat, protocol.ChatPayload{
			Content:  "  \x1b[31m服务器即将重启\x07\n  ",
			Scope:    "lobby",
			IsSystem: true,
		})
		h.handleChat(mockClient, msg)

		mockServer.AssertExpectations(t)
		if captured == nil {
			t.Fatal("expected chat payload to be broadcast")
		}
		if captured.IsSystem {
			t.Fatal("client must not be able to forge IsSystem=true")
		}
		if captured.Content != "[31m服务器即将重启" {
			t.Fatalf("unexpected sanitized content: %q", captured.Content)
		}
	})

	t.Run("drops whitespace or control-only content without broadcasting", func(t *testing.T) {
		mockServer := new(testutil.MockServer)
		mockClient := new(testutil.MockClient)
		mockLimiter := new(testutil.MockChatLimiter)

		h := NewHandler(HandlerDeps{
			Server:      mockServer,
			ChatLimiter: mockLimiter,
		})

		msg := codec.MustNewMessage(protocol.MsgChat, protocol.ChatPayload{
			Content: " \t\r\n\x00\x1b ",
			Scope:   "lobby",
		})
		h.handleChat(mockClient, msg)

		mockServer.AssertNotCalled(t, "BroadcastToLobby", mock.Anything)
		mockLimiter.AssertNotCalled(t, "AllowChat", mock.Anything)
	})

	t.Run("rejects content exceeding 50 runes", func(t *testing.T) {
		mockServer := new(testutil.MockServer)
		mockClient := new(testutil.MockClient)
		mockLimiter := new(testutil.MockChatLimiter)

		h := NewHandler(HandlerDeps{
			Server:      mockServer,
			ChatLimiter: mockLimiter,
		})

		mockClient.On("SendMessage", mock.MatchedBy(func(msg *protocol.Message) bool {
			return msg.Type == protocol.MsgError
		})).Return()

		longText := ""
		for range 51 {
			longText += "哈"
		}
		msg := codec.MustNewMessage(protocol.MsgChat, protocol.ChatPayload{
			Content: longText,
			Scope:   "lobby",
		})
		h.handleChat(mockClient, msg)

		mockClient.AssertExpectations(t)
		mockServer.AssertNotCalled(t, "BroadcastToLobby", mock.Anything)
	})
}
