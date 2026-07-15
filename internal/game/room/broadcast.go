package room

import (
	"errors"
	"log"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// --- Room 方法 ---

// Broadcast 广播消息给房间内所有玩家
func (r *Room) Broadcast(msg *protocol.Message) {
	sendToRecipients(r.SnapshotRecipients(), msg)
}

// broadcastExcept 广播消息给除指定玩家外的所有玩家
func (r *Room) BroadcastExcept(excludeID string, msg *protocol.Message) {
	r.mu.RLock()
	recipients := r.snapshotRecipientsLocked(excludeID)
	r.mu.RUnlock()
	sendToRecipients(recipients, msg)
}

// checkAllReady 检查是否所有玩家都准备好
func (r *Room) checkAllReadyLocked() bool {
	if len(r.players) < 3 {
		return false
	}
	for _, player := range r.players {
		if player == nil || !player.Ready {
			return false
		}
	}
	return true
}

func (r *Room) checkAllReady() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.checkAllReadyLocked()
}

// GetPlayerInfo 获取玩家信息
func (r *Room) GetPlayerInfo(playerID string) (protocol.PlayerInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.playerInfoLocked(playerID)
}

// GetAllPlayersInfo 获取所有玩家信息
func (r *Room) GetAllPlayersInfo() []protocol.PlayerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]protocol.PlayerInfo, 0, len(r.players))
	for _, id := range r.playerOrder {
		if info, ok := r.playerInfoLocked(id); ok {
			infos = append(infos, info)
		}
	}
	return infos
}

func (r *Room) playerInfoLocked(playerID string) (protocol.PlayerInfo, bool) {
	player, ok := r.players[playerID]
	if !ok || player == nil {
		return protocol.PlayerInfo{}, false
	}
	id := player.ID
	if id == "" {
		id = playerID
	}
	return protocol.PlayerInfo{
		ID:         id,
		Name:       player.Name,
		Seat:       player.Seat,
		Ready:      player.Ready,
		IsLandlord: player.IsLandlord,
		Online:     player.Client != nil,
		IsBot:      player.IsBot,
	}, true
}

// SnapshotPlayers returns ordered value copies of the current membership.
func (r *Room) SnapshotPlayers() []PlayerSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotPlayersLocked()
}

func (r *Room) snapshotPlayersLocked() []PlayerSnapshot {
	players := make([]PlayerSnapshot, 0, len(r.players))
	for _, id := range r.playerOrder {
		player, ok := r.players[id]
		if !ok || player == nil {
			continue
		}
		players = append(players, snapshotPlayer(player))
	}
	return players
}

func snapshotPlayer(player *RoomPlayer) PlayerSnapshot {
	return PlayerSnapshot{
		ID:         player.ID,
		Name:       player.Name,
		IsBot:      player.IsBot,
		Client:     player.Client,
		Seat:       player.Seat,
		Ready:      player.Ready,
		IsLandlord: player.IsLandlord,
	}
}

// SnapshotRecipients captures current non-nil delivery handles under the room
// lock so callers can perform delivery after the lock has been released.
func (r *Room) SnapshotRecipients() []types.ClientInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotRecipientsLocked("")
}

func (r *Room) snapshotRecipientsLocked(excludeID string) []types.ClientInterface {
	recipients := make([]types.ClientInterface, 0, len(r.players))
	for id, player := range r.players {
		if id != excludeID && player != nil && player.Client != nil {
			recipients = append(recipients, player.Client)
		}
	}
	return recipients
}

func sendToRecipients(recipients []types.ClientInterface, msg *protocol.Message) {
	for _, client := range recipients {
		if err := client.SendMessage(msg); err != nil {
			log.Printf("发送房间消息给玩家 %s 失败: %v", client.GetID(), err)
		}
	}
}

// State returns the authoritative room state.
func (r *Room) State() RoomState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// SetState changes the authoritative room state.
func (r *Room) SetState(state RoomState) {
	r.mu.Lock()
	r.state = state
	r.mu.Unlock()
}

// SetLandlord atomically clears any previous landlord and assigns playerID.
func (r *Room) SetLandlord(playerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	var landlord *RoomPlayer
	if playerID != "" {
		var ok bool
		landlord, ok = r.players[playerID]
		if !ok || landlord == nil {
			return false
		}
	}
	for _, player := range r.players {
		if player != nil {
			player.IsLandlord = false
		}
	}
	if landlord != nil {
		landlord.IsLandlord = true
	}
	return true
}

// AttachClient restores the delivery handle for an existing member.
func (r *Room) AttachClient(playerID string, client types.ClientInterface) bool {
	if client == nil || client.GetID() != playerID {
		return false
	}
	name, isBot := client.GetName(), client.IsBot()
	r.mu.Lock()
	defer r.mu.Unlock()
	player, ok := r.players[playerID]
	if !ok || player == nil {
		return false
	}
	player.Name = name
	player.IsBot = isBot
	player.Client = client
	return true
}

// DetachClient clears a member's delivery handle only when it still points to
// expected, preventing a stale disconnect from detaching a replacement.
func (r *Room) DetachClient(playerID string, expected types.ClientInterface) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	player, ok := r.players[playerID]
	if !ok || player == nil || player.Client != expected {
		return false
	}
	player.Client = nil
	return true
}

// RemovePlayer atomically removes a member and its seat-order entry.
func (r *Room) RemovePlayer(playerID string) (PlayerSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.removePlayerLocked(playerID)
}

func (r *Room) removePlayerLocked(playerID string) (PlayerSnapshot, bool) {
	player, ok := r.players[playerID]
	if !ok || player == nil {
		return PlayerSnapshot{}, false
	}
	removed := snapshotPlayer(player)
	delete(r.players, playerID)
	for i, id := range r.playerOrder {
		if id == playerID {
			r.playerOrder = append(r.playerOrder[:i], r.playerOrder[i+1:]...)
			break
		}
	}
	return removed, true
}

// PrivateRecipient returns the current non-nil delivery handle for playerID.
func (r *Room) PrivateRecipient(playerID string) (types.ClientInterface, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	player, ok := r.players[playerID]
	if !ok || player == nil || player.Client == nil {
		return nil, false
	}
	return player.Client, true
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
	if r.state != RoomStateWaiting || len(r.players) < 3 {
		return errors.New("cannot start game: room not ready or not enough players")
	}

	r.state = RoomStateReady
	return nil
}
