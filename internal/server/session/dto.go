package session

import (
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
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
		online := !p.IsOffline
		if p.IsBot {
			online = true
		} else if sessionManager != nil {
			online = sessionManager.IsOnline(p.ID)
		}
		players[i] = protocol.PlayerInfo{
			ID:         p.ID,
			Name:       p.Name,
			Seat:       p.Seat,
			Ready:      p.Ready,
			IsLandlord: p.IsLandlord,
			CardsCount: len(p.Hand),
			Online:     online,
			IsBot:      p.IsBot,
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
	mustPlay := false
	canBeat := false
	isGrab := false
	switch gs.state {
	case GameStateBidding:
		if gs.validPlayerIndex(gs.currentBidder) {
			currentTurnID = gs.players[gs.currentBidder].ID
		}
		isGrab = gs.landlordCaller != -1
	case GameStatePlaying:
		if gs.validPlayerIndex(gs.currentPlayer) {
			currentPlayer := gs.players[gs.currentPlayer]
			currentTurnID = currentPlayer.ID
			mustPlay = gs.lastPlayerIdx == gs.currentPlayer || gs.lastPlayedHand.IsEmpty()
			canBeat = mustPlay || rule.FindSmallestBeatingCards(currentPlayer.Hand, gs.lastPlayedHand) != nil
		}
	}

	var lastPlayed []card.Card
	lastPlayerID := ""
	lastPlayerName := ""
	lastHandType := ""
	if !gs.lastPlayedHand.IsEmpty() && gs.validPlayerIndex(gs.lastPlayerIdx) {
		lastPlayed = gs.lastPlayedHand.Cards
		lastPlayer := gs.players[gs.lastPlayerIdx]
		lastPlayerID = lastPlayer.ID
		lastPlayerName = lastPlayer.Name
		lastHandType = gs.lastPlayedHand.Type.String()
	}

	bottomCardsRevealed := gs.state == GameStatePlaying || gs.state == GameStateEnded
	var bottomCards []protocol.CardInfo
	if bottomCardsRevealed {
		bottomCards = convert.CardsToInfos(gs.bottomCards)
	}

	playedCards := make([]protocol.PlayerPlayedCards, len(gs.players))
	for i, p := range gs.players {
		var cards []card.Card
		if i < len(gs.playedCards) {
			cards = gs.playedCards[i]
		}
		playedCards[i] = protocol.PlayerPlayedCards{
			PlayerID: p.ID,
			Cards:    convert.CardsToInfos(cards),
		}
	}
	turnDeadlineMS := gs.turnDeadlineMS()
	serverTimeMS := time.Now().UnixMilli()

	return &protocol.GameStateDTO{
		Phase:               phase,
		Players:             players,
		Hand:                convert.CardsToInfos(hand),
		BottomCards:         bottomCards,
		CurrentTurn:         currentTurnID,
		LastPlayed:          convert.CardsToInfos(lastPlayed),
		LastPlayerID:        lastPlayerID,
		MustPlay:            mustPlay,
		CanBeat:             canBeat,
		SnapshotVersion:     gs.snapshotVersion,
		GameID:              gs.gameID,
		BottomCardsRevealed: bottomCardsRevealed,
		TurnID:              gs.turnID,
		TurnDeadlineMS:      turnDeadlineMS,
		ServerTimeMS:        serverTimeMS,
		LastPlayerName:      lastPlayerName,
		LastHandType:        lastHandType,
		IsGrab:              isGrab,
		Multiplier:          gs.currentMultiplier(),
		BaseScore:           baseScore,
		PlayedCards:         playedCards,
	}
}

func (gs *GameSession) validPlayerIndex(index int) bool {
	return index >= 0 && index < len(gs.players)
}

// currentMultiplier returns the authoritative in-game multiplier. Once the
// game has ended it includes the spring or counter-spring settlement multiplier.
func (gs *GameSession) currentMultiplier() int {
	if gs.state == GameStateEnded && gs.settledMultiplier > 0 {
		return gs.settledMultiplier
	}

	multiplier := max(gs.bidMultiplier, 1)
	for range gs.bombCount {
		multiplier *= 2
	}
	return multiplier
}

func (gs *GameSession) turnDeadlineMS() int64 {
	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	if gs.turnDeadline.IsZero() {
		return 0
	}
	return gs.turnDeadline.UnixMilli()
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
