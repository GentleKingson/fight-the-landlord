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
	attempt := newReconnectAttempt(client)
	defer attempt.cleanup(client)
	if h.metrics != nil {
		h.metrics.ReconnectAttempt()
	}
	payload, parsed := h.parseReconnectPayload(client, msg)
	if !parsed || !h.validateReconnectClientIsUnbound(client) {
		return
	}
	unlockAuthority, acquired := h.acquireReconnectAuthority(client, attempt)
	if !acquired {
		return
	}
	defer unlockAuthority()

	temporaryID := client.GetID()
	temporaryName := client.GetName()
	restored, webSessionTicket, ok := h.restoreReconnectSession(client, payload, temporaryID)
	if !ok {
		return
	}
	previous, ok := h.rebindRestoredClient(client, restored, temporaryID, webSessionTicket)
	if !ok {
		return
	}
	if !h.validateCurrentBrowserRestore(
		client, previous, restored, temporaryID, temporaryName, webSessionTicket,
	) {
		return
	}
	if !h.deliverReconnectState(
		client, previous, restored, temporaryID, temporaryName, webSessionTicket,
	) {
		return
	}
	h.finishReconnectSuccess(client, previous, restored, attempt)
}

type reconnectAttempt struct {
	webClient types.WebSessionClient
	browser   bool
	succeeded bool
}

func newReconnectAttempt(client types.ClientInterface) *reconnectAttempt {
	webClient, browser := client.(types.WebSessionClient)
	if !browser || !webClient.IsBrowserTransport() {
		return &reconnectAttempt{}
	}
	return &reconnectAttempt{webClient: webClient, browser: true}
}

func (attempt *reconnectAttempt) cleanup(client types.ClientInterface) {
	if !attempt.browser || attempt.succeeded {
		return
	}
	attempt.webClient.InvalidateProvisionalWebSessionTicket()
	client.Close()
}

func (h *Handler) parseReconnectPayload(
	client types.ClientInterface,
	msg *protocol.Message,
) (*protocol.ReconnectPayload, bool) {
	payload, err := codec.ParsePayload[protocol.ReconnectPayload](msg)
	if err == nil {
		return payload, true
	}
	if h.metrics != nil {
		h.metrics.ReconnectFailure("decode")
	}
	sendMessage(client, codec.NewCommandErrorMessage(protocol.ErrCodeInvalidMsg, protocol.MsgReconnect))
	return nil, false
}

func (h *Handler) validateReconnectClientIsUnbound(client types.ClientInterface) bool {
	if client.GetRoom() == "" {
		return true
	}
	if h.metrics != nil {
		h.metrics.ReconnectFailure("already_bound")
	}
	sendMessage(client, codec.NewCommandErrorMessageWithText(
		protocol.ErrCodeReconnectInvalid,
		"当前连接已加入房间，无法恢复其他身份",
		protocol.MsgReconnect,
	))
	return false
}

func (h *Handler) acquireReconnectAuthority(
	client types.ClientInterface,
	attempt *reconnectAttempt,
) (func(), bool) {
	if !attempt.browser {
		return types.LockSessionAuthority(h.server), true
	}
	unlock, acquired := types.AcquireBrowserReconnectAuthority(
		h.server, attempt.webClient.BrowserReconnectToken(), client,
	)
	if acquired {
		return unlock, true
	}
	if h.metrics != nil {
		h.metrics.ReconnectFailure("authority_race")
	}
	sendMessage(client, codec.NewCommandErrorMessageWithText(
		protocol.ErrCodeReconnectInvalid,
		"Web 会话状态已变化，请重新连接",
		protocol.MsgReconnect,
	))
	return func() {}, false
}

func (h *Handler) validateCurrentBrowserRestore(
	client, previous types.ClientInterface,
	restored *session.RestoredSession,
	temporaryID, temporaryName, webSessionTicket string,
) bool {
	if webSessionTicket == "" || h.sessionManager.IsCurrentBrowserRestore(restored) {
		return true
	}
	if h.metrics != nil {
		h.metrics.ReconnectFailure("superseded")
	}
	h.rollbackReconnect(client, previous, restored, temporaryID, temporaryName, webSessionTicket)
	return false
}

func (h *Handler) deliverReconnectState(
	client, previous types.ClientInterface,
	restored *session.RestoredSession,
	temporaryID, temporaryName, webSessionTicket string,
) bool {
	payload := buildReconnectedPayload(restored, webSessionTicket)
	// A replaced connection may finish its disconnect cleanup concurrently.
	h.sessionManager.SetOnline(restored.PlayerID)
	restoredRoom, responseSent, restoreErr := h.restoreReconnectRoom(client, restored, &payload)
	if errors.Is(restoreErr, room.ErrReconnectResponseDelivery) {
		h.failReconnectDelivery(client, previous, restored, temporaryID, temporaryName, webSessionTicket)
		return false
	}
	if restoredRoom == nil {
		return h.deliverRoomlessReconnect(
			client, previous, restored, temporaryID, temporaryName, webSessionTicket, &payload,
		)
	}
	if responseSent {
		return true
	}
	if h.metrics != nil {
		h.metrics.ReconnectFailure("snapshot_skipped")
	}
	h.rollbackRestoredCredential(client, restored, webSessionTicket)
	h.closeSkippedReconnect(client, previous, restored.PlayerID)
	return false
}

