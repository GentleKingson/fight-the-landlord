package room

import (
	"errors"
	"log"
	"math/rand/v2"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

var ErrReconnectResponseDelivery = errors.New("reconnect response delivery failed")

// SetAllPlayersReady 设置所有玩家准备状态
func (r *Room) SetAllPlayersReady() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, player := range r.players {
		if player != nil {
			player.Ready = true
		}
	}
}

// ResetAfterGame keeps the room membership intact while returning it to the
// ready-up state. The completed GameSession remains owned by the server until
// all players ready up and the room's start callback replaces it.
func (r *Room) ResetAfterGame() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.state = RoomStateWaiting
	r.CreatedAt = time.Now()
	for _, player := range r.players {
		if player == nil {
			continue
		}
		player.Ready = player.IsBot
		player.IsLandlord = false
	}
}

// NotifyPlayerOffline 通知房间内其他玩家某个玩家掉线
func (rm *RoomManager) NotifyPlayerOffline(client types.ClientInterface) {
	roomCode := client.GetRoom()
	if roomCode == "" {
		return
	}

	rm.mu.Lock()
	room, exists := rm.rooms[roomCode]
	if !exists {
		rm.mu.Unlock()
		return
	}
	room.publishMu.Lock()

	room.mu.Lock()
	playerID := client.GetID()
	player, exists := room.players[playerID]
	if !exists || player == nil || player.Client != client {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.Unlock()
		return
	}
	allOffline := true
	for _, current := range room.players {
		if current != nil && current.Client != nil && current.Client != client {
			allOffline = false
		}
	}
	playerName := player.Name
	var recipients []roomRecipient
	var removal roomRemovalDispatch
	if allOffline {
		log.Printf("🧹 房间 %s 所有玩家已断开连接，清理房间", roomCode)
		removal, _ = rm.removePublishedRoomLocked(room, RoomRemovalAllOffline)
	} else {
		player.Client = nil
		recipients = room.snapshotDeliveryRecipientsLocked("")
	}
	presenceCallback := rm.onPresence
	room.mu.Unlock()
	rm.mu.Unlock()
	if presenceCallback != nil {
		presenceCallback(room, playerID, false)
	}

	if allOffline {
		room.publishMu.Unlock()
		rm.dispatchRoomRemoval(removal)
		return
	}

	rm.sendToCurrentRoomPublished(room, recipients, codec.MustNewMessage(protocol.MsgPlayerOffline, protocol.PlayerOfflinePayload{
		PlayerID:   playerID,
		PlayerName: playerName,
		Timeout:    rm.gameConfig.OfflineWaitTimeout,
	}))
	room.publishMu.Unlock()

	log.Printf("📴 玩家 %s 在房间 %s 中掉线", playerName, roomCode)
}

// ReconnectPlayer 玩家重连到房间
func (rm *RoomManager) ReconnectPlayer(oldPlayerID, roomCode string, newClient types.ClientInterface) error {
	_, _, err := rm.ReconnectPlayerWithResponse(oldPlayerID, roomCode, newClient, nil)
	return err
}

