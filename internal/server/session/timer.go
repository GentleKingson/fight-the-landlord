package session

import (
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
)

// --- 超时控制 ---

func (gs *GameSession) startBidTimer() {
	playerID := gs.players[gs.currentBidder].ID
	expectedTurnID := gs.turnID

	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	bidTimeout := gs.gameConfig.BidTimeoutDuration()
	now := time.Now()
	gs.remainingTime = bidTimeout
	gs.turnDeadline = now.Add(bidTimeout)
	gs.turnTimer = time.AfterFunc(bidTimeout, func() {
		_ = gs.handleBid(playerID, false, expectedTurnID)
	})
}

func (gs *GameSession) startPlayTimer() {
	expectedTurnID := gs.turnID

	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	turnTimeout := gs.gameConfig.TurnTimeoutDuration()
	now := time.Now()
	gs.remainingTime = turnTimeout
	gs.turnDeadline = now.Add(turnTimeout)
	gs.turnTimer = time.AfterFunc(turnTimeout, func() {
		gs.handlePlayTimeout(expectedTurnID)
	})
}

func (gs *GameSession) handlePlayTimeout(expectedTurnID int64) {
	gs.mu.Lock()

	if gs.retired || gs.state != GameStatePlaying || gs.turnID != expectedTurnID {
		gs.mu.Unlock()
		return
	}

	currentPlayer := gs.players[gs.currentPlayer]

	// 尝试找到最小能打过的牌
	cardsToPlay := rule.FindSmallestBeatingCards(currentPlayer.Hand, gs.lastPlayedHand)

	if cardsToPlay != nil {
		// 找到了能打的牌，出牌
		playerID := currentPlayer.ID
		cardInfos := convert.CardsToInfos(cardsToPlay)
		gs.mu.Unlock()
		_ = gs.handlePlayCards(playerID, cardInfos, expectedTurnID)
		return
	}

	// 没有能打的牌，自动 PASS
	playerID := currentPlayer.ID
	gs.mu.Unlock()
	_ = gs.handlePass(playerID, expectedTurnID)
}

func (gs *GameSession) stopTimer() {
	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	if gs.turnTimer != nil {
		gs.turnTimer.Stop()
		gs.turnTimer = nil
	}
	if gs.offlineWaitTimer != nil {
		gs.offlineWaitTimer.Stop()
		gs.offlineWaitTimer = nil
	}
	gs.remainingTime = 0
	gs.turnDeadline = time.Time{}
}

// StopAllTimers stops all timers (for cleanup when all players disconnect)
func (gs *GameSession) StopAllTimers() {
	gs.stopTimer()
}

// --- 离线处理 ---

// PlayerOffline 玩家离线
func (gs *GameSession) PlayerOffline(playerID string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.retired {
		return
	}

	// 找到玩家
	playerIdx := -1
	stateChanged := false
	for i, p := range gs.players {
		if p.ID == playerID {
			stateChanged = !p.IsOffline
			p.IsOffline = true
			playerIdx = i
			break
		}
	}

	if playerIdx == -1 {
		return
	}

	paused := gs.pauseOfflineTurnLocked(playerID, playerIdx)
	if !paused {
		if stateChanged {
			gs.markStateChangedLocked()
		}
		return // 不是当前回合，无需暂停
	}
	gs.markStateChangedLocked()
}

// pauseOfflineTurnLocked pauses the active turn for playerIdx. The caller owns
// gs.mu; timerMu prevents duplicate disconnect notifications from creating
// multiple offline timers for the same authoritative turn.
func (gs *GameSession) pauseOfflineTurnLocked(playerID string, playerIdx int) bool {
	isBidding := gs.state == GameStateBidding && gs.currentBidder == playerIdx
	isPlaying := gs.state == GameStatePlaying && gs.currentPlayer == playerIdx
	if !isBidding && !isPlaying {
		return false
	}

	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()
	if gs.offlineWaitTimer != nil {
		return false
	}

	// 暂停计时器，计算剩余时间
	if gs.turnTimer != nil {
		gs.turnTimer.Stop()
		gs.remainingTime = time.Until(gs.turnDeadline)
		if gs.remainingTime < 0 {
			gs.remainingTime = 0
		}
		gs.turnTimer = nil
		gs.turnDeadline = time.Time{}
	}

	// 启动离线等待计时器
	offlineTimeout := gs.gameConfig.OfflineWaitTimeoutDuration()
	gs.offlineWaitTimer = time.AfterFunc(offlineTimeout, func() {
		gs.handleOfflineTimeout(playerID)
	})

	log.Printf("⏸️ 玩家 %s 离线，暂停计时等待重连 (%v)", gs.players[playerIdx].Name, offlineTimeout)
	return true
}

