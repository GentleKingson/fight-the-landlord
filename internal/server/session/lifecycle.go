package session

import (
	"cmp"
	"log"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
)

// Start 开始游戏
func (gs *GameSession) Start() {
	gs.actionMu.Lock()
	gs.mu.Lock()
	if gs.retired {
		gs.mu.Unlock()
		gs.actionMu.Unlock()
		return
	}
	if gs.metrics != nil && !gs.metricStarted {
		gs.metricStarted = true
		gs.metricStartedAt = time.Now()
		gs.metrics.GameStarted()
	}
	gs.startBiddingRound()
	work := gs.takePendingWorkLocked()
	gs.mu.Unlock()
	gs.dispatchPendingWork(work)
	gs.pauseInitialOfflineTurn()
	gs.actionMu.Unlock()
}

// Retire permanently prevents a removed session from starting and stops any
// timer it already owns. actionMu serializes retirement with Start and command
// delivery, so a late registration callback cannot reactivate the session.
func (gs *GameSession) Retire() {
	gs.actionMu.Lock()
	gs.mu.Lock()
	gs.retired = true
	if gs.metrics != nil && gs.metricStarted && !gs.metricFinished {
		gs.metricFinished = true
		gs.metrics.GameAborted()
	}
	gs.mu.Unlock()
	gs.stopTimer()
	gs.actionMu.Unlock()
	gs.releaseQuiescence()
}

// pauseInitialOfflineTurn closes the registration window where a player can
// disconnect before the first authoritative turn exists. Presence is already
// recorded on the session; once bidding starts, the normal offline wait policy
// must replace the turn timer when that player was selected first.
func (gs *GameSession) pauseInitialOfflineTurn() {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.state != GameStateBidding || !gs.validPlayerIndex(gs.currentBidder) {
		return
	}
	player := gs.players[gs.currentBidder]
	if player.IsOffline && gs.pauseOfflineTurnLocked(player.ID, gs.currentBidder) {
		gs.markStateChangedLocked()
	}
}

// maxRedeals 最大流局次数；达到后下一局随机强制指定地主，避免无限流局
const maxRedeals = 3

// startBiddingRound 发牌并进入叫地主阶段（调用方需持有 gs.mu）
func (gs *GameSession) startBiddingRound() {
	gs.dealNewRound()

	// 进入叫地主阶段
	gs.state = GameStateBidding
	gs.room.SetState(RoomStateBidding)

	// 随机选择第一个叫地主的玩家
	gs.currentBidder = rand.IntN(3)

	// 通知叫地主
	gs.notifyBidTurn()
}

// dealNewRound 重置本局状态并发牌（不进入叫地主流程；调用方需持有 gs.mu）
func (gs *GameSession) dealNewRound() {
	gs.gameID = nextGameID()
	gs.playedCards = make([][]card.Card, len(gs.players))
	gs.lastPlayedHand = rule.ParsedHand{}
	gs.lastPlayerIdx = -1
	gs.consecutivePasses = 0
	gs.settledMultiplier = 0
	gs.settlement = nil

	// 重置叫抢与倍数状态
	gs.landlordCaller = -1
	gs.landlordCandidate = -1
	gs.bidPasses = 0
	gs.grabActions = 0
	gs.bidMultiplier = 1
	gs.bombCount = 0
	gs.landlordPlays = 0
	gs.farmerPlays = 0
	gs.bottomCards = nil
	for _, p := range gs.players {
		p.Hand = nil
		p.IsLandlord = false
	}
	gs.room.SetLandlord("")

	// 创建并洗牌
	gs.deck = card.NewDeck()
	gs.deck.Shuffle()

	// 发牌
	gs.deal()
}

// redeal 流局（无人叫地主）重新发牌（调用方需持有 gs.mu）
// 连续流局达到 maxRedeals 次后，重新发牌并随机强制指定地主，避免无限流局。
func (gs *GameSession) redeal() {
	gs.redealCount++

	if gs.redealCount >= maxRedeals {
		log.Printf("🔄 房间 %s 连续 %d 次流局，重新发牌并随机强制指定地主", gs.room.Code, gs.redealCount)
		gs.dealNewRound()
		gs.setLandlord(rand.IntN(3))
		return
	}

	log.Printf("🔄 房间 %s 无人叫地主，第 %d 次流局，重新发牌", gs.room.Code, gs.redealCount)
	gs.startBiddingRound()
}

// deal 发牌
func (gs *GameSession) deal() {
	// 每人发 17 张
	for range 17 {
		for i := range 3 {
			gs.players[i].Hand = append(gs.players[i].Hand, gs.deck[0])
			gs.deck = gs.deck[1:]
		}
	}

	// 剩余 3 张为底牌
	gs.bottomCards = gs.deck

	// 排序手牌
	for _, p := range gs.players {
		slices.SortFunc(p.Hand, func(a, b card.Card) int {
			return cmp.Compare(b.Rank, a.Rank)
		})
	}

	// GameStart belongs to the authoritative game stream. Emitting it here,
	// after gameID exists and before private hands are sent, gives clients one
	// ordered reset point for both initial deals and redeals.
	gs.queueBroadcastLocked(gs.newGameEventMessage(protocol.MsgGameStart, protocol.GameStartPayload{
		Players: gs.room.GetAllPlayersInfo(),
	}))

	// 发牌是一次权威状态变更；三个玩家收到同一水位的私有投影。
	event := gs.nextEventMetaLocked()
	for _, p := range gs.players {
		gs.queuePrivateLocked(p.ID, newGameEventMessageWithMeta(protocol.MsgDealCards, protocol.DealCardsPayload{
			Cards:       convert.CardsToInfos(p.Hand),
			BottomCards: make([]protocol.CardInfo, 3), // 暂时不显示
		}, event))
	}
}

