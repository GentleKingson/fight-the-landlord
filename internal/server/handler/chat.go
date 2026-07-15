package handler

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

const (
	maxChatContentRunes = 240
	maxChatMessageIDLen = 128
)

// handleChat 处理聊天消息
func (h *Handler) handleChat(client types.ClientInterface, msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.ChatPayload](msg)
	if err != nil {
		sendChatError(client, protocol.ErrCodeInvalidMsg, "聊天消息格式无效")
		return
	}

	// 聊天限流检查
	if h.chatLimiter != nil {
		allowed, reason := h.chatLimiter.AllowChat(client.GetID())
		if !allowed {
			sendChatError(client, protocol.ErrCodeRateLimit, reason)
			return
		}
	}

	content, errText := validateChatPayload(payload)
	if errText != "" {
		sendChatError(client, protocol.ErrCodeInvalidMsg, errText)
		return
	}

	// 客户端只能决定内容、上下文和稳定消息 ID。其余字段均由服务端确认。
	payload.Content = content
	payload.SenderID = client.GetID()
	payload.SenderName = client.GetName()
	payload.IsSystem = false
	payload.RoomCode = ""
	payload.GameID = ""
	now := time.Now()
	payload.Time = now.Unix()
	payload.ServerTime = now.UnixMilli()

	if payload.Scope == "lobby" {
		if client.GetRoom() != "" {
			sendChatError(client, protocol.ErrCodeInvalidMsg, "房间中的玩家不能发送大厅消息")
			return
		}
		if h.server == nil {
			sendChatError(client, protocol.ErrCodeUnknown, "大厅聊天服务暂不可用")
			return
		}
		h.server.BroadcastToLobby(codec.MustNewMessage(protocol.MsgChat, payload))
		return
	}

	roomID := client.GetRoom()
	if roomID == "" {
		sendChatError(client, protocol.ErrCodeNotInRoom, "不在房间中，无法发送此消息")
		return
	}

	if h.roomManager == nil {
		sendChatError(client, protocol.ErrCodeUnknown, "房间服务暂不可用")
		return
	}

	room := h.roomManager.GetRoom(roomID)
	if room == nil {
		sendChatError(client, protocol.ErrCodeRoomNotFound, protocol.ErrorMessages[protocol.ErrCodeRoomNotFound])
		return
	}

	payload.RoomCode = room.Code
	if payload.Scope == "game" {
		gameSession := h.GetGameSession(room.Code)
		if gameSession == nil {
			sendChatError(client, protocol.ErrCodeGameNotStart, protocol.ErrorMessages[protocol.ErrCodeGameNotStart])
			return
		}

		gameID, state, member := gameSession.CurrentGameContext(client.GetID())
		if !member {
			sendChatError(client, protocol.ErrCodeNotInRoom, "您不是当前牌局的成员")
			return
		}
		switch state {
		case session.GameStateBidding, session.GameStatePlaying, session.GameStateEnded:
			// Game chat remains available on the result screen until the next deal.
		default:
			sendChatError(client, protocol.ErrCodeGameNotStart, protocol.ErrorMessages[protocol.ErrCodeGameNotStart])
			return
		}
		if gameID == "" {
			sendChatError(client, protocol.ErrCodeGameNotStart, protocol.ErrorMessages[protocol.ErrCodeGameNotStart])
			return
		}
		payload.GameID = gameID
	}

	if !room.BroadcastFromMember(client, codec.MustNewMessage(protocol.MsgChat, payload)) {
		sendChatError(client, protocol.ErrCodeNotInRoom, "您不是该房间的成员")
	}
}

func validateChatPayload(payload *protocol.ChatPayload) (string, string) {
	if payload.Scope != "lobby" && payload.Scope != "room" && payload.Scope != "game" {
		return "", "聊天范围必须是 lobby、room 或 game"
	}
	if !utf8.ValidString(payload.Content) {
		return "", "聊天内容必须是有效的 UTF-8 文本"
	}
	content := strings.TrimSpace(payload.Content)
	if content == "" {
		return "", "聊天内容不能为空"
	}
	if utf8.RuneCountInString(content) > maxChatContentRunes {
		return "", "聊天内容不能超过 240 个字符"
	}
	if !validChatMessageID(payload.MessageID) {
		return "", "消息 ID 格式无效"
	}
	return content, ""
}

func validChatMessageID(messageID string) bool {
	if len(messageID) == 0 || len(messageID) > maxChatMessageIDLen {
		return false
	}
	for i := range len(messageID) {
		c := messageID[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':' {
			continue
		}
		return false
	}
	return true
}

func sendChatError(client types.ClientInterface, code int, text string) {
	client.SendMessage(codec.NewCommandErrorMessageWithText(code, text, protocol.MsgChat))
}
