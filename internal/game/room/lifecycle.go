package room

import (
	"context"
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
	for _, player := range r.Players {
		player.Ready = true
	}
}

// NotifyPlayerOffline 通知房间内其他玩家某个玩家掉线
func (rm *RoomManager) NotifyPlayerOffline(client types.ClientInterface) {
	roomCode := client.GetRoom()
	if roomCode == "" {
		return
	}

	rm.mu.RLock()
	room, exists := rm.rooms[roomCode]
	rm.mu.RUnlock()
	if !exists {
		return
	}

	room.mu.Lock()

	// 标记当前玩家为离线
	if player, exists := room.Players[client.GetID()]; exists {
		if player.Client != client {
			room.mu.Unlock()
			return
		}
		player.Offline = true
	}

	// 检查所有玩家是否都离线
	allOffline := true
	for _, player := range room.Players {
		if player.Client != nil && !player.Client.IsBot() && !player.Offline {
			allOffline = false
			// 通知其他在线玩家
			player.Client.SendMessage(codec.MustNewMessage(protocol.MsgPlayerOffline, protocol.PlayerOfflinePayload{
				PlayerID:   client.GetID(),
				PlayerName: client.GetName(),
				Timeout:    rm.gameConfig.OfflineWaitTimeout, // 从配置读取
			}))
		}
	}

	// Keep timers running: after the reconnect grace period, normal auto-play continues.
	if allOffline && room.AllOfflineAt.IsZero() {
		room.AllOfflineAt = time.Now()
	}

	// 如果游戏进行中，通知 GameSession 暂停该玩家的计时器（由外部调用者处理）
	room.mu.Unlock()

	log.Printf("📴 玩家 %s 在房间 %s 中掉线", client.GetName(), roomCode)
}

// ReconnectPlayer 玩家重连到房间
func (rm *RoomManager) ReconnectPlayer(oldClient, newClient types.ClientInterface) error {
	roomCode := oldClient.GetRoom()
	if roomCode == "" {
		return nil // 不在房间中，无需重连
	}

	rm.mu.RLock()
	room, exists := rm.rooms[roomCode]
	defer rm.mu.RUnlock()
	if !exists {
		return apperrors.ErrRoomNotFound
	}

	room.mu.Lock()
	if (room.State == RoomStateWaiting && time.Since(room.CreatedAt) > rm.roomTimeout) ||
		(room.State == RoomStateEnded && time.Since(room.EndedAt) > room.resultRetentionDuration(rm.gameConfig.ResultRetentionDuration())) ||
		(room.State != RoomStateEnded && !room.AllOfflineAt.IsZero() && time.Since(room.AllOfflineAt) > room.offlineRetentionDuration(rm.gameConfig.OfflineRoomRetentionDuration())) {
		room.mu.Unlock()
		return apperrors.ErrRoomNotFound
	}

	player, exists := room.Players[oldClient.GetID()]
	if !exists {
		room.mu.Unlock()
		return apperrors.ErrNotInRoom
	}

	// 更新客户端引用
	player.Client = newClient
	player.Offline = false
	room.AllOfflineAt = time.Time{}
	newClient.SetRoom(roomCode)

	// 通知其他玩家该玩家已上线
	for id, p := range room.Players {
		if id != newClient.GetID() && p.Client != nil {
			p.Client.SendMessage(codec.MustNewMessage(protocol.MsgPlayerOnline, protocol.PlayerOnlinePayload{
				PlayerID:   newClient.GetID(),
				PlayerName: newClient.GetName(),
			}))
		}
	}

	// 如果游戏进行中，通知 GameSession 恢复该玩家的计时器（由外部调用者处理）
	room.mu.Unlock()

	log.Printf("📶 玩家 %s 重连到房间 %s", newClient.GetName(), roomCode)

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
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		rm.cleanup()
	}
}

// endedRoomRetention allows a short reconnect window for the final result.
const endedRoomRetention = 2 * time.Minute

// cleanup removes expired waiting rooms and completed games from both registries.
func (rm *RoomManager) cleanup() {
	var removed []*Room
	rm.mu.Lock()
	now := time.Now()
	for code, r := range rm.rooms {
		r.mu.Lock()
		waitingExpired := r.State == RoomStateWaiting && now.Sub(r.CreatedAt) > rm.roomTimeout
		endedExpired := r.State == RoomStateEnded && now.Sub(r.EndedAt) > r.resultRetentionDuration(rm.gameConfig.ResultRetentionDuration())
		offlineExpired := r.State != RoomStateEnded && !r.AllOfflineAt.IsZero() && now.Sub(r.AllOfflineAt) > r.offlineRetentionDuration(rm.gameConfig.OfflineRoomRetentionDuration())
		if waitingExpired || endedExpired || offlineExpired {
			if waitingExpired {
				r.Broadcast(codec.NewErrorMessageWithText(protocol.ErrCodeUnknown, "房间超时已关闭"))
			}
			for _, p := range r.Players {
				// Players may already have joined another room after the result.
				if p.Client.GetRoom() == code {
					p.Client.SetRoom("")
				}
			}
			r.State = RoomStateEnded
			delete(rm.rooms, code)
			removed = append(removed, r)
		}
		r.mu.Unlock()
	}
	rm.mu.Unlock()
	// GameSession takes its own lock and may acquire the room lock: never call
	// into it while holding either RoomManager.mu or Room.mu.
	for _, r := range removed {
		rm.finishRemoval(r)
	}
}

func (rm *RoomManager) SetOnRoomRemoved(callback func(*Room)) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.onRoomRemoved = callback
}

func (rm *RoomManager) finishRemoval(r *Room) {
	rm.mu.RLock()
	callback := rm.onRoomRemoved
	rm.mu.RUnlock()
	if callback != nil {
		callback(r)
	}
	r.mu.RLock()
	for _, p := range r.Players {
		if p.Client.IsBot() {
			p.Client.Close()
		}
	}
	r.mu.RUnlock()
	if rm.redisStore != nil && rm.redisStore.IsReady() {
		go func() { _ = rm.redisStore.DeleteRoom(context.Background(), r.Code) }()
	}
	log.Printf("🏠 房间 %s 已清理", r.Code)
}

// SerializeForRedis 为Redis序列化准备数据（提供只读访问）
func (r *Room) SerializeForRedis(serialize func()) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	serialize()
}
