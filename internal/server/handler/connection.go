package handler

import (
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// handlePing 处理心跳消息
func (h *Handler) handlePing(client types.ClientInterface, msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.PingPayload](msg)
	if err != nil {
		return
	}

	// 立即回复 pong
	client.SendMessage(codec.MustNewMessage(protocol.MsgPong, protocol.PongPayload{
		ClientTimestamp: payload.Timestamp,
		ServerTimestamp: time.Now().UnixMilli(),
	}))
}

// handleReconnect 处理断线重连
func (h *Handler) handleReconnect(client types.ClientInterface, msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.ReconnectPayload](msg)
	if err != nil {
		client.SendMessage(codec.NewErrorMessage(protocol.ErrCodeInvalidMsg))
		return
	}

	// 验证重连令牌
	if !h.sessionManager.CanReconnect(payload.Token, payload.PlayerID) {
		client.SendMessage(codec.NewErrorMessageWithText(protocol.ErrCodeUnknown, "重连令牌无效或已过期"))
		return
	}

	h.server.RestoreClient(payload.PlayerID, client, func() {
		h.restoreConnection(client, payload.PlayerID)
	})
}

// restoreConnection runs inside the server connection lifecycle lock.
func (h *Handler) restoreConnection(client types.ClientInterface, playerID string) {
	session := h.sessionManager.Snapshot(playerID)
	if session == nil {
		return
	}

	// 标记会话上线
	h.sessionManager.SetOnline(session.PlayerID)

	// 构建重连响应
	reconnectPayload := protocol.ReconnectedPayload{
		PlayerID:   session.PlayerID,
		PlayerName: session.PlayerName,
	}

	// 如果在房间中，尝试恢复房间信息
	if session.RoomCode != "" && h.tryRestoreRoomState(client, session, &reconnectPayload) {
		return // Active-game snapshot and turn have already been enqueued under the game lock.
	}

	// 发送重连成功消息
	client.SendMessage(codec.MustNewMessage(protocol.MsgReconnected, reconnectPayload))

	if reconnectPayload.RoomCode != "" && reconnectPayload.GameState == nil {
		if r := h.roomManager.GetRoom(reconnectPayload.RoomCode); r != nil {
			client.SendMessage(codec.MustNewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
				RoomCode: r.Code, Player: r.GetPlayerInfo(client.GetID()), Players: r.GetAllPlayersInfo(),
			}))
		}
	}

	log.Printf("🔄 玩家 %s (%s) 重连成功", session.PlayerName, session.PlayerID)
}

// tryRestoreRoomState 尝试恢复房间状态
func (h *Handler) tryRestoreRoomState(client types.ClientInterface, session *session.PlayerSession, payload *protocol.ReconnectedPayload) bool {
	room := h.roomManager.GetRoom(session.RoomCode)
	if room == nil {
		client.SetRoom("")
		return false
	}

	client.SetRoom(session.RoomCode)
	// 重连到房间
	var err error
	if gameSession := h.GetGameSession(session.RoomCode); gameSession != nil {
		err = gameSession.ReconnectClient(h.roomManager, client, h.sessionManager, payload)
		if err == nil {
			return true
		}
	} else {
		err = h.roomManager.ReconnectPlayer(client, client)
	}
	if err != nil {
		log.Printf("重连到房间失败: %v", err)
		client.SetRoom("")
		return false
	}

	client.SetRoom(session.RoomCode)
	payload.RoomCode = session.RoomCode

	return false
}
