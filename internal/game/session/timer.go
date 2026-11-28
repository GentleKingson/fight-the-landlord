package session

import (
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
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
	gs.timerMode = "turn"
	if !gs.inTransition {
		gs.turnTimer = time.AfterFunc(bidTimeout, func() { gs.handleTurnTimeout(turnID, "timeout") })
	}
}

func (gs *GameSession) startPlayTimer() {
	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	turnTimeout := gs.gameConfig.TurnTimeoutDuration()
	gs.timerStartTime = time.Now()
	gs.remainingTime = turnTimeout
	turnID := gs.turnID
	gs.timerMode = "turn"
	if !gs.inTransition {
		gs.turnTimer = time.AfterFunc(turnTimeout, func() { gs.handleTurnTimeout(turnID, "timeout") })
	}
}

func (gs *GameSession) handleTurnTimeout(turnID uint64, source string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if turnID != gs.turnID || gs.stopped || gs.pending != nil {
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
	if gs.pending != nil || gs.stopped {
		return
	}
	p := gs.players[gs.currentPlayer]
	opponentHand := gs.lastPlayedHand
	if gs.lastPlayerIdx == gs.currentPlayer {
		opponentHand = rule.ParsedHand{}
	}
	cards := rule.FindSmallestBeatingCards(p.Hand, opponentHand)
	gs.applyTimeoutPlayLocked(cards, source)
}

// applyTimeoutPlayLocked falls back to a legal action if the suggested play is rejected.
func (gs *GameSession) applyTimeoutPlayLocked(cards []card.Card, source string) {
	p := gs.players[gs.currentPlayer]
	if cards != nil {
		if err := gs.handlePlayCardsLocked(p.ID, convert.CardsToInfos(cards), source); err == nil {
			return
		} else {
			log.Printf("自动出牌失败，尝试兜底动作: %v", err)
		}
	}
	if gs.pending != nil {
		return
	}
	if !gs.lastPlayedHand.IsEmpty() && gs.lastPlayerIdx != gs.currentPlayer {
		if err := gs.handlePassLocked(p.ID, source); err == nil {
			return
		}
	} else if len(p.Hand) > 0 {
		if err := gs.handlePlayCardsLocked(p.ID, convert.CardsToInfos(p.Hand[len(p.Hand)-1:]), source); err == nil {
			return
		}
	}
	if gs.pending != nil {
		return
	}
	// Keep a live timer even if an unexpected invalid state prevents the fallback.
	gs.stopTimer()
	gs.notifyPlayTurn()
}

func (gs *GameSession) stopTimer() {
	gs.timerMode = ""
	gs.cancelBotTurn()
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

// StopAllTimers closes the session and rejects in-flight decisions after cleanup.
func (gs *GameSession) StopAllTimers() {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	gs.stopped = true
	if gs.retryTimer != nil {
		gs.retryTimer.Stop()
	}
	gs.stopDealTimer()
	gs.state = GameStateEnded
	gs.turnID++
	gs.stopTimer()
	gs.finishRecording("aborted", nil)
}

// --- 离线处理 ---

// PlayerOffline 玩家离线
func (gs *GameSession) PlayerOffline(playerID string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	observedAt := time.Now()
	if gs.pending != nil {
		gs.presence = append(gs.presence, presenceChange{id: playerID, at: observedAt})
		return
	}
	_ = gs.transition(func() error { gs.playerOfflineLocked(playerID, observedAt); return nil })
}

func (gs *GameSession) playerOfflineLocked(playerID string, observedAt time.Time) {
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
	gs.updateAllOfflineLocked(observedAt)

	// 检查是否是当前回合玩家
	isBidding := gs.state == GameStateBidding && gs.currentBidder == playerIdx
	isPlaying := gs.state == GameStatePlaying && gs.currentPlayer == playerIdx

	if !isBidding && !isPlaying {
		return // 不是当前回合，无需暂停
	}

	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()

	// 暂停计时器，计算剩余时间
	if gs.turnTimer != nil || gs.timerMode == "turn" {
		if gs.turnTimer != nil {
			gs.turnTimer.Stop()
		}
		gs.remainingTime = gs.timerStartTime.Add(gs.remainingTime).Sub(observedAt)
		if gs.remainingTime < 0 {
			gs.remainingTime = 0
		}
		gs.turnTimer = nil
	}

	gs.turnID++ // Invalidate a turn timeout that raced with pause.
	gs.cancelBotTurn()
	// 启动离线等待计时器
	offlineTimeout := gs.gameConfig.OfflineWaitTimeoutDuration()
	turnID := gs.turnID
	gs.timerMode, gs.offlineDeadline = "offline", observedAt.Add(offlineTimeout)
	if !gs.inTransition {
		gs.offlineWaitTimer = time.AfterFunc(offlineTimeout, func() { gs.handleTurnTimeout(turnID, "offline") })
	}

	log.Printf("⏸️ 玩家 %s 离线，暂停计时等待重连 (%v)", gs.players[playerIdx].Name, offlineTimeout)
}

// PlayerOnline 玩家上线
func (gs *GameSession) PlayerOnline(playerID string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	gs.playerOnlineLocked(playerID)
}

func (gs *GameSession) playerOnlineLocked(playerID string) {
	if gs.pending != nil {
		gs.presence = append(gs.presence, presenceChange{id: playerID, online: true})
		return
	}
	if gs.store != nil && !gs.inTransition {
		_ = gs.transition(func() error { gs.playerOnlineLocked(playerID); return nil })
		return
	}
	playerIdx := gs.markPlayerOnline(playerID)
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

	if gs.timerMode != "offline" {
		return
	}

	// 取消离线等待计时器
	if gs.offlineWaitTimer != nil {
		gs.offlineWaitTimer.Stop()
		gs.offlineWaitTimer = nil
	}

	gs.turnID++ // Invalidate any callback already queued before reconnection.
	gs.cancelBotTurn()

	// 恢复计时器
	if gs.remainingTime >= 0 {
		gs.timerStartTime = time.Now()
		turnID := gs.turnID
		gs.timerMode = "turn"
		if !gs.inTransition {
			gs.turnTimer = time.AfterFunc(gs.remainingTime, func() { gs.handleTurnTimeout(turnID, "timeout") })
		}
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

func (gs *GameSession) markPlayerOnline(playerID string) int {
	for i, p := range gs.players {
		if p.ID != playerID {
			continue
		}
		if !p.IsOffline {
			return -1
		}
		p.IsOffline = false
		gs.allOfflineAt = time.Time{}
		return i
	}
	return -1
}
