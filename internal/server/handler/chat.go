package handler

import (
	"fmt"
	"strings"
	"time"
	"unicode"
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
	if playerMuted(h.server, client.GetID()) {
		sendChatError(client, protocol.ErrCodeRateLimit, "您已被暂时禁言")
		return
	}
	payload, legacy, err := codec.ParseChatPayload(msg)
	if err != nil {
		sendChatError(client, protocol.ErrCodeInvalidMsg, "聊天消息格式无效")
		return
	}
	if legacy {
		h.legacyChatMessages.Add(1)
		if !validChatMessageID(payload.MessageID) && msg.Command != nil {
			payload.MessageID = msg.Command.RequestID
		}
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
	payload.MessageID = fmt.Sprintf("srv:%d:%d", now.UnixNano(), h.chatMessageSequence.Add(1))

	if payload.Scope == "lobby" {
		h.broadcastLobbyChat(client, payload)
		return
	}
	h.broadcastRoomChat(client, payload)
}

func (h *Handler) broadcastLobbyChat(client types.ClientInterface, payload *protocol.ChatPayload) {
	if client.GetRoom() != "" {
		sendChatError(client, protocol.ErrCodeInvalidMsg, "房间中的玩家不能发送大厅消息")
		return
	}
	if h.server == nil {
		sendChatError(client, protocol.ErrCodeUnknown, "大厅聊天服务暂不可用")
		return
	}
	message := codec.MustNewMessage(protocol.MsgChat, payload)
	if broadcaster, ok := h.server.(interface {
		BroadcastToLobbyFrom(types.ClientInterface, *protocol.Message)
	}); ok {
		broadcaster.BroadcastToLobbyFrom(client, message)
		return
	}
	h.server.BroadcastToLobby(message)
}

func (h *Handler) broadcastRoomChat(client types.ClientInterface, payload *protocol.ChatPayload) {
	roomID := client.GetRoom()
	if roomID == "" {
		sendChatError(client, protocol.ErrCodeNotInRoom, "不在房间中，无法发送此消息")
		return
	}

	if h.roomManager == nil {
		sendChatError(client, protocol.ErrCodeUnknown, "房间服务暂不可用")
		return
	}

	gameRoom := h.roomManager.GetRoom(roomID)
	if gameRoom == nil {
		sendChatError(client, protocol.ErrCodeRoomNotFound, protocol.ErrorMessages[protocol.ErrCodeRoomNotFound])
		return
	}

	payload.RoomCode = gameRoom.Code
	if payload.Scope == "game" {
		gameID, ok := h.authorizeGameChat(client, gameRoom.Code)
		if !ok {
			return
		}
		payload.GameID = gameID
	}

	if !gameRoom.BroadcastFromMember(client, codec.MustNewMessage(protocol.MsgChat, payload)) {
		sendChatError(client, protocol.ErrCodeNotInRoom, "您不是该房间的成员")
	}
}

func (h *Handler) authorizeGameChat(client types.ClientInterface, roomCode string) (string, bool) {
	gameSession := h.GetGameSession(roomCode)
	if gameSession == nil {
		sendChatError(client, protocol.ErrCodeGameNotStart, protocol.ErrorMessages[protocol.ErrCodeGameNotStart])
		return "", false
	}
	gameID, state, member := gameSession.CurrentGameContext(client.GetID())
	if !member {
		sendChatError(client, protocol.ErrCodeNotInRoom, "您不是当前牌局的成员")
		return "", false
	}
	if state != session.GameStateBidding && state != session.GameStatePlaying && state != session.GameStateEnded {
		sendChatError(client, protocol.ErrCodeGameNotStart, protocol.ErrorMessages[protocol.ErrCodeGameNotStart])
		return "", false
	}
	if gameID == "" {
		sendChatError(client, protocol.ErrCodeGameNotStart, protocol.ErrorMessages[protocol.ErrCodeGameNotStart])
		return "", false
	}
	return gameID, true
}

func validateChatPayload(payload *protocol.ChatPayload) (content, errText string) {
	if payload.Scope != "lobby" && payload.Scope != "room" && payload.Scope != "game" {
		return "", "聊天范围必须是 lobby、room 或 game"
	}
	if !utf8.ValidString(payload.Content) {
		return "", "聊天内容必须是有效的 UTF-8 文本"
	}
	content = strings.TrimSpace(stripDangerousChatRunes(payload.Content))
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

func stripDangerousChatRunes(content string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || isBidirectionalControl(r) {
			return -1
		}
		return r
	}, content)
}

func isBidirectionalControl(r rune) bool {
	switch r {
	case '\u061C', '\u200E', '\u200F',
		'\u202A', '\u202B', '\u202C', '\u202D', '\u202E',
		'\u2066', '\u2067', '\u2068', '\u2069',
		'\u206A', '\u206B', '\u206C', '\u206D', '\u206E', '\u206F':
		return true
	default:
		return false
	}
}

func validChatMessageID(messageID string) bool {
	if messageID == "" || len(messageID) > maxChatMessageIDLen {
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
	sendMessage(client, codec.NewCommandErrorMessageWithText(code, text, protocol.MsgChat))
}
