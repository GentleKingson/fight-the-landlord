package session

import (
	"cmp"
	"slices"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
)

// HandleBid 处理叫地主 / 抢地主
//
// 叫抢流程：
//   - 叫地主阶段（尚无人叫）：依次询问每位玩家「叫 / 不叫」。第一个叫地主的人成为暂定地主，
//     底倍为 1，随后进入抢地主阶段。若一圈无人叫则流局重新发牌。
//   - 抢地主阶段：从原叫者下家开始，跳过本轮“不叫”的玩家，每人仅一次「抢 / 不抢」。
//     有人抢后，原叫者最后有一次反抢机会；无人抢则原叫者直接成为地主。
//     每次抢翻一倍，所有有资格的玩家决策后，最后叫/抢者成为地主。
func (gs *GameSession) HandleBid(playerID string, bid bool) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	return gs.handleBidLocked(playerID, bid, "")
}

func (gs *GameSession) handleBidLocked(playerID string, bid bool, source string) error {
	if gs.state != GameStateBidding {
		return apperrors.ErrGameNotStart
	}

	currentPlayer := gs.players[gs.currentBidder]
	if currentPlayer.ID != playerID {
		return apperrors.ErrNotYourTurn
	}

	gs.recordDecision("bid", currentPlayer, nil, bid, source)

	// 取消超时计时器
	gs.stopTimer()

	isGrab := gs.landlordCaller != -1 // 已有人叫地主则处于抢地主阶段

	if isGrab {
		gs.handleGrab(currentPlayer, bid)
	} else {
		gs.handleCall(currentPlayer, bid)
	}
	return nil
}

// handleCall 处理叫地主阶段的决策
func (gs *GameSession) handleCall(player *GamePlayer, bid bool) {
	if bid {
		// 叫地主：成为暂定地主，底倍为 1，进入抢地主阶段
		gs.landlordCaller = gs.currentBidder
		gs.landlordCandidate = gs.currentBidder
		gs.bidMultiplier = 1
		gs.bidPasses = 0

		gs.broadcastBidResult(player, true, false)

		gs.advanceGrabTurn()
		return
	}

	// 不叫
	gs.declinedCall[gs.currentBidder] = true
	gs.bidPasses++
	gs.broadcastBidResult(player, false, false)

	// 一圈无人叫地主 → 流局，重新发牌
	if gs.bidPasses >= 3 {
		gs.redeal()
		return
	}

	gs.currentBidder = (gs.currentBidder + 1) % 3
	gs.notifyBidTurn()
}

// handleGrab 处理抢地主阶段的决策
func (gs *GameSession) handleGrab(player *GamePlayer, bid bool) {
	if bid {
		// 抢地主：翻倍并接管暂定地主身份
		gs.bidMultiplier *= 2
		gs.landlordCandidate = gs.currentBidder
	}
	gs.grabDecided[gs.currentBidder] = true

	gs.broadcastBidResult(player, bid, true)

	gs.advanceGrabTurn()
}

func (gs *GameSession) advanceGrabTurn() {
	next := gs.nextGrabber(gs.currentBidder)
	if next == -1 {
		gs.setLandlord(gs.landlordCandidate)
		return
	}
	gs.currentBidder = next
	gs.notifyBidTurn()
}

// nextGrabber 按座位顺序查找剩余资格；-1 表示叫抢结束。
// 原叫者只有在地主被抢走时才参与，且只在其余座位决策后轮到。
func (gs *GameSession) nextGrabber(from int) int {
	for step := 1; step <= 3; step++ {
		next := (from + step) % 3
		if !gs.declinedCall[next] && !gs.grabDecided[next] && next != gs.landlordCandidate {
			return next
		}
	}
	return -1
}

// broadcastBidResult 广播叫/抢地主结果
func (gs *GameSession) broadcastBidResult(player *GamePlayer, bid, isGrab bool) {
	gs.room.Broadcast(codec.MustNewMessage(protocol.MsgBidResult, protocol.BidResultPayload{
		PlayerID:   player.ID,
		PlayerName: player.Name,
		Bid:        bid,
		IsGrab:     isGrab,
		Multiplier: gs.bidMultiplier,
	}))
}

// setLandlord 设置地主
func (gs *GameSession) setLandlord(idx int) {
	landlord := gs.players[idx]
	landlord.IsLandlord = true

	// 底牌给地主
	landlord.Hand = append(landlord.Hand, gs.bottomCards...)
	slices.SortFunc(landlord.Hand, func(a, b card.Card) int {
		return cmp.Compare(b.Rank, a.Rank)
	})

	// 更新房间玩家状态
	gs.room.Players[landlord.ID].IsLandlord = true

	// 广播地主信息（含底倍）
	gs.room.Broadcast(codec.MustNewMessage(protocol.MsgLandlord, protocol.LandlordPayload{
		PlayerID:    landlord.ID,
		PlayerName:  landlord.Name,
		BottomCards: convert.CardsToInfos(gs.bottomCards),
		Multiplier:  gs.bidMultiplier,
	}))

	// 给地主发送更新后的手牌
	rp := gs.room.Players[landlord.ID]
	client := rp.Client
	client.SendMessage(codec.MustNewMessage(protocol.MsgDealCards, protocol.DealCardsPayload{
		Cards:       convert.CardsToInfos(landlord.Hand),
		BottomCards: convert.CardsToInfos(gs.bottomCards),
	}))

	// 开始游戏，地主先出牌
	gs.state = GameStatePlaying
	gs.room.SetState(RoomStatePlaying)
	gs.currentPlayer = idx
	gs.lastPlayerIdx = idx

	gs.recordEvent("landlord", map[string]any{"seat": idx, "bid_multiplier": gs.bidMultiplier})
	gs.notifyPlayTurn()
}

// notifyBidTurn 通知当前玩家叫/抢地主
func (gs *GameSession) notifyBidTurn() {
	gs.decisionStarted = time.Now()
	gs.turnID++
	player := gs.players[gs.currentBidder]
	gs.broadcastBotTurn(codec.MustNewMessage(protocol.MsgBidTurn, protocol.BidTurnPayload{
		PlayerID:   player.ID,
		Timeout:    gs.gameConfig.BidTimeout,
		IsGrab:     gs.landlordCaller != -1,
		Multiplier: gs.bidMultiplier,
	}))
	gs.startBidTimer()
}
