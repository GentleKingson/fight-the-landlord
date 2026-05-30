package session

import (
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// BuildGameStateDTO 构建游戏状态 DTO（用于重连等场景）
func (gs *GameSession) BuildGameStateDTO(playerID string, sessionManager *SessionManager) *protocol.GameStateDTO {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	var hand []card.Card
	for _, p := range gs.players {
		if p.ID == playerID {
			hand = p.Hand
			break
		}
	}
	players := make([]protocol.PlayerInfo, len(gs.players))
	for i, p := range gs.players {
		players[i] = protocol.PlayerInfo{
			ID:         p.ID,
			Name:       p.Name,
			Seat:       p.Seat,
			IsLandlord: p.IsLandlord,
			CardsCount: len(p.Hand),
			Online:     sessionManager.IsOnline(p.ID),
		}
	}
	phase := "waiting"
	switch gs.state {
	case GameStateBidding:
		phase = "bidding"
	case GameStatePlaying:
		phase = "playing"
	case GameStateEnded:
		phase = "ended"
	}
	currentTurnID := ""
	switch gs.state {
	case GameStateBidding:
		currentTurnID = gs.players[gs.currentBidder].ID
	case GameStatePlaying:
		currentTurnID = gs.players[gs.currentPlayer].ID
	}
	var lastPlayed []card.Card
	lastPlayerID := ""
	if !gs.lastPlayedHand.IsEmpty() {
		lastPlayed = gs.lastPlayedHand.Cards
		lastPlayerID = gs.players[gs.lastPlayerIdx].ID
	}
	return &protocol.GameStateDTO{
		Phase:        phase,
		Players:      players,
		Hand:         convert.CardsToInfos(hand),
		BottomCards:  convert.CardsToInfos(gs.bottomCards),
		CurrentTurn:  currentTurnID,
		LastPlayed:   convert.CardsToInfos(lastPlayed),
		LastPlayerID: lastPlayerID,
		MustPlay:     gs.lastPlayerIdx == gs.currentPlayer || gs.lastPlayedHand.IsEmpty(),
		CanBeat:      true,
	}
}

// ResendTurnTo 在重连后向指定玩家补发"当前回合"通知（叫地主/出牌），携带计时器的剩余时间。它只向单个客户端发送、不广播、不重启计时器，用于恢复重连玩家的操作提示（按钮、倒计时、叫/抢区分）。
func (gs *GameSession) ResendTurnTo(client types.ClientInterface) {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	switch gs.state {
	case GameStateBidding:
		player := gs.players[gs.currentBidder]
		client.SendMessage(codec.MustNewMessage(protocol.MsgBidTurn, protocol.BidTurnPayload{
			PlayerID:   player.ID,
			Timeout:    gs.remainingTurnSeconds(gs.gameConfig.BidTimeout),
			IsGrab:     gs.landlordCaller != -1,
			Multiplier: gs.bidMultiplier,
		}))
	case GameStatePlaying:
		player := gs.players[gs.currentPlayer]
		mustPlay := gs.lastPlayerIdx == gs.currentPlayer || gs.lastPlayedHand.IsEmpty()
		canBeat := mustPlay
		if !mustPlay {
			canBeat = rule.FindSmallestBeatingCards(player.Hand, gs.lastPlayedHand) != nil
		}
		client.SendMessage(codec.MustNewMessage(protocol.MsgPlayTurn, protocol.PlayTurnPayload{
			PlayerID: player.ID,
			Timeout:  gs.remainingTurnSeconds(gs.gameConfig.TurnTimeout),
			MustPlay: mustPlay,
			CanBeat:  canBeat,
		}))
	}
}

// remainingTurnSeconds 返回当前回合计时器剩余秒数；无计时器时回退到 fallback
func (gs *GameSession) remainingTurnSeconds(fallback int) int {
	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	if gs.turnTimer == nil || gs.timerStartTime.IsZero() {
		return fallback
	}
	remaining := time.Until(gs.timerStartTime.Add(gs.remainingTime))
	if remaining <= 0 {
		return 0
	}
	return int(remaining.Seconds())
}

// SerializeForRedis 为Redis序列化准备数据（提供只读访问）
func (gs *GameSession) SerializeForRedis(serialize func()) {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	serialize()
}

// GetStateForSerialization 获取state用于序列化
func (gs *GameSession) GetStateForSerialization() GameState {
	return gs.state
}

// GetCurrentPlayerForSerialization 获取currentPlayer用于序列化
func (gs *GameSession) GetCurrentPlayerForSerialization() int {
	return gs.currentPlayer
}

// GetCurrentBidderForSerialization 获取当前叫/抢地主玩家索引
func (gs *GameSession) GetCurrentBidderForSerialization() int {
	return gs.currentBidder
}

// GetHighestBidderForSerialization 获取当前暂定地主索引用于序列化
func (gs *GameSession) GetHighestBidderForSerialization() int {
	return gs.landlordCandidate
}

// GetPlayersForSerialization 获取players用于序列化
func (gs *GameSession) GetPlayersForSerialization() []*GamePlayer {
	return gs.players
}

// GetBottomCardsForSerialization 获取bottomCards用于序列化
func (gs *GameSession) GetBottomCardsForSerialization() []card.Card {
	return gs.bottomCards
}
