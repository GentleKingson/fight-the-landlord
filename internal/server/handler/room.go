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
		client.SendMessage(codec.NewRequestError(protocol.MsgCreateRoom,
			protocol.ErrCodeServerMaintenance, "服务器维护中，暂停创建房间"))
		return
	}

	// 如果已在房间中，先离开
	if !h.leaveWaitingRoom(client, protocol.MsgCreateRoom) {
		return
	}

	room, err := h.roomManager.CreateRoom(client)
	if err != nil {
		client.SendMessage(codec.NewRequestError(protocol.MsgCreateRoom, protocol.ErrCodeUnknown, err.Error()))
		return
	}

	if room == nil {
		client.SendMessage(codec.NewRequestError(protocol.MsgCreateRoom, protocol.ErrCodeUnknown, "创建房间失败"))
		return
	}

	client.SendMessage(codec.MustNewMessage(protocol.MsgRoomCreated, room.BuildRoomCreatedPayload(client.GetID())))
}

// handleJoinRoom 处理加入房间
func (h *Handler) handleJoinRoom(client types.ClientInterface, msg *protocol.Message) {
	// 维护模式检查
	if h.server.IsMaintenanceMode() {
		client.SendMessage(codec.NewRequestError(protocol.MsgJoinRoom,
			protocol.ErrCodeServerMaintenance, "服务器维护中，暂停加入房间"))
		return
	}

	payload, err := codec.ParsePayload[protocol.JoinRoomPayload](msg)
	if err != nil {
		client.SendMessage(codec.NewRequestError(protocol.MsgJoinRoom, protocol.ErrCodeInvalidMsg, protocol.ErrorMessages[protocol.ErrCodeInvalidMsg]))
		return
	}

	// 如果已在房间中，先离开
	if !h.leaveWaitingRoom(client, protocol.MsgJoinRoom) {
		return
	}

	room, err := h.roomManager.JoinRoom(client, payload.RoomCode)
	if err != nil {
		rejectRoomAction(client, protocol.MsgJoinRoom, err)
		return
	}

	if room == nil {
		client.SendMessage(codec.NewRequestError(protocol.MsgJoinRoom, protocol.ErrCodeUnknown, "加入房间失败"))
		return
	}

	client.SendMessage(codec.MustNewMessage(protocol.MsgRoomJoined, room.BuildRoomJoinedPayload(client.GetID())))
}

// handleLeaveRoom 处理离开房间
func (h *Handler) handleLeaveRoom(client types.ClientInterface) {
	h.leaveWaitingRoom(client, protocol.MsgLeaveRoom)
}

func (h *Handler) leaveWaitingRoom(client types.ClientInterface, request protocol.MessageType) bool {
	if err := h.roomManager.LeaveRoom(client); err != nil {
		client.SendMessage(codec.NewRequestError(request, protocol.ErrCodeGameStarted, err.Error()))
		return false
	}
	return true
}

// handleQuickMatch 处理快速匹配
func (h *Handler) handleQuickMatch(client types.ClientInterface) {
	// 维护模式检查
	if h.server.IsMaintenanceMode() {
		client.SendMessage(codec.NewErrorMessageWithText(
			protocol.ErrCodeServerMaintenance, "服务器维护中，暂停快速匹配"))
		return
	}

	// 如果已在房间中，先离开
	if !h.leaveWaitingRoom(client, protocol.MsgQuickMatch) {
		return
	}

	h.matcher.AddToQueue(client)
}

// handlePracticeMatch 处理人机练习
func (h *Handler) handlePracticeMatch(client types.ClientInterface) {
	if h.server.IsMaintenanceMode() {
		client.SendMessage(codec.NewErrorMessageWithText(
			protocol.ErrCodeServerMaintenance, "服务器维护中，暂停人机练习"))
		return
	}

	if client.GetRoom() != "" {
		client.SendMessage(codec.NewErrorMessageWithText(protocol.ErrCodeUnknown, "已在房间中，请先离开当前房间"))
		return
	}

	h.matcher.PracticeMatch(client)
}

// handleReady 处理准备
func (h *Handler) handleReady(client types.ClientInterface, msg *protocol.Message) {
	if err := h.roomManager.SetPlayerReady(client, msg.Type == protocol.MsgReady); err != nil {
		rejectRoomAction(client, msg.Type, err)
	}
}

// handleStartGame 处理房主开始游戏（不满 3 人自动补齐人机）
func (h *Handler) handleStartGame(client types.ClientInterface) {
	if h.server != nil && h.server.IsMaintenanceMode() {
		client.SendMessage(codec.NewRequestError(protocol.MsgStartGame,
			protocol.ErrCodeServerMaintenance, "服务器维护中，暂停开始游戏"))
		return
	}
	if err := h.roomManager.StartRoom(client); err != nil {
		rejectRoomAction(client, protocol.MsgStartGame, err)
	}
}

func rejectRoomAction(client types.ClientInterface, request protocol.MessageType, err error) {
	code := protocol.ErrCodeUnknown
	var gameErr *apperrors.GameError
	if errors.As(err, &gameErr) {
		code = gameErr.Code
	}
	client.SendMessage(codec.NewRequestError(request, code, err.Error()))
}
