package room

import (
	"errors"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// --- Room 方法 ---

// PlayerClient 房间内玩家 ID 与客户端快照
type PlayerClient struct {
	ID     string
	Client types.ClientInterface
}

// Broadcast 广播消息给房间内所有玩家（线程安全，锁外发送）
func (r *Room) Broadcast(msg *protocol.Message) {
	r.mu.RLock()
	clients := make([]types.ClientInterface, 0, len(r.Players))
	for _, player := range r.Players {
		if player != nil && player.Client != nil {
			clients = append(clients, player.Client)
		}
	}
	r.mu.RUnlock()
	for _, client := range clients {
		client.SendMessage(msg)
	}
}

// broadcastLocked 调用者已持有 r.mu 时在锁内广播
func (r *Room) broadcastLocked(msg *protocol.Message) {
	for _, player := range r.Players {
		if player != nil && player.Client != nil {
			player.Client.SendMessage(msg)
		}
	}
}

// BroadcastExcept 广播消息给除指定玩家外的所有玩家（线程安全，锁外发送）
func (r *Room) BroadcastExcept(excludeID string, msg *protocol.Message) {
	r.mu.RLock()
	clients := make([]types.ClientInterface, 0, len(r.Players))
	for id, player := range r.Players {
		if id != excludeID && player != nil && player.Client != nil {
			clients = append(clients, player.Client)
		}
	}
	r.mu.RUnlock()
	for _, client := range clients {
		client.SendMessage(msg)
	}
}

// broadcastExceptLocked 调用者已持有 r.mu 时在锁内排除指定玩家广播
func (r *Room) broadcastExceptLocked(excludeID string, msg *protocol.Message) {
	for id, player := range r.Players {
		if id != excludeID && player != nil && player.Client != nil {
			player.Client.SendMessage(msg)
		}
	}
}

// SetLandlord 线程安全设置指定玩家的地主标记
func (r *Room) SetLandlord(playerID string, isLandlord bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p := r.Players[playerID]; p != nil {
		p.IsLandlord = isLandlord
	}
}

// ResetLandlords 线程安全重置房间内所有玩家的地主标记
func (r *Room) ResetLandlords() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.Players {
		if p != nil {
			p.IsLandlord = false
		}
	}
}

// GetClient 线程安全获取指定玩家的客户端引用
func (r *Room) GetClient(playerID string) types.ClientInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p := r.Players[playerID]; p != nil {
		return p.Client
	}
	return nil
}

// IsPlayerBot 线程安全判断指定玩家是否为机器人
func (r *Room) IsPlayerBot(playerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p := r.Players[playerID]; p != nil && p.Client != nil {
		return p.Client.IsBot()
	}
	return false
}

// GetPlayerClients 线程安全获取房间内所有玩家 ID 与客户端快照
func (r *Room) GetPlayerClients() []PlayerClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PlayerClient, 0, len(r.Players))
	for id, p := range r.Players {
		if p != nil && p.Client != nil {
			out = append(out, PlayerClient{ID: id, Client: p.Client})
		}
	}
	return out
}

// GetOrderedClients 线程安全按座位顺序获取玩家 ID 与客户端快照
func (r *Room) GetOrderedClients() []PlayerClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PlayerClient, 0, len(r.PlayerOrder))
	for _, id := range r.PlayerOrder {
		if p := r.Players[id]; p != nil && p.Client != nil {
			out = append(out, PlayerClient{ID: id, Client: p.Client})
		}
	}
	return out
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
	cardsCount := 0
	// 游戏会话由外部调用方管理，此处暂不传入
	return protocol.PlayerInfo{
		ID:         player.Client.GetID(),
		Name:       player.Client.GetName(),
		Seat:       player.Seat,
		Ready:      player.Ready,
		IsLandlord: player.IsLandlord,
		CardsCount: cardsCount,
		Online:     !player.Offline,
		IsBot:      player.Client.IsBot(),
	}
}

// GetAllPlayersInfo 获取所有玩家信息
func (r *Room) GetAllPlayersInfo() []protocol.PlayerInfo {
	infos := make([]protocol.PlayerInfo, 0, len(r.Players))
	for _, id := range r.PlayerOrder {
		infos = append(infos, r.GetPlayerInfo(id))
	}
	return infos
}

// GetOwnerID 线程安全获取房主 ID
func (r *Room) GetOwnerID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.OwnerID
}

// GetClients 线程安全获取房间内所有客户端列表
func (r *Room) GetClients() []types.ClientInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clients := make([]types.ClientInterface, 0, len(r.Players))
	for _, p := range r.Players {
		if p.Client != nil {
			clients = append(clients, p.Client)
		}
	}
	return clients
}

// BuildRoomCreatedPayload 线程安全构建房间创建响应
func (r *Room) BuildRoomCreatedPayload(playerID string) protocol.RoomCreatedPayload {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return protocol.RoomCreatedPayload{
		RoomCode: r.Code,
		Player:   r.GetPlayerInfo(playerID),
		OwnerID:  r.OwnerID,
	}
}

// BuildRoomJoinedPayload 线程安全构建加入房间响应
func (r *Room) BuildRoomJoinedPayload(playerID string) protocol.RoomJoinedPayload {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return protocol.RoomJoinedPayload{
		RoomCode: r.Code,
		Player:   r.GetPlayerInfo(playerID),
		Players:  r.GetAllPlayersInfo(),
		OwnerID:  r.OwnerID,
	}
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

	// 广播游戏开始
	r.broadcastLocked(codec.MustNewMessage(protocol.MsgGameStart, protocol.GameStartPayload{
		Players: r.GetAllPlayersInfo(),
	}))

	return nil
}
