package room

import (
	"log"
	"math/rand/v2"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

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

	room.mu.Lock()
	player, exists := room.players[client.GetID()]
	if !exists || player == nil || player.Client != client {
		room.mu.Unlock()
		rm.mu.Unlock()
		return
	}
	player.Client = nil

	allOffline := true
	for _, player := range room.players {
		if player != nil && player.Client != nil {
			allOffline = false
		}
	}
	recipients := room.snapshotRecipientsLocked("")

	if allOffline {
		log.Printf("🧹 房间 %s 所有玩家已断开连接，清理房间", roomCode)
		room.state = RoomStateEnded
		delete(rm.rooms, roomCode)
	}
	room.mu.Unlock()
	rm.mu.Unlock()

	if allOffline {
		rm.deleteRoomAsync(roomCode)
		return
	}

	sendToRecipients(recipients, codec.MustNewMessage(protocol.MsgPlayerOffline, protocol.PlayerOfflinePayload{
		PlayerID:   client.GetID(),
		PlayerName: client.GetName(),
		Timeout:    rm.gameConfig.OfflineWaitTimeout,
	}))

	log.Printf("📴 玩家 %s 在房间 %s 中掉线", client.GetName(), roomCode)
}

// ReconnectPlayer 玩家重连到房间
func (rm *RoomManager) ReconnectPlayer(oldPlayerID, roomCode string, newClient types.ClientInterface) error {
	if roomCode == "" {
		return nil // 不在房间中，无需重连
	}
	if newClient.GetID() != oldPlayerID {
		return apperrors.ErrNotInRoom
	}
	playerName, isBot := newClient.GetName(), newClient.IsBot()

	rm.mu.RLock()
	room, exists := rm.rooms[roomCode]
	if !exists {
		rm.mu.RUnlock()
		return apperrors.ErrRoomNotFound
	}

	room.mu.Lock()
	player, exists := room.players[oldPlayerID]
	if !exists || player == nil {
		room.mu.Unlock()
		rm.mu.RUnlock()
		return apperrors.ErrNotInRoom
	}

	player.Name = playerName
	player.IsBot = isBot
	newClient.SetRoom(roomCode)
	player.Client = newClient
	recipients := room.snapshotRecipientsLocked(oldPlayerID)
	room.mu.Unlock()
	rm.mu.RUnlock()
	sendToRecipients(recipients, codec.MustNewMessage(protocol.MsgPlayerOnline, protocol.PlayerOnlinePayload{
		PlayerID:   oldPlayerID,
		PlayerName: playerName,
	}))

	log.Printf("📶 玩家 %s 重连到房间 %s", playerName, roomCode)

	return nil
}

// generateRoomCode 生成房间号
func (rm *RoomManager) generateRoomCode() string {
	for {
		code := make([]byte, roomCodeLength)
		for i := range code {
			code[i] = roomCodeChars[rand.IntN(len(roomCodeChars))]
		}
		codeStr := string(code)
		if _, exists := rm.rooms[codeStr]; !exists {
			return codeStr
		}
	}
}

// cleanupLoop 定期清理超时房间
func (rm *RoomManager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rm.cleanup()
	}
}

// cleanup 清理超时房间
func (rm *RoomManager) cleanup() {
	rm.mu.Lock()
	now := time.Now()
	type expiredRoom struct {
		code       string
		recipients []types.ClientInterface
	}
	expired := make([]expiredRoom, 0)

	for code, room := range rm.rooms {
		room.mu.Lock()
		if room.state == RoomStateWaiting && now.Sub(room.CreatedAt) > rm.roomTimeout {
			room.state = RoomStateEnded
			for _, player := range room.players {
				if player != nil && player.Client != nil {
					player.Client.SetRoom("")
				}
			}
			expired = append(expired, expiredRoom{
				code:       code,
				recipients: room.snapshotRecipientsLocked(""),
			})
			delete(rm.rooms, code)
		}
		room.mu.Unlock()
	}
	rm.mu.Unlock()

	for _, removed := range expired {
		sendToRecipients(removed.recipients, codec.NewErrorMessageWithText(protocol.ErrCodeUnknown, "房间超时已关闭"))
		rm.deleteRoomAsync(removed.code)
		log.Printf("🏠 房间 %s 超时已清理", removed.code)
	}
}