// endGame 结束游戏
func (gs *GameSession) endGame(winner *GamePlayer) {
	gs.stopTimer()
	gs.state = GameStateEnded
	gs.room.SetState(RoomStateEnded)
	if gs.metrics != nil && gs.metricStarted && !gs.metricFinished {
		gs.metricFinished = true
		gs.metrics.GameFinished(time.Since(gs.metricStartedAt))
	}

	// 计算最终倍数与各玩家得分
	multiplier := gs.finalMultiplier(winner)
	gs.settledMultiplier = multiplier
	scores := gs.computeScores(winner, multiplier)

	// 收集所有玩家剩余手牌
	playerHands := make([]protocol.PlayerHand, len(gs.players))
	for i, p := range gs.players {
		playerHands[i] = protocol.PlayerHand{
			PlayerID:   p.ID,
			PlayerName: p.Name,
			Cards:      convert.CardsToInfos(p.Hand),
		}
	}
	gs.settlement = cloneGameSettlement(&protocol.GameSettlementDTO{
		WinnerID:         winner.ID,
		WinnerName:       winner.Name,
		WinnerIsLandlord: winner.IsLandlord,
		Multiplier:       multiplier,
		Scores:           scores,
		PlayerHands:      playerHands,
	})

	// Keep the room ended until every client has observed GameOver. A Ready
	// command delivered synchronously by a client callback must not be able to
	// replace this session before the terminal event is broadcast.
	gs.queueBroadcastLocked(gs.newGameEventMessage(protocol.MsgGameOver, protocol.GameOverPayload{
		WinnerID:    gs.settlement.WinnerID,
		WinnerName:  gs.settlement.WinnerName,
		IsLandlord:  gs.settlement.WinnerIsLandlord,
		PlayerHands: gs.settlement.PlayerHands,
		Multiplier:  gs.settlement.Multiplier,
		Scores:      gs.settlement.Scores,
	}))

	// Preserve membership and the completed GameSession for reconnect. The room
	// is reopened only after GameOver delivery has completed, without holding
	// GameSession.mu or Room.mu during network I/O.
	for _, p := range gs.players {
		p.Ready = p.IsBot
	}
	gs.pendingRoomReset = true
	gs.markStateChangedLocked()

	role := "农民"
	if winner.IsLandlord {
		role = "地主"
	}
	log.Printf("🎮 游戏结束！房间 %s，获胜者: %s (%s)，倍数: %d",
		gs.room.Code, winner.Name, role, multiplier)

	gs.queueGameResultsLocked(winner)
}

// finalMultiplier 计算本局最终倍数：底倍 × 炸弹/王炸 × 春天/反春天
func (gs *GameSession) finalMultiplier(winner *GamePlayer) int {
	mult := max(gs.bidMultiplier, 1)

	// 炸弹与王炸：每个翻一倍
	for range gs.bombCount {
		mult *= 2
	}

	// 春天：地主获胜且农民一张牌都没出过
	// 反春天：农民获胜且地主只在首攻出过一手牌
	switch {
	case winner.IsLandlord && gs.farmerPlays == 0:
		mult *= 2
	case !winner.IsLandlord && gs.landlordPlays == 1:
		mult *= 2
	}

	return mult
}

// computeScores 按最终倍数计算各玩家得分（地主独自对抗两名农民）
func (gs *GameSession) computeScores(winner *GamePlayer, mult int) []protocol.PlayerScore {
	landlordWins := winner.IsLandlord
	scores := make([]protocol.PlayerScore, len(gs.players))
	for i, p := range gs.players {
		var score int
		switch {
		case p.IsLandlord && landlordWins:
			score = 2 * mult
		case p.IsLandlord && !landlordWins:
			score = -2 * mult
		case !p.IsLandlord && landlordWins:
			score = -mult
		default: // 农民获胜
			score = mult
		}
		scores[i] = protocol.PlayerScore{
			PlayerID:   p.ID,
			PlayerName: p.Name,
			IsLandlord: p.IsLandlord,
			Score:      score,
		}
	}
	return scores
}

func (gs *GameSession) queueGameResultsLocked(winner *GamePlayer) {
	// 计算获胜方
	landlordWins := winner.IsLandlord
	settledAt := time.Now()

	roomPlayers := gs.room.SnapshotPlayers()
	roomNames := make(map[string]string, len(roomPlayers))
	for _, player := range roomPlayers {
		roomNames[player.ID] = player.Name
	}
	for _, p := range gs.players {
		if p.IsBot {
			continue // Bot 不计入排行榜
		}

		isWinner := false
		if landlordWins {
			isWinner = p.IsLandlord
		} else {
			isWinner = !p.IsLandlord
		}

		// 获取玩家名称
		playerName := p.Name
		if currentName := roomNames[p.ID]; currentName != "" {
			playerName = currentName
		}

		gs.pendingResults = append(gs.pendingResults, pendingGameResult{
			gameID:     gs.gameID,
			settledAt:  settledAt,
			playerID:   p.ID,
			playerName: playerName,
			isLandlord: p.IsLandlord,
			isWinner:   isWinner,
		})
	}
}
