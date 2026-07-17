package session

import (
	"cmp"
	"slices"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
)

type validatedPlay struct {
	player *GamePlayer
	cards  []card.Card
	hand   rule.ParsedHand
}

// IsCurrentPlayTurn is a side-effect-free freshness check for asynchronous bot
// decisions. HandlePlayCardsAt and HandlePassAt still repeat these checks under
// the mutation lock so a turn cannot advance between this check and commit.
func (gs *GameSession) IsCurrentPlayTurn(playerID, expectedGameID string, expectedTurnID int64) bool {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	if gs.retired || gs.state != GameStatePlaying || expectedGameID == "" || expectedTurnID <= 0 ||
		gs.gameID != expectedGameID || gs.turnID != expectedTurnID ||
		gs.currentPlayer < 0 || gs.currentPlayer >= len(gs.players) {
		return false
	}
	current := gs.players[gs.currentPlayer]
	return current != nil && current.ID == playerID
}

// HandlePlayCards 处理出牌
func (gs *GameSession) HandlePlayCards(playerID string, cardInfos []protocol.CardInfo) error {
	return gs.handlePlayCards(playerID, cardInfos, 0)
}

func (gs *GameSession) handlePlayCards(playerID string, cardInfos []protocol.CardInfo, expectedTurnID int64) error {
	return gs.handlePlayCardsAt(playerID, cardInfos, "", expectedTurnID)
}

func (gs *GameSession) HandlePlayCardsAt(playerID string, cardInfos []protocol.CardInfo, expectedGameID string, expectedTurnID int64) error {
	if expectedGameID == "" {
		return apperrors.ErrStaleGame
	}
	if expectedTurnID <= 0 {
		return apperrors.ErrStaleTurn
	}
	return gs.handlePlayCardsAt(playerID, cardInfos, expectedGameID, expectedTurnID)
}

func (gs *GameSession) handlePlayCardsAt(playerID string, cardInfos []protocol.CardInfo, expectedGameID string, expectedTurnID int64) error {
	gs.actionMu.Lock()
	defer gs.actionMu.Unlock()
	gs.mu.Lock()
	play, err := gs.validatePlayLocked(playerID, cardInfos, expectedGameID, expectedTurnID)
	if err != nil {
		gs.mu.Unlock()
		return err
	}

	// 所有验证通过后才取消计时器
	gs.stopTimer()

	// 出牌成功，更新状态
	gs.lastPlayedHand = play.hand
	gs.lastPlayerIdx = gs.currentPlayer
	gs.consecutivePasses = 0

	// 累计倍数与出牌次数（用于结算）
	if play.hand.Type == rule.Bomb || play.hand.Type == rule.Rocket {
		gs.bombCount++ // 炸弹 / 王炸各翻一倍
	}
	if play.player.IsLandlord {
		gs.landlordPlays++
	} else {
		gs.farmerPlays++
	}

	// 从手牌中移除
	play.player.Hand = card.RemoveCards(play.player.Hand, play.cards)

	// 对出的牌进行排序（从大到小），确保显示顺序正确
	sortedCards := make([]card.Card, len(play.cards))
	copy(sortedCards, play.cards)
	slices.SortFunc(sortedCards, func(a, b card.Card) int {
		return cmp.Compare(b.Rank, a.Rank)
	})
	gs.lastPlayedHand.Cards = append([]card.Card(nil), sortedCards...)
	if len(gs.playedCards) != len(gs.players) {
		gs.playedCards = make([][]card.Card, len(gs.players))
	}
	gs.playedCards[gs.currentPlayer] = append(gs.playedCards[gs.currentPlayer], sortedCards...)

	// 广播出牌信息
	gs.queueCommandBroadcastLocked(playerID, gs.newGameEventMessage(protocol.MsgCardPlayed, protocol.CardPlayedPayload{
		PlayerID:   playerID,
		PlayerName: play.player.Name,
		Cards:      convert.CardsToInfos(sortedCards), // 使用排序后的牌
		CardsLeft:  len(play.player.Hand),
		HandType:   play.hand.Type.String(),
	}))

	// 检查是否获胜
	if len(play.player.Hand) == 0 {
		gs.endGame(play.player)
		work := gs.takePendingWorkLocked()
		gs.mu.Unlock()
		gs.dispatchPendingWork(work)
		return nil
	}

	// 下一个玩家
	gs.currentPlayer = (gs.currentPlayer + 1) % 3
	gs.notifyPlayTurn()

	work := gs.takePendingWorkLocked()
	gs.mu.Unlock()
	gs.dispatchPendingWork(work)
	return nil
}

