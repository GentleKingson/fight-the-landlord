package session

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

var ErrPersistenceUnavailable = errors.New("牌局正在等待可靠存储，请稍后重试或重连")

type presenceChange struct {
	id     string
	online bool
	at     time.Time
}

// transition holds gs.mu throughout. No accepted-action notification or room
// release is visible until the exact candidate has committed in Redis.
func (gs *GameSession) transition(action func() error) error {
	if gs.store == nil || gs.inTransition {
		return action()
	}
	if gs.pending != nil || gs.stopped {
		return ErrPersistenceUnavailable
	}
	gs.inTransition = true
	err := action()
	gs.inTransition = false
	if err != nil {
		gs.effects = nil
		return err
	}
	data, err := json.Marshal(gs.snapshotLocked())
	if err != nil {
		return err
	}
	record := &storage.GameRecord{Version: 2, ID: gs.gameID, RoomCode: gs.room.Code,
		Revision: gs.revision + 1, SavedAt: time.Now(), State: data, Ended: gs.state == GameStateEnded}
	if record.Ended {
		record.ExpiresAt = gs.endedAt.Add(gs.gameConfig.ResultRetentionDuration())
		record.Results = gs.settlementResults()
	} else if !gs.allOfflineAt.IsZero() {
		record.ExpiresAt = gs.allOfflineAt.Add(gs.gameConfig.OfflineRoomRetentionDuration())
	}
	gs.pending = record
	return gs.commitPendingLocked()
}

func (gs *GameSession) commitPendingLocked() error {
	if gs.retryTimer != nil {
		gs.retryTimer.Stop()
		gs.retryTimer = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := gs.store.SaveGame(ctx, gs.pending)
	cancel()
	if err != nil {
		gs.stopPhysicalTimers()
		gs.cancelBotTurn()
		log.Printf("牌局持久化暂停 game=%s revision=%d: %v", gs.gameID, gs.pending.Revision, err)
		if !errors.Is(err, storage.ErrStaleGame) && !gs.stopped {
			gs.retryTimer = time.AfterFunc(time.Second, func() {
				gs.mu.Lock()
				defer gs.mu.Unlock()
				if gs.pending != nil && !gs.stopped {
					_ = gs.commitPendingLocked()
				}
			})
		}
		return ErrPersistenceUnavailable
	}
	gs.revision, gs.committed = gs.pending.Revision, gs.pending
	gs.pending = nil
	effects := gs.effects
	gs.effects = nil
	for _, effect := range effects {
		effect()
	}
	if len(gs.presence) > 0 {
		changes := gs.presence
		gs.presence = nil
		return gs.transition(func() error {
			for _, change := range changes {
				if change.online {
					gs.playerOnlineLocked(change.id)
				} else {
					gs.playerOfflineLocked(change.id, change.at)
				}
			}
			return nil
		})
	}
	gs.room.SyncRetentionTimes(gs.allOfflineAt, gs.endedAt)
	gs.armPersistentTimers()
	return nil
}

func (gs *GameSession) effect(effect func()) {
	if gs.inTransition {
		gs.effects = append(gs.effects, effect)
	} else {
		effect()
	}
}

func (gs *GameSession) broadcast(msg *protocol.Message) {
	gs.effect(func() { gs.room.Broadcast(msg) })
}

func (gs *GameSession) send(client types.ClientInterface, msg *protocol.Message) {
	gs.effect(func() { client.SendMessage(msg) })
}

func (gs *GameSession) setRoomState(state room.RoomState) {
	gs.effect(func() { gs.room.SetState(state) })
}

func (gs *GameSession) stopPhysicalTimers() {
	gs.stopDealTimer()
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

func (gs *GameSession) armPersistentTimers() {
	if gs.store == nil || gs.stopped {
		return
	}
	gs.stopPhysicalTimers()
	turn := gs.turnID
	switch gs.timerMode {
	case "deal":
		deal := gs.dealID
		gs.dealTimer = time.AfterFunc(max(0, time.Until(gs.dealEndAt)), func() { gs.finishDeal(deal) })
	case "turn":
		gs.turnTimer = time.AfterFunc(max(0, time.Until(gs.timerStartTime.Add(gs.remainingTime))), func() { gs.handleTurnTimeout(turn, "timeout") })
	case "offline":
		gs.offlineWaitTimer = time.AfterFunc(max(0, time.Until(gs.offlineDeadline)), func() { gs.handleTurnTimeout(turn, "offline") })
	}
}

func (gs *GameSession) settlementResults() []storage.GameResult {
	if gs.lastResult == nil {
		return nil
	}
	result, err := codec.ParsePayload[protocol.GameOverPayload](gs.lastResult)
	if err != nil {
		return nil
	}
	var results []storage.GameResult
	for _, p := range gs.players {
		if !gs.room.Players[p.ID].Client.IsBot() {
			results = append(results, storage.GameResult{GameID: gs.gameID, PlayerID: p.ID,
				PlayerName: p.Name, IsLandlord: p.IsLandlord, IsWinner: p.IsLandlord == result.IsLandlord})
		}
	}
	return results
}

// Suspend is used when stopping the process; it must not commit an ended game.
func (gs *GameSession) Suspend() {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	gs.stopped = true
	gs.stopPhysicalTimers()
	gs.cancelBotTurn()
	if gs.retryTimer != nil {
		gs.retryTimer.Stop()
	}
}

func (gs *GameSession) updateAllOfflineLocked(observedAt time.Time) {
	for _, p := range gs.players {
		if !gs.room.Players[p.ID].Client.IsBot() && !p.IsOffline {
			gs.allOfflineAt = time.Time{}
			return
		}
	}
	if gs.allOfflineAt.IsZero() {
		gs.allOfflineAt = observedAt
	}
}
