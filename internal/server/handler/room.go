package handler

import (
	"errors"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
)

// handleCreateRoom 处理创建房间
func (h *Handler) handleCreateRoom(client types.ClientInterface) {
	// 维护模式检查
	if h.server.IsMaintenanceMode() {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeServerMaintenance, "服务器维护中，暂停创建房间", protocol.MsgCreateRoom))
		return
	}

	if !h.leaveRoomBeforeCommand(client, protocol.MsgCreateRoom) {
		return
	}

	room, err := h.roomManager.CreateRoomWithResponse(client)
	if err != nil {
		sendMessage(client, codec.NewCommandErrorMessageWithText(protocol.ErrCodeUnknown, err.Error(), protocol.MsgCreateRoom))
		return
	}

	if room == nil {
		sendMessage(client, codec.NewCommandErrorMessageWithText(protocol.ErrCodeUnknown, "创建房间失败", protocol.MsgCreateRoom))
		return
	}
}

// handleJoinRoom 处理加入房间
func (h *Handler) handleJoinRoom(client types.ClientInterface, msg *protocol.Message) {
	// 维护模式检查
	if h.server.IsMaintenanceMode() {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeServerMaintenance, "服务器维护中，暂停加入房间", protocol.MsgJoinRoom))
		return
	}

	payload, err := codec.ParsePayload[protocol.JoinRoomPayload](msg)
	if err != nil {
		sendMessage(client, codec.NewCommandErrorMessage(protocol.ErrCodeInvalidMsg, protocol.MsgJoinRoom))
		return
	}

	if !h.leaveRoomBeforeCommand(client, protocol.MsgJoinRoom) {
		return
	}

	room, err := h.roomManager.JoinRoomWithResponse(client, payload.RoomCode)
	if err != nil {
		var gameErr *apperrors.GameError
		if errors.As(err, &gameErr) {
			sendMessage(client, codec.NewCommandErrorMessage(gameErr.Code, protocol.MsgJoinRoom))
		} else {
			sendMessage(client, codec.NewCommandErrorMessageWithText(protocol.ErrCodeUnknown, err.Error(), protocol.MsgJoinRoom))
		}
		return
	}

	if room == nil {
		sendMessage(client, codec.NewCommandErrorMessageWithText(protocol.ErrCodeUnknown, "加入房间失败", protocol.MsgJoinRoom))
		return
	}
}

// handleLeaveRoom 处理离开房间
func (h *Handler) handleLeaveRoom(client types.ClientInterface) {
	if h.roomManager == nil || client.GetRoom() == "" {
		sendMessage(client, codec.NewCommandErrorMessage(protocol.ErrCodeNotInRoom, protocol.MsgLeaveRoom))
		return
	}

	roomCode := client.GetRoom()
	playerID := client.GetID()
	if !h.roomManager.LeaveRoom(client) || client.GetRoom() != "" {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeUnknown, "离开房间失败", protocol.MsgLeaveRoom))
		return
	}

	_, _ = types.SendMessageIfIdentity(
		client,
		playerID,
		"",
		codec.MustNewMessage(protocol.MsgRoomLeft, protocol.RoomLeftPayload{RoomCode: roomCode}),
	)
}

func (h *Handler) leaveRoomBeforeCommand(client types.ClientInterface, command protocol.MessageType) bool {
	if client.GetRoom() == "" {
		return true
	}
	if h.roomManager != nil && h.roomManager.LeaveRoom(client) {
		return true
	}
	sendMessage(client, codec.NewCommandErrorMessageWithText(
		protocol.ErrCodeGameStarted,
		"无法离开当前房间",
		command,
	))
	return false
}

// handleQuickMatch 处理快速匹配
func (h *Handler) handleQuickMatch(client types.ClientInterface) {
	// 维护模式检查
	if h.server.IsMaintenanceMode() {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeServerMaintenance, "服务器维护中，暂停快速匹配", protocol.MsgQuickMatch))
		return
	}

	if !h.leaveRoomBeforeCommand(client, protocol.MsgQuickMatch) {
		return
	}

	if h.matcher == nil {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeUnknown, "匹配服务暂不可用", protocol.MsgQuickMatch))
		return
	}

	accepted := h.matcher.AddToQueue(client)
	if !accepted {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeUnknown, "已在匹配队列中", protocol.MsgQuickMatch))
		return
	}
}

// handlePracticeMatch 处理人机练习
func (h *Handler) handlePracticeMatch(client types.ClientInterface) {
	if h.server.IsMaintenanceMode() {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeServerMaintenance, "服务器维护中，暂停人机练习", protocol.MsgPracticeMatch))
		return
	}

	if !h.leaveRoomBeforeCommand(client, protocol.MsgPracticeMatch) {
		return
	}

	if h.matcher == nil {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeUnknown, "匹配服务暂不可用", protocol.MsgPracticeMatch))
		return
	}

	if !h.matcher.PracticeMatch(client) {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeUnknown, "已在匹配中", protocol.MsgPracticeMatch))
	}
}

// handleCancelMatch 处理取消匹配。
func (h *Handler) handleCancelMatch(client types.ClientInterface) {
	if h.matcher == nil {
		sendMessage(client, codec.NewCommandErrorMessageWithText(
			protocol.ErrCodeUnknown, "匹配服务暂不可用", protocol.MsgCancelMatch))
		return
	}

	if !h.matcher.RemoveFromQueue(client) {
		sendMessage(client, codec.NewCommandErrorMessage(protocol.ErrCodeMatchNotQueued, protocol.MsgCancelMatch))
		return
	}

	sendMessage(client, codec.MustNewMessage(protocol.MsgMatchCancelled, protocol.MatchCancelledPayload{
		Reason: protocol.MatchCancelReason,
	}))
}

// handleReady 处理准备
func (h *Handler) handleReady(client types.ClientInterface, ready bool) {
	command := protocol.MsgReady
	if !ready {
		command = protocol.MsgCancelReady
	}
	if h.roomManager == nil {
		sendMessage(client, codec.NewCommandErrorMessage(protocol.ErrCodeNotInRoom, command))
		return
	}

	err := h.roomManager.SetPlayerReady(client, ready)
	if err != nil {
		var gameErr *apperrors.GameError
		if errors.As(err, &gameErr) {
			sendMessage(client, codec.NewCommandErrorMessage(gameErr.Code, command))
		} else {
			sendMessage(client, codec.NewCommandErrorMessageWithText(protocol.ErrCodeUnknown, err.Error(), command))
		}
	}
}
