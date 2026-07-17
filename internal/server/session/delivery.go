package session

import (
	"context"
	"log"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

type pendingDelivery struct {
	playerID       string
	resultPlayerID string
	message        *protocol.Message
}

type pendingGameResult struct {
	gameID     string
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

func (gs *GameSession) queueCommandBroadcastLocked(playerID string, message *protocol.Message) {
	gs.pendingDeliveries = append(gs.pendingDeliveries, pendingDelivery{
		resultPlayerID: playerID,
		message:        message,
	})
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
		gs.dispatchPendingDelivery(delivery)
	}
	if work.resetRoom {
		// Reopen rematch admission immediately after terminal delivery. The
		// quiescence lease remains held through leaderboard settlement below.
		gs.room.ResetAfterGame()
	}

	leaderboard := gs.leaderboard
	if leaderboard != nil && leaderboard.IsReady() {
		for _, result := range work.results {
			if err := leaderboard.RecordGameResult(
				context.Background(),
				result.gameID,
				result.playerID,
				result.playerName,
				result.isLandlord,
				result.isWinner,
			); err != nil {
				log.Printf("记录游戏结果失败: %v", err)
			}
		}
	}
	if work.resetRoom {
		// Restart safety waits for both terminal delivery and synchronous
		// leaderboard settlement without adding Redis latency to rematch Ready.
		gs.releaseQuiescence()
	}
}

func (gs *GameSession) dispatchPendingDelivery(delivery pendingDelivery) {
	if delivery.playerID == "" {
		gs.dispatchBroadcastDelivery(delivery)
		return
	}
	client, online := gs.room.PrivateRecipient(delivery.playerID)
	if !online {
		return
	}
	var err error
	if manager := gs.roomManager.Load(); manager != nil {
		_, err = manager.SendIfCurrentMember(gs.room, delivery.playerID, client, delivery.message)
	} else {
		_, err = types.SendMessageIfIdentity(client, delivery.playerID, gs.room.Code, delivery.message)
	}
	if err != nil {
		log.Printf("发送玩家 %s 的私有游戏消息失败: %v", delivery.playerID, err)
	}
}

func (gs *GameSession) dispatchBroadcastDelivery(delivery pendingDelivery) {
	if manager := gs.roomManager.Load(); manager != nil {
		if delivery.resultPlayerID != "" {
			manager.BroadcastCommandResultIfCurrentRoom(gs.room, delivery.resultPlayerID, delivery.message)
			return
		}
		if delivery.message.Type == protocol.MsgGameStart {
			manager.BroadcastBuiltIfCurrentRoom(gs.room, func() *protocol.Message {
				message := codec.MustNewMessage(protocol.MsgGameStart, protocol.GameStartPayload{
					Players: gs.room.GetAllPlayersInfo(),
				})
				message.Event = delivery.message.Event
				return message
			})
			return
		}
		manager.BroadcastIfCurrentRoom(gs.room, delivery.message)
		return
	}
	if delivery.resultPlayerID != "" {
		gs.room.BroadcastCommandResult(delivery.resultPlayerID, delivery.message)
		return
	}
	gs.room.Broadcast(delivery.message)
}
