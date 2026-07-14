package handler

import (
	"errors"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
)

// sendGameError 统一处理游戏错误并发送给客户端。
func sendGameError(client types.ClientInterface, command protocol.MessageType, err error) {
	if gameErr, ok := errors.AsType[*apperrors.GameError](err); ok {
		client.SendMessage(codec.NewCommandErrorMessage(gameErr.Code, command))
	} else {
		client.SendMessage(codec.NewCommandErrorMessageWithText(protocol.ErrCodeUnknown, err.Error(), command))
	}
}

// handleBid 处理叫地主
func (h *Handler) handleBid(client types.ClientInterface, msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.BidPayload](msg)
	if err != nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeInvalidMsg, protocol.MsgBid))
		return
	}

	if h.roomManager == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeGameNotStart, protocol.MsgBid))
		return
	}

	room := h.roomManager.GetRoom(client.GetRoom())
	if room == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeNotInRoom, protocol.MsgBid))
		return
	}

	gameSession := h.GetGameSession(room.Code)
	if gameSession == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeGameNotStart, protocol.MsgBid))
		return
	}

	if err := gameSession.HandleBid(client.GetID(), payload.Bid); err != nil {
		sendGameError(client, protocol.MsgBid, err)
	}
}

// handlePlayCards 处理出牌
func (h *Handler) handlePlayCards(client types.ClientInterface, msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.PlayCardsPayload](msg)
	if err != nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeInvalidMsg, protocol.MsgPlayCards))
		return
	}

	if h.roomManager == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeGameNotStart, protocol.MsgPlayCards))
		return
	}

	room := h.roomManager.GetRoom(client.GetRoom())
	if room == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeNotInRoom, protocol.MsgPlayCards))
		return
	}

	gameSession := h.GetGameSession(room.Code)
	if gameSession == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeGameNotStart, protocol.MsgPlayCards))
		return
	}

	if err := gameSession.HandlePlayCards(client.GetID(), payload.Cards); err != nil {
		sendGameError(client, protocol.MsgPlayCards, err)
	}
}

// handlePass 处理不出
func (h *Handler) handlePass(client types.ClientInterface) {
	if h.roomManager == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeGameNotStart, protocol.MsgPass))
		return
	}

	room := h.roomManager.GetRoom(client.GetRoom())
	if room == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeNotInRoom, protocol.MsgPass))
		return
	}

	gameSession := h.GetGameSession(room.Code)
	if gameSession == nil {
		client.SendMessage(codec.NewCommandErrorMessage(protocol.ErrCodeGameNotStart, protocol.MsgPass))
		return
	}

	if err := gameSession.HandlePass(client.GetID()); err != nil {
		sendGameError(client, protocol.MsgPass, err)
	}
}
