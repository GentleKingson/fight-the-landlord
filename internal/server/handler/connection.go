package handler

import (
	"errors"
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

	temporaryID := client.GetID()
	restored, err := h.sessionManager.RestoreSession(payload.Token, payload.PlayerID, temporaryID)
	if err != nil {
		code := protocol.ErrCodeReconnectInvalid
		if errors.Is(err, session.ErrReconnectExpired) {
			code = protocol.ErrCodeReconnectExpired
		}
		client.SendMessage(codec.NewErrorMessage(code))
		return
	}

	previous, err := h.server.RebindClient(
		temporaryID,
		restored.PlayerID,
		restored.PlayerName,
		restored.RoomCode,
		client,
	)
	if err != nil {
		if !h.sessionManager.RollbackRestore(restored) {
			h.sessionManager.SetOffline(restored.PlayerID)
		}
		log.Printf("重连身份绑定失败: %v", err)
		client.SendMessage(codec.NewErrorMessageWithText(protocol.ErrCodeUnknown, "重连身份恢复失败"))
		client.Close()
		return
	}

	// 构建重连响应
	reconnectPayload := protocol.ReconnectedPayload{
		PlayerID:       restored.PlayerID,
		PlayerName:     restored.PlayerName,
		ReconnectToken: restored.ReconnectToken,
	}

	// 如果在房间中，尝试恢复房间信息
	if restored.RoomCode != "" {
		h.tryRestoreRoomState(client, restored, &reconnectPayload)
	}

	// A replaced connection may finish its disconnect cleanup concurrently.
	// Reassert online status after the client and room mappings are installed.
	h.sessionManager.SetOnline(restored.PlayerID)
	if previous != nil && previous != client {
		previous.Close()
	}

	// 完整快照已包含回合、截止时间和叫抢状态；再补发相对
	// timeout 会把精确的绝对截止时间重置为“从收包时开始”。
	reconnectedMessage := codec.MustNewMessage(protocol.MsgReconnected, reconnectPayload)
	reconnectedMessage.Event = session.EventMetaFromGameStateDTO(reconnectPayload.GameState)
	client.SendMessage(reconnectedMessage)

	log.Printf("🔄 玩家 %s (%s) 重连成功", restored.PlayerName, restored.PlayerID)
}

// tryRestoreRoomState 尝试恢复房间状态
func (h *Handler) tryRestoreRoomState(client types.ClientInterface, restored *session.RestoredSession, payload *protocol.ReconnectedPayload) {
	room := h.roomManager.GetRoom(restored.RoomCode)
	if room == nil {
		client.SetRoom("")
		return
	}

	// 重连到房间
	if err := h.roomManager.ReconnectPlayer(restored.PlayerID, restored.RoomCode, client); err != nil {
		log.Printf("重连到房间失败: %v", err)
		client.SetRoom("")
		return
	}

	payload.RoomCode = restored.RoomCode

	// 如果游戏正在进行，恢复游戏状态
	if gameSession := h.GetGameSession(restored.RoomCode); gameSession != nil {
		h.sessionManager.SetOnline(restored.PlayerID)
		gameSession.PlayerOnline(restored.PlayerID)
		payload.GameState = gameSession.BuildGameStateDTO(restored.PlayerID, h.sessionManager)
	}
}
