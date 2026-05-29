package session

import (
	"cmp"
	"slices"

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
//   - 抢地主阶段：除暂定地主外的玩家依次「抢 / 不抢」，每次抢翻一倍并接管暂定地主身份
//     （原叫地主者可「反抢」）。当连续两人放弃后，暂定地主成为地主，底倍即叫抢累计的倍数。
func (gs *GameSession) HandleBid(playerID string, bid bool) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	if gs.state != GameStateBidding {
		return apperrors.ErrGameNotStart
	}

	currentPlayer := gs.players[gs.currentBidder]
	if currentPlayer.ID != playerID {
		return apperrors.ErrNotYourTurn
	}

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
		gs.grabActions = 0

		gs.broadcastBidResult(player, true, false)

		gs.currentBidder = gs.nextGrabber(gs.currentBidder)
		gs.notifyBidTurn()
		return
	}

	// 不叫
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
		gs.bidPasses = 0
	} else {
		gs.bidPasses++
	}
	gs.grabActions++

	gs.broadcastBidResult(player, bid, true)

	// 抢地主结束条件（满足其一）：
	//   1. 除暂定地主外的两名玩家连续放弃；
	//   2. 每名玩家都已抢过一次（叫地主者 A 之后 B、C、A 依次决策，最多 3 次），
	//      防止互相反抢导致倍数无限翻倍。
	if gs.bidPasses >= 2 || gs.grabActions >= 3 {
		gs.setLandlord(gs.landlordCandidate)
		return
	}

	gs.currentBidder = gs.nextGrabber(gs.currentBidder)
	gs.notifyBidTurn()
}

// nextGrabber 返回下一个可抢地主的玩家索引（跳过当前暂定地主）
func (gs *GameSession) nextGrabber(from int) int {
	next := (from + 1) % 3
	if next == gs.landlordCandidate {
		next = (next + 1) % 3
	}
	return next
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
	gs.room.State = RoomStatePlaying
	gs.currentPlayer = idx
	gs.lastPlayerIdx = idx

	gs.notifyPlayTurn()
}

// notifyBidTurn 通知当前玩家叫/抢地主
func (gs *GameSession) notifyBidTurn() {
	player := gs.players[gs.currentBidder]
	gs.room.Broadcast(codec.MustNewMessage(protocol.MsgBidTurn, protocol.BidTurnPayload{
		PlayerID:   player.ID,
		Timeout:    gs.gameConfig.BidTimeout,
		IsGrab:     gs.landlordCaller != -1,
		Multiplier: gs.bidMultiplier,
	}))
	gs.startBidTimer()
}
