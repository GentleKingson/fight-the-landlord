package handler

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

const maxChatContentRunes = 50

func sanitizeChatContent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// handleChat 处理聊天消息
func (h *Handler) handleChat(client types.ClientInterface, msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.ChatPayload](msg)
	if err != nil {
		return
	}

	if !utf8.ValidString(payload.Content) {
		client.SendMessage(codec.NewErrorMessage(protocol.ErrCodeInvalidMsg))
		return
	}

	content := sanitizeChatContent(payload.Content)
	if content == "" {
		return
	}
	if utf8.RuneCountInString(content) > maxChatContentRunes {
		client.SendMessage(codec.NewErrorMessageWithText(
			protocol.ErrCodeInvalidMsg, "聊天消息不能超过 50 个字符"))
		return
	}

	// 聊天限流检查
	if h.chatLimiter != nil {
		allowed, reason := h.chatLimiter.AllowChat(client.GetID())
		if !allowed {
			client.SendMessage(codec.NewErrorMessageWithText(
				protocol.ErrCodeRateLimit, reason))
			return
		}
	}

	// 客户端上行聊天一律不可信为系统消息，覆盖发送者与内容字段
	payload.Content = content
	payload.IsSystem = false
	payload.SenderID = client.GetID()
	payload.SenderName = client.GetName()
	payload.Time = time.Now().Unix()

	chatMsg := codec.MustNewMessage(protocol.MsgChat, payload)

	// 大厅聊天：广播给所有大厅玩家
	if payload.Scope != "room" {
		h.server.BroadcastToLobby(chatMsg)
		return
	}

	// 房间聊天：检查房间状态
	roomID := client.GetRoom()
	if roomID == "" {
		client.SendMessage(codec.NewErrorMessageWithText(protocol.ErrCodeNotInRoom, "不在房间中，无法发送房间消息"))
		return
	}

	if h.roomManager == nil {
		return
	}

	room := h.roomManager.GetRoom(roomID)
	if room == nil {
		return
	}

	room.Broadcast(chatMsg)
}