func buildReconnectedPayload(
	restored *session.RestoredSession,
	webSessionTicket string,
) protocol.ReconnectedPayload {
	payload := protocol.ReconnectedPayload{
		PlayerID:         restored.PlayerID,
		PlayerName:       restored.PlayerName,
		WebSessionTicket: webSessionTicket,
	}
	if webSessionTicket == "" {
		payload.ReconnectToken = restored.ReconnectToken
	}
	return payload
}

func (h *Handler) deliverRoomlessReconnect(
	client, previous types.ClientInterface,
	restored *session.RestoredSession,
	temporaryID, temporaryName, webSessionTicket string,
	payload *protocol.ReconnectedPayload,
) bool {
	sent, err := h.sendReconnected(client, restored, nil, payload)
	if err == nil && sent {
		return true
	}
	h.failReconnectDelivery(client, previous, restored, temporaryID, temporaryName, webSessionTicket)
	return false
}

func (h *Handler) failReconnectDelivery(
	client, previous types.ClientInterface,
	restored *session.RestoredSession,
	temporaryID, temporaryName, webSessionTicket string,
) {
	if h.metrics != nil {
		h.metrics.ReconnectFailure("delivery")
	}
	h.rollbackReconnect(client, previous, restored, temporaryID, temporaryName, webSessionTicket)
}

func (h *Handler) finishReconnectSuccess(
	client, previous types.ClientInterface,
	restored *session.RestoredSession,
	attempt *reconnectAttempt,
) {
	if h.matcher != nil && previous != nil && previous != client {
		h.matcher.ReplaceClient(previous, client)
	}
	if previous != nil && previous != client {
		previous.Close()
		types.RetireBrowserSessionClient(h.server, restored.PlayerID, previous)
	}
	if attempt.browser {
		attempt.succeeded = true
		attempt.webClient.DiscardProvisionalWebSessionTicket()
	}

	log.Printf("🔄 玩家 %s (%s) 重连成功", restored.PlayerName, restored.PlayerID)
	if h.metrics != nil {
		h.metrics.ReconnectSuccess()
	}
}

func (h *Handler) restoreReconnectSession(
	client types.ClientInterface,
	payload *protocol.ReconnectPayload,
	temporaryID string,
) (*session.RestoredSession, string, bool) {
	restored, webClient, browserTransport, err := h.restoreReconnectCredential(client, payload, temporaryID)
	if err == nil {
		if playerBanned(h.server, restored.PlayerID) {
			h.rollbackRestoredSession(restored)
			if h.metrics != nil {
				h.metrics.ReconnectFailure("invalid")
			}
			sendMessage(client, codec.NewCommandErrorMessageWithText(
				protocol.ErrCodeReconnectInvalid,
				"该玩家已被暂时封禁",
				protocol.MsgReconnect,
			))
			client.Close()
			return nil, "", false
		}
		if !browserTransport {
			return restored, "", true
		}
		return h.issueBrowserReconnectTicket(client, webClient, restored)
	}
	h.rejectReconnectCredential(client, err)
	return nil, "", false
}

func (h *Handler) restoreReconnectCredential(
	client types.ClientInterface,
	payload *protocol.ReconnectPayload,
	temporaryID string,
) (*session.RestoredSession, types.WebSessionClient, bool, error) {
	webClient, browserTransport := client.(types.WebSessionClient)
	browserTransport = browserTransport && webClient.IsBrowserTransport()
	if !browserTransport {
		restored, err := h.sessionManager.RestoreSession(payload.Token, payload.PlayerID, temporaryID)
		return restored, nil, false, err
	}
	if payload.Token != "" || payload.PlayerID != "" || webClient.BrowserReconnectToken() == "" {
		return nil, webClient, true, session.ErrInvalidReconnect
	}
	restored, err := h.sessionManager.RestoreSessionByToken(webClient.BrowserReconnectToken(), temporaryID)
	return restored, webClient, true, err
}

func (h *Handler) issueBrowserReconnectTicket(
	client types.ClientInterface,
	webClient types.WebSessionClient,
	restored *session.RestoredSession,
) (*session.RestoredSession, string, bool) {
	ticket, err := webClient.IssueWebSessionTicket(
		restored.ReconnectToken,
		webClient.BrowserReconnectToken(),
		func() bool { return h.rollbackRestoredSession(restored) },
		func() bool { return h.sessionManager.OrphanBrowserRestore(restored) },
	)
	if err != nil {
		h.rollbackRestoredSession(restored)
		h.rejectBrowserReconnectTicket(client, true)
		return nil, "", false
	}
	if webClient.TrackWebSessionTicket(ticket) {
		return restored, ticket, true
	}
	webClient.InvalidateWebSessionTicket(ticket)
	h.rejectBrowserReconnectTicket(client, false)
	return nil, "", false
}

