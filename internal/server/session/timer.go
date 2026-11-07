package session

import (
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
)

// --- 超时控制 ---

func (gs *GameSession) startBidTimer() {
	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	bidTimeout := gs.gameConfig.BidTimeoutDuration()
	gs.timerStartTime = time.Now()
	gs.remainingTime = bidTimeout
	turnID := gs.turnID
	gs.turnTimer = time.AfterFunc(bidTimeout, func() { gs.handleTurnTimeout(turnID, "timeout") })
}

func (gs *GameSession) startPlayTimer() {
	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	turnTimeout := gs.gameConfig.TurnTimeoutDuration()
	gs.timerStartTime = time.Now()
	gs.remainingTime = turnTimeout
	turnID := gs.turnID
	gs.turnTimer = time.AfterFunc(turnTimeout, func() { gs.handleTurnTimeout(turnID, "timeout") })
}

func (gs *GameSession) handleTurnTimeout(turnID uint64, source string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if turnID != gs.turnID {
		return
	}
	switch gs.state {
	case GameStateBidding:
		if gs.players[gs.currentBidder].IsOffline {
			source = "offline"
		}
		_ = gs.handleBidLocked(gs.players[gs.currentBidder].ID, false, source)
	case GameStatePlaying:
		if gs.players[gs.currentPlayer].IsOffline {
			source = "offline"
		}
		gs.playTimeoutLocked(source)
	}
}

func (gs *GameSession) handlePlayTimeout() {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.state == GameStatePlaying {
		gs.playTimeoutLocked("timeout")
	}
}

func (gs *GameSession) playTimeoutLocked(source string) {
	p := gs.players[gs.currentPlayer]
	cards := rule.FindSmallestBeatingCards(p.Hand, gs.lastPlayedHand)
	if cards != nil {
		_ = gs.handlePlayCardsLocked(p.ID, convert.CardsToInfos(cards), source)
	} else {
		_ = gs.handlePassLocked(p.ID, source)
	}
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
}

// StopAllTimers stops all timers (for cleanup when all players disconnect)
func (gs *GameSession) StopAllTimers() {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	gs.turnID++
	gs.stopTimer()
	gs.finishRecording("aborted", nil)
}

// --- 离线处理 ---

// PlayerOffline 玩家离线
func (gs *GameSession) PlayerOffline(playerID string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	// 找到玩家
	playerIdx := -1
	for i, p := range gs.players {
		if p.ID == playerID {
			p.IsOffline = true
			playerIdx = i
			break
		}
	}

	if playerIdx == -1 {
		return
	}

	// 检查是否是当前回合玩家
	isBidding := gs.state == GameStateBidding && gs.currentBidder == playerIdx
	isPlaying := gs.state == GameStatePlaying && gs.currentPlayer == playerIdx

	if !isBidding && !isPlaying {
		return // 不是当前回合，无需暂停
	}

	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	// 暂停计时器，计算剩余时间
	if gs.turnTimer != nil {
		gs.turnTimer.Stop()
		gs.remainingTime = time.Until(gs.timerStartTime.Add(gs.remainingTime))
		if gs.remainingTime < 0 {
			gs.remainingTime = 0
		}
		gs.turnTimer = nil
	}

	gs.turnID++ // Invalidate a turn timeout that raced with pause.
	// 启动离线等待计时器
	offlineTimeout := gs.gameConfig.OfflineWaitTimeoutDuration()
	turnID := gs.turnID
	gs.offlineWaitTimer = time.AfterFunc(offlineTimeout, func() { gs.handleTurnTimeout(turnID, "offline") })

	log.Printf("⏸️ 玩家 %s 离线，暂停计时等待重连 (%v)", gs.players[playerIdx].Name, offlineTimeout)
}

// PlayerOnline 玩家上线
func (gs *GameSession) PlayerOnline(playerID string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	gs.playerOnlineLocked(playerID)
}

func (gs *GameSession) playerOnlineLocked(playerID string) {
	// 找到玩家
	playerIdx := -1
	for i, p := range gs.players {
		if p.ID == playerID {
			if !p.IsOffline {
				return
			}
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

	// 检查是否是当前回合玩家，如果是则恢复计时器
	isBidding := gs.state == GameStateBidding && gs.currentBidder == playerIdx
	isPlaying := gs.state == GameStatePlaying && gs.currentPlayer == playerIdx

	if !isBidding && !isPlaying {
		return
	}

	if gs.turnTimer != nil || gs.offlineWaitTimer == nil {
		return
	}

	// 取消离线等待计时器
	if gs.offlineWaitTimer != nil {
		gs.offlineWaitTimer.Stop()
		gs.offlineWaitTimer = nil
	}

	gs.turnID++ // Invalidate any callback already queued before reconnection.

	// 恢复计时器
	if gs.remainingTime >= 0 {
		gs.timerStartTime = time.Now()
		turnID := gs.turnID
		gs.turnTimer = time.AfterFunc(gs.remainingTime, func() { gs.handleTurnTimeout(turnID, "timeout") })
		log.Printf("▶️ 玩家 %s 重连，恢复计时 (剩余 %v)", gs.players[playerIdx].Name, gs.remainingTime)
	}
}

// handleOfflineTimeout 离线超时处理
func (gs *GameSession) handleOfflineTimeout(playerID string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.state == GameStateBidding && gs.players[gs.currentBidder].ID == playerID {
		_ = gs.handleBidLocked(playerID, false, "offline")
	} else if gs.state == GameStatePlaying && gs.players[gs.currentPlayer].ID == playerID {
		gs.playTimeoutLocked("offline")
	}
}
