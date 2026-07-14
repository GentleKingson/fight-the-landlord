package handler

import (
	"time"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// handleChat 处理聊天消息
func (h *Handler) handleChat(client types.ClientInterface, msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.ChatPayload](msg)
	if err != nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeInvalidMsg, protocol.MsgChat))
		return
	}

	// 聊天限流检查
	if h.chatLimiter != nil {
		allowed, reason := h.chatLimiter.AllowChat(client.GetID())
		if !allowed {
			client.SendMessage(codec.NewCommandErrorMessageWithText(
				protocol.ErrCodeRateLimit, reason, protocol.MsgChat))
			return
		}
	}

	// 填充发送者信息
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
		client.SendMessage(codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeNotInRoom, "不在房间中，无法发送房间消息", protocol.MsgChat))
		return
	}

	if h.roomManager == nil {
		client.SendMessage(codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeUnknown, "房间服务暂不可用", protocol.MsgChat))
		return
	}

	room := h.roomManager.GetRoom(roomID)
	if room == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeRoomNotFound, protocol.MsgChat))
		return
	}

	room.Broadcast(chatMsg)
}