func (h *Handler) rejectBrowserReconnectTicket(client types.ClientInterface, sendError bool) {
	if h.metrics != nil {
		h.metrics.ReconnectFailure("ticket")
	}
	if sendError {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeUnknown, "Web 会话确认票据创建失败", protocol.MsgReconnect,
		))
	}
	client.Close()
}

func (h *Handler) rejectReconnectCredential(client types.ClientInterface, err error) {
	code := protocol.ErrCodeReconnectInvalid
	if errors.Is(err, session.ErrReconnectExpired) {
		code = protocol.ErrCodeReconnectExpired
	}
	if h.metrics != nil {
		reason := "invalid"
		if errors.Is(err, session.ErrReconnectExpired) {
			reason = "expired"
		}
		h.metrics.ReconnectFailure(reason)
	}
	sendMessage(client, codec.NewCommandErrorMessage(code, protocol.MsgReconnect))
}

func (h *Handler) rebindRestoredClient(
	client types.ClientInterface,
	restored *session.RestoredSession,
	temporaryID string,
	webSessionTicket string,
) (types.ClientInterface, bool) {
	previous, err := h.server.RebindClient(
		temporaryID, restored.PlayerID, restored.PlayerName, restored.RoomCode, client,
	)
	if err == nil {
		return previous, true
	}
	h.rollbackRestoredCredential(client, restored, webSessionTicket)
	if h.metrics != nil {
		h.metrics.ReconnectFailure("rebind")
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
) (*room.Room, bool, error) {
	if restored.RoomCode == "" || h.roomManager == nil {
		return nil, false, nil
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
		return restoredRoom, responseSent, nil
	}
	log.Printf("重连到房间失败: %v", err)
	if errors.Is(err, room.ErrReconnectResponseDelivery) {
		return nil, false, err
	}
	types.CompareAndSetRoom(client, restored.PlayerID, restored.RoomCode, "")
	return nil, false, nil
}

func (h *Handler) rollbackReconnect(
	client, previous types.ClientInterface,
	restored *session.RestoredSession,
	temporaryID, temporaryName, webSessionTicket string,
) {
	rebindErr := h.server.RollbackRebindClient(
		temporaryID, temporaryName, restored.PlayerID, restored.RoomCode, client, previous,
	)
	h.rollbackRestoredCredential(client, restored, webSessionTicket)
	if rebindErr != nil {
		log.Printf("重连身份回滚失败: %v", rebindErr)
		h.sessionManager.SetOffline(restored.PlayerID)
		client.Close()
		return
	}
	client.Close()
}

func (h *Handler) rollbackRestoredCredential(
	client types.ClientInterface,
	restored *session.RestoredSession,
	webSessionTicket string,
) {
	if webSessionTicket != "" {
		invalidateWebSessionTicket(client, webSessionTicket)
		return
	}
	h.rollbackRestoredSession(restored)
}

func (h *Handler) rollbackRestoredSession(restored *session.RestoredSession) bool {
	if h.sessionManager.RollbackRestore(restored) {
		return true
	}
	log.Printf("重连凭据回滚失败: player=%s", restored.PlayerID)
	h.sessionManager.SetOffline(restored.PlayerID)
	return false
}

func invalidateWebSessionTicket(client types.ClientInterface, ticket string) {
	if ticket == "" {
		return
	}
	if webClient, ok := client.(types.WebSessionClient); ok {
		webClient.InvalidateWebSessionTicket(ticket)
	}
}

func (h *Handler) closeSkippedReconnect(
	client types.ClientInterface,
	previous types.ClientInterface,
	playerID string,
) {
	log.Printf("玩家 %s 的重连快照因身份已变化而跳过", playerID)
	if previous != nil && previous != client {
		previous.Close()
		types.RetireBrowserSessionClient(h.server, playerID, previous)
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
func (h *Handler) sendReconnected(client types.ClientInterface, restored *session.RestoredSession, expectedRoom *room.Room, payload *protocol.ReconnectedPayload) (bool, error) {
	if expectedRoom != nil && h.roomManager != nil {
		sent, err := h.roomManager.SendBuiltIfCurrentMember(expectedRoom, restored.PlayerID, client, func() *protocol.Message {
			return h.buildReconnectedMessage(client, restored, expectedRoom, payload)
		})
		if err != nil {
			log.Printf("发送重连状态给玩家 %s 失败: %v", restored.PlayerID, err)
		}
		if sent {
			return true, nil
		}
		return false, err
	}
	types.CompareAndSetRoom(client, restored.PlayerID, restored.RoomCode, "")
	message := h.buildReconnectedMessage(client, restored, nil, payload)
	sent, err := types.SendCommandResultIfIdentity(client, restored.PlayerID, "", message)
	if err != nil {
		log.Printf("发送重连状态给玩家 %s 失败: %v", restored.PlayerID, err)
		return false, err
	}
	return sent, nil
}