// PlayerOnline 玩家上线
func (gs *GameSession) PlayerOnline(playerID string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.retired {
		return
	}

	// 找到玩家
	playerIdx := -1
	stateChanged := false
	for i, p := range gs.players {
		if p.ID == playerID {
			stateChanged = p.IsOffline
			p.IsOffline = false
			playerIdx = i
			break
		}
	}

	if playerIdx == -1 {
		return
	}

	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	// 取消离线等待计时器
	if gs.offlineWaitTimer != nil {
		gs.offlineWaitTimer.Stop()
		gs.offlineWaitTimer = nil
		stateChanged = true
	}

	// 检查是否是当前回合玩家，如果是则恢复计时器
	isBidding := gs.state == GameStateBidding && gs.currentBidder == playerIdx
	isPlaying := gs.state == GameStatePlaying && gs.currentPlayer == playerIdx

	if !isBidding && !isPlaying {
		if stateChanged {
			gs.markStateChangedLocked()
		}
		return
	}

	// 恢复计时器
	if gs.remainingTime > 0 {
		now := time.Now()
		gs.turnDeadline = now.Add(gs.remainingTime)
		expectedTurnID := gs.turnID
		if isBidding {
			playerID := gs.players[gs.currentBidder].ID
			gs.turnTimer = time.AfterFunc(gs.remainingTime, func() {
				_ = gs.handleBid(playerID, false, expectedTurnID)
			})
		} else {
			gs.turnTimer = time.AfterFunc(gs.remainingTime, func() {
				gs.handlePlayTimeout(expectedTurnID)
			})
		}
		log.Printf("▶️ 玩家 %s 重连，恢复计时 (剩余 %v)", gs.players[playerIdx].Name, gs.remainingTime)
		stateChanged = true
	}
	if stateChanged {
		gs.markStateChangedLocked()
	}
}

// handleOfflineTimeout 离线超时处理
func (gs *GameSession) handleOfflineTimeout(playerID string) {
	gs.mu.Lock()
	if gs.retired {
		gs.mu.Unlock()
		return
	}

	// 找到玩家
	playerIdx := -1
	for i, p := range gs.players {
		if p.ID == playerID {
			playerIdx = i
			break
		}
	}

	if playerIdx == -1 {
		gs.mu.Unlock()
		return
	}

	log.Printf("⏰ 玩家 %s 离线超时，自动执行操作", gs.players[playerIdx].Name)

	// 根据当前状态执行自动操作
	if gs.state == GameStateBidding && gs.currentBidder == playerIdx {
		expectedTurnID := gs.turnID
		gs.mu.Unlock()
		_ = gs.handleBid(playerID, false, expectedTurnID)
		return
	}

	if gs.state == GameStatePlaying && gs.currentPlayer == playerIdx {
		currentPlayer := gs.players[playerIdx]
		mustPlay := gs.lastPlayerIdx == gs.currentPlayer || gs.lastPlayedHand.IsEmpty()
		expectedTurnID := gs.turnID

		if mustPlay && len(currentPlayer.Hand) > 0 {
			// 出最小的牌
			minCard := currentPlayer.Hand[len(currentPlayer.Hand)-1]
			gs.mu.Unlock()
			_ = gs.handlePlayCards(playerID, []protocol.CardInfo{convert.CardToInfo(minCard)}, expectedTurnID)
			return
		}
		gs.mu.Unlock()
		_ = gs.handlePass(playerID, expectedTurnID)
		return
	}

	gs.mu.Unlock()
}
