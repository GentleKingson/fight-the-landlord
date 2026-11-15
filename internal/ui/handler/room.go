package handler

import (
	tea "charm.land/bubbletea/v2"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	payloadconv "github.com/palemoky/fight-the-landlord/internal/protocol/convert/payload"
	"github.com/palemoky/fight-the-landlord/internal/ui/model"
)

func updateWaitingInputPrompt(m model.Model) {
	state := m.Game().State()
	isOwner := state.OwnerID != "" && state.OwnerID == m.PlayerID()
	isReady := false
	for _, p := range state.Players {
		if p.ID == m.PlayerID() {
			isReady = p.Ready
			break
		}
	}

	switch {
	case isOwner && isReady:
		m.Input().Placeholder = "已准备 | 输入 S 开始游戏(补Bot) 或 C 取消准备"
	case isOwner:
		m.Input().Placeholder = "输入 S 开始游戏(不足3人补Bot) 或 R 准备"
	case isReady:
		m.Input().Placeholder = "已准备，等待其他玩家 (输入 C 取消准备)"
	default:
		m.Input().Placeholder = "输入 R 准备"
	}
	m.Input().Focus()
}

func handleMsgMatchCanceled(m model.Model, _ *protocol.Message) tea.Cmd {
	if m.Phase() == model.PhaseMatching {
		m.EnterLobby()
	}
	return nil
}

func handleMsgRoomCreated(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.RoomCreatedPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	state := m.Game().State()
	state.RoomCode = payload.RoomCode
	state.OwnerID = payload.OwnerID
	if state.OwnerID == "" {
		state.OwnerID = payload.Player.ID
	}
	state.Players = []protocol.PlayerInfo{payload.Player}
	m.SetPhase(model.PhaseWaiting)
	updateWaitingInputPrompt(m)
	return nil
}

func handleMsgRoomJoined(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.RoomJoinedPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	state := m.Game().State()
	state.RoomCode = payload.RoomCode
	state.OwnerID = payload.OwnerID
	state.Players = payload.Players
	m.SetPhase(model.PhaseWaiting)
	updateWaitingInputPrompt(m)
	m.PlaySound("join")
	return nil
}

func handleMsgPlayerJoined(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.PlayerJoinedPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	m.Game().State().Players = append(m.Game().State().Players, payload.Player)
	return nil
}

func handleMsgPlayerLeft(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.PlayerLeftPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	state := m.Game().State()
	players := state.Players
	for i, p := range players {
		if p.ID == payload.PlayerID {
			players = append(players[:i], players[i+1:]...)
			state.Players = players
			break
		}
	}
	if payload.OwnerID != "" {
		state.OwnerID = payload.OwnerID
	} else if payload.PlayerID == state.OwnerID {
		if len(state.Players) > 0 {
			state.OwnerID = state.Players[0].ID
		} else {
			state.OwnerID = ""
		}
	}
	if m.Phase() == model.PhaseWaiting {
		updateWaitingInputPrompt(m)
	}
	return nil
}

func handleMsgPlayerReady(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.PlayerReadyPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)

	// 更新玩家状态
	for i, p := range m.Game().State().Players {
		if p.ID != payload.PlayerID {
			continue
		}
		m.Game().State().Players[i].Ready = payload.Ready

		// 只更新自己的输入状态
		if payload.PlayerID == m.PlayerID() && m.Phase() == model.PhaseWaiting {
			updateWaitingInputPrompt(m)
		}
		break
	}
	return nil
}

func handleMsgRoomListResult(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.RoomListResultPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	m.Lobby().SetAvailableRooms(payload.Rooms)
	return nil
}

func handleMsgPlayerOffline(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.PlayerOfflinePayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	for i, p := range m.Game().State().Players {
		if p.ID == payload.PlayerID {
			m.Game().State().Players[i].Online = false
			break
		}
	}
	return nil
}

func handleMsgPlayerOnline(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.PlayerOnlinePayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	for i, p := range m.Game().State().Players {
		if p.ID == payload.PlayerID {
			m.Game().State().Players[i].Online = true
			break
		}
	}
	return nil
}
