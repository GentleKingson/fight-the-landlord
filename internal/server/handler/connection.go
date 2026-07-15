package handler

import (
	"errors"
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// handlePing 处理心跳消息
func (h *Handler) handlePing(client types.ClientInterface, msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.PingPayload](msg)
	if err != nil {
		sendMessage(client, codec.NewCommandErrorMessage(protocol.ErrCodeInvalidMsg, protocol.MsgPing))
		return
	}

	// 立即回复 pong
	sendMessage(client, codec.MustNewMessage(protocol.MsgPong, protocol.PongPayload{
		ClientTimestamp: payload.Timestamp,
		ServerTimestamp: time.Now().UnixMilli(),
	}))
}

// handleReconnect 处理断线重连
func (h *Handler) handleReconnect(client types.ClientInterface, msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.ReconnectPayload](msg)
	if err != nil {
		sendMessage(client, codec.NewCommandErrorMessage(protocol.ErrCodeInvalidMsg, protocol.MsgReconnect))
		return
	}

	// Rebinding a connection that already owns a room membership would leave
	// that room holding the same physical Client under the old logical player.
	// Reject before consuming either reconnect credential; clients must leave
	// their provisional room before restoring another identity.
	if client.GetRoom() != "" {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeReconnectInvalid,
			"当前连接已加入房间，无法恢复其他身份",
			protocol.MsgReconnect,
		))
		return
	}

	temporaryID := client.GetID()
	restored, ok := h.restoreReconnectSession(client, payload, temporaryID)
	if !ok {
		return
	}
	previous, ok := h.rebindRestoredClient(client, restored, temporaryID)
	if !ok {
		return
	}
	// 构建重连响应
	reconnectPayload := protocol.ReconnectedPayload{
		PlayerID:       restored.PlayerID,
		PlayerName:     restored.PlayerName,
		ReconnectToken: restored.ReconnectToken,
	}

	// A replaced connection may finish its disconnect cleanup concurrently.
	// Reassert online status after identity migration; exact room presence is
	// updated inside the room publication transaction below.
	h.sessionManager.SetOnline(restored.PlayerID)

	restoredRoom, responseSent := h.restoreReconnectRoom(client, restored, &reconnectPayload)
	if restoredRoom == nil {
		h.sendReconnected(client, restored, nil, &reconnectPayload)
	} else if !responseSent {
		h.closeSkippedReconnect(client, previous, restored.PlayerID)
		return
	}

	// Publish the rotated identity and reconnect token before migrating queued
	// matcher work. ReplaceClient may immediately enqueue MatchQueued or let an
	// inflight match publish RoomJoined; neither may overtake Reconnected.
	if h.matcher != nil && previous != nil && previous != client {
		h.matcher.ReplaceClient(previous, client)
	}
	if previous != nil && previous != client {
		previous.Close()
	}

	log.Printf("🔄 玩家 %s (%s) 重连成功", restored.PlayerName, restored.PlayerID)
}

func (h *Handler) restoreReconnectSession(
	client types.ClientInterface,
	payload *protocol.ReconnectPayload,
	temporaryID string,
) (*session.RestoredSession, bool) {
	restored, err := h.sessionManager.RestoreSession(payload.Token, payload.PlayerID, temporaryID)
	if err == nil {
		return restored, true
	}
	code := protocol.ErrCodeReconnectInvalid
	if errors.Is(err, session.ErrReconnectExpired) {
		code = protocol.ErrCodeReconnectExpired
	}
	sendMessage(client, codec.NewCommandErrorMessage(code, protocol.MsgReconnect))
	return nil, false
}

func (h *Handler) rebindRestoredClient(
	client types.ClientInterface,
	restored *session.RestoredSession,
	temporaryID string,
) (types.ClientInterface, bool) {
	previous, err := h.server.RebindClient(
		temporaryID, restored.PlayerID, restored.PlayerName, restored.RoomCode, client,
	)
	if err == nil {
		return previous, true
	}
	if !h.sessionManager.RollbackRestore(restored) {
		h.sessionManager.SetOffline(restored.PlayerID)
	}
	log.Printf("重连身份绑定失败: %v", err)
	sendMessage(client, codec.NewCommandErrorMessageWithText(
		protocol.ErrCodeUnknown, "重连身份恢复失败", protocol.MsgReconnect,
	))
	client.Close()
	return nil, false
}

