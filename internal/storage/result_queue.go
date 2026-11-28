package storage

import (
	"context"
	"log"
	"time"
)

// GameResult is immutable once queued. Callers must enqueue before releasing a
// player's seat, so successive games enter the queue in completion order.
type GameResult struct {
	GameID     string
	PlayerID   string
	PlayerName string
	IsLandlord bool
	IsWinner   bool
}

type queuedResult struct {
	GameResult
	deadline time.Time
}

// QueueGameResult never waits for Redis. One worker per player owns both initial
// writes and retries; later games cannot overtake a failed or ambiguous write.
// The queue is process-local and each result retains the existing 10m lifetime.
func (lm *LeaderboardManager) QueueGameResult(result GameResult) {
	lm.resultsMu.Lock()
	defer lm.resultsMu.Unlock()
	if lm.results == nil {
		lm.results = make(map[string][]queuedResult)
	}
	queue := lm.results[result.PlayerID]
	lm.results[result.PlayerID] = append(queue, queuedResult{result, time.Now().Add(10 * time.Minute)})
	if len(queue) == 0 {
		go lm.flushPlayerResults(result.PlayerID)
	}
}

func (lm *LeaderboardManager) flushPlayerResults(playerID string) {
	for {
		lm.resultsMu.Lock()
		result := lm.results[playerID][0]
		lm.resultsMu.Unlock()
		lm.writeQueuedResult(result)
		lm.resultsMu.Lock()
		queue := lm.results[playerID]
		queue[0] = queuedResult{}
		if len(queue) == 1 {
			delete(lm.results, playerID)
			lm.resultsMu.Unlock()
			return
		}
		lm.results[playerID] = queue[1:]
		lm.resultsMu.Unlock()
	}
}

func (lm *LeaderboardManager) writeQueuedResult(result queuedResult) {
	ctx, cancel := context.WithDeadline(context.Background(), result.deadline)
	defer cancel()
	for ctx.Err() == nil {
		attempt, stop := context.WithTimeout(ctx, 2*time.Second)
		err := lm.RecordGameResultOnce(attempt, result.GameID, result.PlayerID, result.PlayerName, result.IsLandlord, result.IsWinner)
		stop()
		if err == nil {
			return
		}
		log.Printf("战绩写入失败，将按顺序重试，game=%s player=%s: %v", result.GameID, result.PlayerID, err)
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	log.Printf("战绩重试超时，game=%s player=%s", result.GameID, result.PlayerID)
}