// ReconnectPlayerWithResponse attaches one physical client, updates the game
// presence projection, publishes PlayerOnline, and optionally enqueues the
// authoritative reconnect snapshot under one room publication boundary.
func (rm *RoomManager) ReconnectPlayerWithResponse(
	oldPlayerID, roomCode string,
	newClient types.ClientInterface,
	buildResponse func(*Room) *protocol.Message,
) (*Room, bool, error) {
	if roomCode == "" {
		return nil, false, nil // 不在房间中，无需重连
	}
	if newClient.GetID() != oldPlayerID {
		return nil, false, apperrors.ErrNotInRoom
	}
	playerName, isBot := newClient.GetName(), newClient.IsBot()

	rm.mu.RLock()
	room, exists := rm.rooms[roomCode]
	if !exists {
		rm.mu.RUnlock()
		return nil, false, apperrors.ErrRoomNotFound
	}
	room.publishMu.Lock()

	room.mu.Lock()
	player, exists := room.players[oldPlayerID]
	if !exists || player == nil {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.RUnlock()
		return nil, false, apperrors.ErrNotInRoom
	}

	if !types.CompareAndSetRoom(newClient, oldPlayerID, roomCode, roomCode) &&
		!types.CompareAndSetRoom(newClient, oldPlayerID, "", roomCode) {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.RUnlock()
		return nil, false, apperrors.ErrNotInRoom
	}
	previousName := player.Name
	previousIsBot := player.IsBot
	previousClient := player.Client
	player.Name = playerName
	player.IsBot = isBot
	player.Client = newClient
	recipients := room.snapshotDeliveryRecipientsLocked(oldPlayerID)
	presenceCallback := rm.onPresence
	room.mu.Unlock()
	rm.mu.RUnlock()
	responseSent, sendErr := rm.sendReconnectResponsePublished(room, oldPlayerID, newClient, buildResponse)
	if sendErr != nil {
		rollbackRoomReconnect(room, oldPlayerID, player, newClient, previousName, previousIsBot, previousClient)
		room.publishMu.Unlock()
		return nil, false, sendErr
	}
	if presenceCallback != nil {
		presenceCallback(room, oldPlayerID, true)
	}
	rm.sendToCurrentRoomPublished(room, recipients, codec.MustNewMessage(protocol.MsgPlayerOnline, protocol.PlayerOnlinePayload{
		PlayerID:   oldPlayerID,
		PlayerName: playerName,
	}))
	room.publishMu.Unlock()

	log.Printf("📶 玩家 %s 重连到房间 %s", playerName, roomCode)

	return room, responseSent, nil
}

func (rm *RoomManager) sendReconnectResponsePublished(
	room *Room,
	playerID string,
	client types.ClientInterface,
	buildResponse func(*Room) *protocol.Message,
) (bool, error) {
	if buildResponse == nil {
		return false, nil
	}
	response := buildResponse(room)
	if response == nil {
		return false, nil
	}
	sent, err := rm.sendCommandResultIfCurrentMemberPublished(room, playerID, client, response)
	if err != nil {
		return false, errors.Join(ErrReconnectResponseDelivery, err)
	}
	if !sent {
		return false, ErrReconnectResponseDelivery
	}
	return true, nil
}

func rollbackRoomReconnect(
	room *Room,
	playerID string,
	player *RoomPlayer,
	newClient types.ClientInterface,
	previousName string,
	previousIsBot bool,
	previousClient types.ClientInterface,
) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if current := room.players[playerID]; current == player && current.Client == newClient {
		current.Name = previousName
		current.IsBot = previousIsBot
		current.Client = previousClient
	}
}

// SetOnPresenceChanged installs a callback that mirrors exact room presence
// into the registered GameSession before the corresponding wire event.
func (rm *RoomManager) SetOnPresenceChanged(callback func(*Room, string, bool)) {
	rm.mu.Lock()
	rm.onPresence = callback
	rm.mu.Unlock()
}

// generateRoomCode 生成房间号
func (rm *RoomManager) generateRoomCode() string {
	for {
		var codeStr string
		if rm.roomCodeFunc != nil {
			codeStr = rm.roomCodeFunc()
		} else {
			code := make([]byte, roomCodeLength)
			for i := range code {
				code[i] = roomCodeChars[rand.IntN(len(roomCodeChars))]
			}
			codeStr = string(code)
		}
		_, published := rm.rooms[codeStr]
		_, pending := rm.pendingRooms[codeStr]
		_, retiring := rm.retiringRooms[codeStr]
		if !published && !pending && !retiring {
			return codeStr
		}
	}
}

// cleanupLoop 定期清理超时房间
func (rm *RoomManager) cleanupLoop() {
	ticker := time.NewTicker(rm.cleanupPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rm.cleanup()
		case <-rm.ctx.Done():
			return
		}
	}
}

