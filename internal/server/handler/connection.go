package handler

import (
	"errors"
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
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

	// Do not abandon an existing game by changing identity on this socket.
	if client.GetRoom() != "" && client.GetID() != payload.PlayerID {
		client.SendMessage(codec.NewErrorMessage(protocol.ErrCodeGameStarted))
		return
	}
	valid := false
	if h.authenticate != nil {
		name, err := h.authenticate(payload.PlayerID, payload.Token)
		if err != nil && !errors.Is(err, storage.ErrInvalidIdentity) {
			client.SendMessage(codec.NewErrorMessage(protocol.ErrCodeIdentityUnavailable))
			client.Close()
			return
		}
		if err == nil {
			h.sessionManager.EnsureSession(payload.PlayerID, name)
			valid = true
		}
	} else {
		valid = h.sessionManager.CanReconnect(payload.Token, payload.PlayerID)
	}
	if !valid {
		client.SendMessage(codec.NewErrorMessage(protocol.ErrCodeIdentityInvalid))
		return
	}

	h.server.RestoreClient(payload.PlayerID, client, func() {
		h.RestoreConnection(client, payload.PlayerID)
	})
}

// RestoreConnection runs inside the server connection lifecycle lock.
func (h *Handler) RestoreConnection(client types.ClientInterface, playerID string) {
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

	if session.RoomCode == "" && session.LastRoomCode != "" {
		if r := h.roomManager.GetRoom(session.LastRoomCode); r != nil && r.GetState() == room.RoomStateEnded {
			session.RoomCode = session.LastRoomCode
		}
	}
	// 如果在房间中，尝试恢复房间信息
	if session.RoomCode != "" && h.tryRestoreRoomState(client, session, &reconnectPayload) {
		return // Active-game snapshot and turn have already been enqueued under the game lock.
	}

	// 发送重连成功消息
	client.SendMessage(codec.MustNewMessage(protocol.MsgReconnected, reconnectPayload))

	if reconnectPayload.RoomCode != "" && reconnectPayload.GameState == nil {
		if r := h.roomManager.GetRoom(reconnectPayload.RoomCode); r != nil {
			client.SendMessage(codec.MustNewMessage(protocol.MsgRoomJoined, r.BuildRoomJoinedPayload(client.GetID())))
		}
	}

	log.Printf("🔄 玩家 %s (%s) 重连成功", session.PlayerName, session.PlayerID)
}

// tryRestoreRoomState 尝试恢复房间状态
func (h *Handler) tryRestoreRoomState(client types.ClientInterface, saved *session.PlayerSession, payload *protocol.ReconnectedPayload) bool {
	room := h.roomManager.GetRoom(saved.RoomCode)
	if room == nil {
		// A fresh socket has no local RoomID yet; clear the stale association
		// using the room from the session snapshot rather than its empty ID.
		h.sessionManager.ClearRoomIfCurrent(saved.PlayerID, saved.RoomCode)
		client.SetRoom("")
		return false
	}

	client.SetRoom(saved.RoomCode)
	// 重连到房间
	var err error
	if gameSession := h.GetGameSession(saved.RoomCode); gameSession != nil {
		err = gameSession.ReconnectClient(h.roomManager, client, h.sessionManager, payload)
		if err == nil {
			return true
		}
	} else {
		err = h.roomManager.ReconnectPlayer(client, client)
	}
	if err != nil {
		log.Printf("重连到房间失败: %v", err)
		if errors.Is(err, session.ErrPersistenceUnavailable) {
			client.SendMessage(codec.NewErrorMessageWithText(protocol.ErrCodeUnknown, err.Error()))
			// Preserve the association, but let the client's existing reconnect
			// loop retry both room replacement and snapshot delivery.
			client.Close()
			return true
		}
		client.SetRoom("")
		return false
	}

	client.SetRoom(saved.RoomCode)
	payload.RoomCode = saved.RoomCode

	return false
}
