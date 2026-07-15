package session

import (
	"context"
	"log"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

type pendingDelivery struct {
	playerID string
	message  *protocol.Message
}

type pendingGameResult struct {
	playerID   string
	playerName string
	isLandlord bool
	isWinner   bool
}

type pendingWork struct {
	deliveries []pendingDelivery
	results    []pendingGameResult
	resetRoom  bool
}

// GameSession mutations run under gs.mu, but delivery never does. This keeps
// arbitrary Client implementations and storage I/O outside both gs.mu and
// Room.mu while preserving the event order committed by the game state.
func (gs *GameSession) queueBroadcastLocked(message *protocol.Message) {
	gs.pendingDeliveries = append(gs.pendingDeliveries, pendingDelivery{message: message})
}

func (gs *GameSession) queuePrivateLocked(playerID string, message *protocol.Message) {
	gs.pendingDeliveries = append(gs.pendingDeliveries, pendingDelivery{
		playerID: playerID,
		message:  message,
	})
}

func (gs *GameSession) takePendingWorkLocked() pendingWork {
	work := pendingWork{
		deliveries: gs.pendingDeliveries,
		results:    gs.pendingResults,
		resetRoom:  gs.pendingRoomReset,
	}
	gs.pendingDeliveries = nil
	gs.pendingResults = nil
	gs.pendingRoomReset = false
	return work
}

func (gs *GameSession) dispatchPendingWork(work pendingWork) {
	for _, delivery := range work.deliveries {
		if delivery.playerID == "" {
			gs.room.Broadcast(delivery.message)
			continue
		}
		client, online := gs.room.PrivateRecipient(delivery.playerID)
		if !online {
			continue
		}
		if err := client.SendMessage(delivery.message); err != nil {
			log.Printf("发送玩家 %s 的私有游戏消息失败: %v", delivery.playerID, err)
		}
	}
	if work.resetRoom {
		gs.room.ResetAfterGame()
	}

	leaderboard := gs.leaderboard
	if leaderboard == nil || !leaderboard.IsReady() {
		return
	}
	for _, result := range work.results {
		if err := leaderboard.RecordGameResult(
			context.Background(),
			result.playerID,
			result.playerName,
			result.isLandlord,
			result.isWinner,
		); err != nil {
			log.Printf("记录游戏结果失败: %v", err)
		}
	}
}
