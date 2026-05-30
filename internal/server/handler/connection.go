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

	// 获取旧会话
	session := h.sessionManager.GetSession(payload.PlayerID)
	if session == nil {
		client.SendMessage(codec.NewErrorMessageWithText(protocol.ErrCodeUnknown, "会话不存在"))
		return
	}

	// 注意：由于ClientInterface不允许修改ID/Name，我们需要通过Server层面处理
	// 这里我们假设client已经是正确的类型，可以进行类型断言
	oldID := client.GetID()

	// 从旧 ID 注销，用新 ID 注册
	h.server.UnregisterClient(oldID)
	h.server.RegisterClient(session.PlayerID, client)

	// 标记会话上线
	h.sessionManager.SetOnline(session.PlayerID)

	// 构建重连响应
	reconnectPayload := protocol.ReconnectedPayload{
		PlayerID:   session.PlayerID,
		PlayerName: session.PlayerName,
	}

	// 如果在房间中，尝试恢复房间信息
	if session.RoomCode != "" {
		h.tryRestoreRoomState(client, session, &reconnectPayload)
	}

	// 发送重连成功消息
	client.SendMessage(codec.MustNewMessage(protocol.MsgReconnected, reconnectPayload))

	// 快照恢复后，若正轮到该玩家，补发当前回合通知，恢复其操作提示与倒计时（快照本身不含 IsGrab / 剩余时间等回合信息）。须在 MsgReconnected 之后发送，确保客户端先应用快照、再设置回合提示。
	if reconnectPayload.GameState != nil && reconnectPayload.GameState.CurrentTurn == session.PlayerID {
		if gameSession := h.GetGameSession(session.RoomCode); gameSession != nil {
			gameSession.ResendTurnTo(client)
		}
	}

	log.Printf("🔄 玩家 %s (%s) 重连成功", session.PlayerName, session.PlayerID)
}

// tryRestoreRoomState 尝试恢复房间状态
func (h *Handler) tryRestoreRoomState(client types.ClientInterface, session *session.PlayerSession, payload *protocol.ReconnectedPayload) {
	room := h.roomManager.GetRoom(session.RoomCode)
	if room == nil {
		return
	}

	oldClient := h.server.GetClientByID(session.PlayerID)
	if oldClient == nil {
		return
	}

	// 重连到房间
	if err := h.roomManager.ReconnectPlayer(oldClient, client); err != nil {
		log.Printf("重连到房间失败: %v", err)
		return
	}

	client.SetRoom(session.RoomCode)
	payload.RoomCode = session.RoomCode

	// 如果游戏正在进行，恢复游戏状态
	if gameSession := h.GetGameSession(session.RoomCode); gameSession != nil {
		payload.GameState = gameSession.BuildGameStateDTO(session.PlayerID, h.sessionManager)
	}
}
