package room

import (
	"errors"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

// --- Room 方法 ---

// Broadcast 广播消息给房间内所有玩家
func (r *Room) Broadcast(msg *protocol.Message) {
	for _, player := range r.Players {
		if player != nil && player.Client != nil {
			player.Client.SendMessage(msg)
		}
	}
}

// broadcastExcept 广播消息给除指定玩家外的所有玩家
func (r *Room) BroadcastExcept(excludeID string, msg *protocol.Message) {
	for id, player := range r.Players {
		if id != excludeID && player != nil && player.Client != nil {
			player.Client.SendMessage(msg)
		}
	}
}

// checkAllReady 检查是否所有玩家都准备好
func (r *Room) checkAllReady() bool {
	if len(r.Players) < 3 {
		return false
	}
	for _, player := range r.Players {
		if !player.Ready {
			return false
		}
	}
	return true
}

// GetPlayerInfo 获取玩家信息
func (r *Room) GetPlayerInfo(playerID string) protocol.PlayerInfo {
	player := r.Players[playerID]
	client := player.Client
	cardsCount := 0
	// 游戏会话由外部调用方管理，此处暂不传入
	info := protocol.PlayerInfo{
		ID:         playerID,
		Seat:       player.Seat,
		Ready:      player.Ready,
		IsLandlord: player.IsLandlord,
		CardsCount: cardsCount,
		Online:     client != nil,
	}
	if client != nil {
		info.ID = client.GetID()
		info.Name = client.GetName()
		info.IsBot = client.IsBot()
	}
	return info
}

// GetAllPlayersInfo 获取所有玩家信息
func (r *Room) GetAllPlayersInfo() []protocol.PlayerInfo {
	infos := make([]protocol.PlayerInfo, 0, len(r.Players))
	for _, id := range r.PlayerOrder {
		infos = append(infos, r.GetPlayerInfo(id))
	}
	return infos
}

// StartGame 准备开始游戏（不创建GameSession，由外部管理）
// 注意：调用者负责保存到 Redis
func (r *Room) StartGame() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startGameLocked()
}

// startGameLocked 开始游戏（调用者已持有锁时使用）
func (r *Room) startGameLocked() error {
	if r.State != RoomStateWaiting || len(r.Players) < 3 {
		return errors.New("cannot start game: room not ready or not enough players")
	}

	r.State = RoomStateReady
	return nil
}