// Close stops background work, retires every published room, and waits for
// cleanup and persistence workers to exit. It is safe to call repeatedly.
func (rm *RoomManager) Close() error {
	if rm == nil {
		return nil
	}
	rm.closeOnce.Do(func() {
		rm.mu.Lock()
		rm.closed = true
		rm.mu.Unlock()
		if rm.cancel != nil {
			rm.cancel()
		}
		rm.cleanupWG.Wait()

		dispatches, pendingPlayers := rm.removeAllForShutdown()
		for _, dispatch := range dispatches {
			rm.dispatchRoomRemoval(dispatch)
		}
		for _, player := range pendingPlayers {
			closeRemovedBot(player.Client)
		}

		rm.persistenceMu.Lock()
		rm.persistenceClosed = true
		// Saves no longer describe a published room. Preserve deletes so current
		// workers can finish retirement bookkeeping before they exit.
		for _, queue := range rm.persistenceQueues {
			queue.pendingSave = nil
			queue.pendingAfterDelete = nil
		}
		rm.persistenceMu.Unlock()
		rm.persistenceWG.Wait()
	})
	return nil
}

func (rm *RoomManager) removeAllForShutdown() ([]roomRemovalDispatch, []PlayerSnapshot) {
	rm.mu.Lock()
	dispatches := make([]roomRemovalDispatch, 0, len(rm.rooms))
	for _, gameRoom := range rm.rooms {
		gameRoom.publishMu.Lock()
		gameRoom.mu.Lock()
		dispatch, removed := rm.removePublishedRoomLocked(gameRoom, RoomRemovalShutdown)
		gameRoom.mu.Unlock()
		gameRoom.publishMu.Unlock()
		if removed {
			dispatches = append(dispatches, dispatch)
		}
	}

	pendingPlayers := make([]PlayerSnapshot, 0)
	for code, transaction := range rm.pendingRooms {
		if transaction == nil || transaction.room == nil {
			delete(rm.pendingRooms, code)
			continue
		}
		transaction.room.mu.Lock()
		pendingPlayers = append(pendingPlayers, transaction.room.snapshotPlayersLocked()...)
		transaction.room.state = RoomStateEnded
		transaction.state = matchRoomEnded
		transaction.room.mu.Unlock()
		delete(rm.pendingRooms, code)
	}
	rm.mu.Unlock()
	return dispatches, pendingPlayers
}

// cleanup 清理超时房间
func (rm *RoomManager) cleanup() {
	rm.mu.Lock()
	now := time.Now()
	type expiredRoom struct {
		removal roomRemovalDispatch
	}
	expired := make([]expiredRoom, 0)

	for _, room := range rm.rooms {
		room.publishMu.Lock()
		room.mu.Lock()
		if room.state == RoomStateWaiting && now.Sub(room.CreatedAt) > rm.roomTimeout {
			removal, removed := rm.removePublishedRoomLocked(room, RoomRemovalTimeout)
			if !removed {
				room.mu.Unlock()
				room.publishMu.Unlock()
				continue
			}
			expired = append(expired, expiredRoom{removal: removal})
		}
		room.mu.Unlock()
		room.publishMu.Unlock()
	}
	rm.mu.Unlock()

	for _, removed := range expired {
		rm.dispatchRoomRemoval(removed.removal)
		sendRemovalMessageIfUnbound(removed.removal.removal, codec.NewErrorMessageWithText(protocol.ErrCodeUnknown, "房间超时已关闭"))
	}
}

func sendRemovalMessageIfUnbound(removal RoomRemoval, message *protocol.Message) {
	for _, player := range removal.Players {
		if player.Client == nil || player.IsBot {
			continue
		}
		if _, err := types.SendMessageIfIdentity(player.Client, player.ID, "", message); err != nil {
			log.Printf("发送房间 %s 移除通知给玩家 %s 失败: %v", removal.Code, player.ID, err)
		}
	}
}