func (h *Handler) restoreReconnectRoom(
	client types.ClientInterface,
	restored *session.RestoredSession,
	payload *protocol.ReconnectedPayload,
) (*room.Room, bool) {
	if restored.RoomCode == "" || h.roomManager == nil {
		return nil, false
	}
	restoredRoom, responseSent, err := h.roomManager.ReconnectPlayerWithResponse(
		restored.PlayerID,
		restored.RoomCode,
		client,
		func(gameRoom *room.Room) *protocol.Message {
			return h.buildReconnectedMessage(client, restored, gameRoom, payload)
		},
	)
	if err == nil {
		return restoredRoom, responseSent
	}
	log.Printf("重连到房间失败: %v", err)
	types.CompareAndSetRoom(client, restored.PlayerID, restored.RoomCode, "")
	return nil, false
}

func (h *Handler) closeSkippedReconnect(
	client types.ClientInterface,
	previous types.ClientInterface,
	playerID string,
) {
	log.Printf("玩家 %s 的重连快照因身份已变化而跳过", playerID)
	if previous != nil && previous != client {
		previous.Close()
		if h.matcher != nil {
			h.matcher.PlayerDisconnected(previous)
		}
	}
	client.Close()
}

// tryRestoreRoomState 尝试恢复房间状态
func (h *Handler) tryRestoreRoomState(client types.ClientInterface, restored *session.RestoredSession) *room.Room {
	gameRoom := h.roomManager.GetRoom(restored.RoomCode)
	if gameRoom == nil {
		types.CompareAndSetRoom(client, restored.PlayerID, restored.RoomCode, "")
		return nil
	}

	// 重连到房间
	if err := h.roomManager.ReconnectPlayer(restored.PlayerID, restored.RoomCode, client); err != nil {
		log.Printf("重连到房间失败: %v", err)
		types.CompareAndSetRoom(client, restored.PlayerID, restored.RoomCode, "")
		return nil
	}
	return gameRoom
}

// buildReconnectedMessage runs inside the room publication boundary. A waiting
// room also gets a full roster snapshot, while an active game gets its private
// authoritative projection and exact event watermark.
func (h *Handler) buildReconnectedMessage(
	client types.ClientInterface,
	restored *session.RestoredSession,
	expectedRoom *room.Room,
	payload *protocol.ReconnectedPayload,
) *protocol.Message {
	h.gamesLifecycleMu.Lock()
	defer h.gamesLifecycleMu.Unlock()

	payload.RoomCode = ""
	payload.GameState = nil
	if expectedRoom != nil && expectedRoom.IsCurrentMember(restored.PlayerID, client) {
		payload.RoomCode = expectedRoom.Code
		payload.GameState = &protocol.GameStateDTO{
			Phase:        "waiting",
			Players:      expectedRoom.GetAllPlayersInfo(),
			ServerTimeMS: time.Now().UnixMilli(),
		}

		h.gamesMu.RLock()
		registration := h.games[expectedRoom.Code]
		h.gamesMu.RUnlock()
		if registration.room == expectedRoom && registration.session != nil && registration.session.RoomIdentity() == expectedRoom {
			_, _, gameMember := registration.session.CurrentGameContext(restored.PlayerID)
			if gameMember {
				payload.GameState = registration.session.BuildGameStateDTO(restored.PlayerID, h.sessionManager)
			}
		}
	}

	message := codec.MustNewMessage(protocol.MsgReconnected, *payload)
	message.Event = session.EventMetaFromGameStateDTO(payload.GameState)
	return message
}

// sendReconnected is retained for roomless recovery and focused tests. Room
// restores build and enqueue the snapshot under RoomManager.publishMu.
func (h *Handler) sendReconnected(client types.ClientInterface, restored *session.RestoredSession, expectedRoom *room.Room, payload *protocol.ReconnectedPayload) {
	if expectedRoom != nil && h.roomManager != nil {
		sent, err := h.roomManager.SendBuiltIfCurrentMember(expectedRoom, restored.PlayerID, client, func() *protocol.Message {
			return h.buildReconnectedMessage(client, restored, expectedRoom, payload)
		})
		if err != nil {
			log.Printf("发送重连状态给玩家 %s 失败: %v", restored.PlayerID, err)
		}
		if sent {
			return
		}
	}
	types.CompareAndSetRoom(client, restored.PlayerID, restored.RoomCode, "")
	message := h.buildReconnectedMessage(client, restored, nil, payload)
	if _, err := types.SendMessageIfIdentity(client, restored.PlayerID, "", message); err != nil {
		log.Printf("发送重连状态给玩家 %s 失败: %v", restored.PlayerID, err)
	}
}