func (gs *GameSession) validatePlayLocked(playerID string, cardInfos []protocol.CardInfo, expectedGameID string, expectedTurnID int64) (validatedPlay, error) {
	if gs.retired {
		return validatedPlay{}, apperrors.ErrGameNotStart
	}
	if expectedGameID != "" && gs.gameID != expectedGameID {
		return validatedPlay{}, apperrors.ErrStaleGame
	}
	if gs.state != GameStatePlaying {
		return validatedPlay{}, apperrors.ErrGameNotStart
	}
	if expectedTurnID != 0 && gs.turnID != expectedTurnID {
		if expectedGameID != "" {
			return validatedPlay{}, apperrors.ErrStaleTurn
		}
		return validatedPlay{}, apperrors.ErrNotYourTurn
	}

	currentPlayer := gs.players[gs.currentPlayer]
	if currentPlayer.ID != playerID {
		return validatedPlay{}, apperrors.ErrNotYourTurn
	}
	cards := convert.InfosToCards(cardInfos)
	if !gs.validateCardsInHand(currentPlayer, cards) {
		return validatedPlay{}, apperrors.ErrInvalidCards
	}
	handToPlay, err := rule.ParseHand(cards)
	if err != nil {
		return validatedPlay{}, apperrors.ErrInvalidCards
	}
	isNewRound := gs.lastPlayerIdx == gs.currentPlayer || gs.lastPlayedHand.IsEmpty()
	if !isNewRound && !rule.CanBeat(handToPlay, gs.lastPlayedHand) {
		return validatedPlay{}, apperrors.ErrCannotBeat
	}
	return validatedPlay{player: currentPlayer, cards: cards, hand: handToPlay}, nil
}

// HandlePass 处理不出
func (gs *GameSession) HandlePass(playerID string) error {
	return gs.handlePass(playerID, 0)
}

func (gs *GameSession) handlePass(playerID string, expectedTurnID int64) error {
	return gs.handlePassAt(playerID, "", expectedTurnID)
}

func (gs *GameSession) HandlePassAt(playerID, expectedGameID string, expectedTurnID int64) error {
	if expectedGameID == "" {
		return apperrors.ErrStaleGame
	}
	if expectedTurnID <= 0 {
		return apperrors.ErrStaleTurn
	}
	return gs.handlePassAt(playerID, expectedGameID, expectedTurnID)
}

func (gs *GameSession) handlePassAt(playerID, expectedGameID string, expectedTurnID int64) error {
	gs.actionMu.Lock()
	defer gs.actionMu.Unlock()
	gs.mu.Lock()
	if gs.retired {
		gs.mu.Unlock()
		return apperrors.ErrGameNotStart
	}
	if expectedGameID != "" && gs.gameID != expectedGameID {
		gs.mu.Unlock()
		return apperrors.ErrStaleGame
	}

	if gs.state != GameStatePlaying {
		gs.mu.Unlock()
		return apperrors.ErrGameNotStart
	}
	if expectedTurnID != 0 && gs.turnID != expectedTurnID {
		gs.mu.Unlock()
		if expectedGameID != "" {
			return apperrors.ErrStaleTurn
		}
		return apperrors.ErrNotYourTurn
	}

	currentPlayer := gs.players[gs.currentPlayer]
	if currentPlayer.ID != playerID {
		gs.mu.Unlock()
		return apperrors.ErrNotYourTurn
	}

	// 检查是否必须出牌
	mustPlay := gs.lastPlayerIdx == gs.currentPlayer || gs.lastPlayedHand.IsEmpty()
	if mustPlay {
		gs.mu.Unlock()
		return apperrors.ErrMustPlay
	}

	// 取消超时计时器
	gs.stopTimer()

	gs.consecutivePasses++

	// 广播不出
	gs.queueCommandBroadcastLocked(playerID, gs.newGameEventMessage(protocol.MsgPlayerPass, protocol.PlayerPassPayload{
		PlayerID:   playerID,
		PlayerName: currentPlayer.Name,
	}))

	// 如果连续两人不出，新一轮开始
	if gs.consecutivePasses >= 2 {
		gs.lastPlayedHand = rule.ParsedHand{}
		gs.lastPlayerIdx = (gs.currentPlayer + 1) % 3
		gs.consecutivePasses = 0
	}

	// 下一个玩家
	gs.currentPlayer = (gs.currentPlayer + 1) % 3
	gs.notifyPlayTurn()

	work := gs.takePendingWorkLocked()
	gs.mu.Unlock()
	gs.dispatchPendingWork(work)
	return nil
}

// validateCardsInHand 验证牌是否在手中
func (gs *GameSession) validateCardsInHand(player *GamePlayer, cards []card.Card) bool {
	handCopy := make([]card.Card, len(player.Hand))
	copy(handCopy, player.Hand)

	for _, c := range cards {
		found := false
		for i, h := range handCopy {
			if h.Suit == c.Suit && h.Rank == c.Rank {
				handCopy = append(handCopy[:i], handCopy[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// GetPlayerCardsCount 获取玩家手牌数量
func (gs *GameSession) GetPlayerCardsCount(playerID string) int {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	for _, p := range gs.players {
		if p.ID == playerID {
			return len(p.Hand)
		}
	}
	return 0
}

// notifyPlayTurn 通知当前玩家出牌
func (gs *GameSession) notifyPlayTurn() {
	player := gs.players[gs.currentPlayer]
	mustPlay := gs.lastPlayerIdx == gs.currentPlayer || gs.lastPlayedHand.IsEmpty()

	// 计算是否能打过上家
	canBeat := mustPlay // 如果必须出牌，则肯定能出（新一轮）
	if !mustPlay {
		// 检查是否有能打过上家的牌
		beatingCards := rule.FindSmallestBeatingCards(player.Hand, gs.lastPlayedHand)
		canBeat = beatingCards != nil
	}

	gs.turnID++
	gs.startPlayTimer()
	gs.queueBroadcastLocked(gs.newGameEventMessage(protocol.MsgPlayTurn, protocol.PlayTurnPayload{
		PlayerID: player.ID,
		Timeout:  gs.gameConfig.TurnTimeout,
		MustPlay: mustPlay,
		CanBeat:  canBeat,
	}))
}
