package room

import (
	"errors"
	"log"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

type roomRecipient struct {
	playerID string
	client   types.ClientInterface
}

// --- Room 方法 ---

// Broadcast 广播消息给房间内所有玩家
func (r *Room) Broadcast(msg *protocol.Message) {
	r.publishMu.Lock()
	r.mu.RLock()
	recipients := r.snapshotDeliveryRecipientsLocked("")
	r.mu.RUnlock()
	sendToRoomRecipients(r.Code, recipients, msg)
	r.publishMu.Unlock()
}

// BroadcastCommandResult is the manager-free equivalent used by focused
// GameSession tests and bots.
func (r *Room) BroadcastCommandResult(resultPlayerID string, msg *protocol.Message) {
	r.publishMu.Lock()
	r.mu.RLock()
	recipients := r.snapshotDeliveryRecipientsLocked("")
	r.mu.RUnlock()
	for _, recipient := range recipients {
		if recipient.playerID == resultPlayerID {
			_, _ = types.SendCommandResultIfIdentity(recipient.client, recipient.playerID, r.Code, msg)
		} else {
			_, _ = types.SendMessageIfIdentity(recipient.client, recipient.playerID, r.Code, msg)
		}
	}
	r.publishMu.Unlock()
}

// broadcastExcept 广播消息给除指定玩家外的所有玩家
func (r *Room) BroadcastExcept(excludeID string, msg *protocol.Message) {
	r.publishMu.Lock()
	r.mu.RLock()
	recipients := r.snapshotDeliveryRecipientsLocked(excludeID)
	r.mu.RUnlock()
	sendToRoomRecipients(r.Code, recipients, msg)
	r.publishMu.Unlock()
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
	return r.allPlayersInfoLocked()
}

func (r *Room) allPlayersInfoLocked() []protocol.PlayerInfo {
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

func (r *Room) snapshotDeliveryRecipientsLocked(excludeID string) []roomRecipient {
	recipients := make([]roomRecipient, 0, len(r.players))
	for id, player := range r.players {
		if id != excludeID && player != nil && player.Client != nil {
			recipients = append(recipients, roomRecipient{playerID: id, client: player.Client})
		}
	}
	return recipients
}

func sendToRoomRecipients(roomCode string, recipients []roomRecipient, msg *protocol.Message) {
	for _, recipient := range recipients {
		if _, err := types.SendMessageIfIdentity(recipient.client, recipient.playerID, roomCode, msg); err != nil {
			log.Printf("发送房间消息给玩家 %s 失败: %v", recipient.playerID, err)
		}
	}
}

// lockPublishedRoom reserves exact manager ownership and the room publication
// order. It releases RoomManager.mu before delivery; a remover that arrived
// later holds rm.mu and waits for publishMu, so callers must not reacquire rm.mu
// until they release publishMu.
func (rm *RoomManager) lockPublishedRoom(gameRoom *Room) bool {
	if rm == nil || gameRoom == nil {
		return false
	}
	rm.mu.RLock()
	if rm.rooms[gameRoom.Code] != gameRoom {
		rm.mu.RUnlock()
		return false
	}
	gameRoom.publishMu.Lock()
	rm.mu.RUnlock()
	return true
}

func (rm *RoomManager) sendIfCurrentMemberPublished(gameRoom *Room, playerID string, client types.ClientInterface, msg *protocol.Message) (bool, error) {
	gameRoom.mu.RLock()
	player := gameRoom.players[playerID]
	current := player != nil && player.Client == client
	gameRoom.mu.RUnlock()
	if !current {
		return false, nil
	}
	return types.SendMessageIfIdentity(client, playerID, gameRoom.Code, msg)
}

func (rm *RoomManager) sendCommandResultIfCurrentMemberPublished(gameRoom *Room, playerID string, client types.ClientInterface, msg *protocol.Message) (bool, error) {
	gameRoom.mu.RLock()
	player := gameRoom.players[playerID]
	current := player != nil && player.Client == client
	gameRoom.mu.RUnlock()
	if !current {
		return false, nil
	}
	return types.SendCommandResultIfIdentity(client, playerID, gameRoom.Code, msg)
}

// SendIfCurrentMember orders delivery with exact room, logical-player, and
// physical-client ownership. Room mutations wait on publishMu, while client
// identity rebinding is checked atomically by the production Client.
func (rm *RoomManager) SendIfCurrentMember(gameRoom *Room, playerID string, client types.ClientInterface, msg *protocol.Message) (bool, error) {
	if rm == nil || gameRoom == nil || playerID == "" || client == nil {
		return false, nil
	}
	if !rm.lockPublishedRoom(gameRoom) {
		return false, nil
	}
	defer gameRoom.publishMu.Unlock()
	return rm.sendIfCurrentMemberPublished(gameRoom, playerID, client, msg)
}

// WithCurrentRoomPublication runs action while the manager still owns the
// exact room and while presence, removal, and other room publications are
// excluded. Callers must not publish another room event from action.
//
// Lock order is RoomManager.mu -> Room.publishMu -> caller-owned lifecycle
// locks. In particular, callers must never acquire RoomManager.mu after their
// own lock while trying to enter this boundary.
func (rm *RoomManager) WithCurrentRoomPublication(gameRoom *Room, action func()) bool {
	if action == nil || !rm.lockPublishedRoom(gameRoom) {
		return false
	}
	defer gameRoom.publishMu.Unlock()
	action()
	return true
}

// SendBuiltIfCurrentMember validates the exact recipient, constructs an
// authoritative snapshot, and enqueues it without allowing another room
// publication between those steps.
func (rm *RoomManager) SendBuiltIfCurrentMember(
	gameRoom *Room,
	playerID string,
	client types.ClientInterface,
	build func() *protocol.Message,
) (bool, error) {
	if build == nil || !rm.lockPublishedRoom(gameRoom) {
		return false, nil
	}
	defer gameRoom.publishMu.Unlock()
	gameRoom.mu.RLock()
	current := gameRoom.isCurrentMemberLocked(playerID, client)
	gameRoom.mu.RUnlock()
	if !current {
		return false, nil
	}
	message := build()
	if message == nil {
		return false, nil
	}
	return rm.sendIfCurrentMemberPublished(gameRoom, playerID, client, message)
}

// SendIfCurrentRoom remains a compatibility wrapper for callers that do not
// already own an immutable player ID.
func (rm *RoomManager) SendIfCurrentRoom(gameRoom *Room, client types.ClientInterface, msg *protocol.Message) (bool, error) {
	if client == nil {
		return false, nil
	}
	return rm.SendIfCurrentMember(gameRoom, client.GetID(), client, msg)
}

func (rm *RoomManager) sendToCurrentRoomPublished(gameRoom *Room, recipients []roomRecipient, msg *protocol.Message) {
	for _, recipient := range recipients {
		if _, err := rm.sendIfCurrentMemberPublished(gameRoom, recipient.playerID, recipient.client, msg); err != nil {
			log.Printf("发送房间消息给玩家 %s 失败: %v", recipient.playerID, err)
		}
	}
}

func (rm *RoomManager) sendCommandResultToCurrentRoomPublished(gameRoom *Room, recipients []roomRecipient, resultPlayerID string, msg *protocol.Message) {
	for _, recipient := range recipients {
		var err error
		if recipient.playerID == resultPlayerID {
			_, err = rm.sendCommandResultIfCurrentMemberPublished(gameRoom, recipient.playerID, recipient.client, msg)
		} else {
			_, err = rm.sendIfCurrentMemberPublished(gameRoom, recipient.playerID, recipient.client, msg)
		}
		if err != nil {
			log.Printf("发送房间消息给玩家 %s 失败: %v", recipient.playerID, err)
		}
	}
}

// BroadcastCommandResultIfCurrentRoom sends one room event to every current
// member while marking only the command's logical actor as its direct result.
func (rm *RoomManager) BroadcastCommandResultIfCurrentRoom(gameRoom *Room, resultPlayerID string, msg *protocol.Message) bool {
	if !rm.lockPublishedRoom(gameRoom) {
		return false
	}
	defer gameRoom.publishMu.Unlock()
	gameRoom.mu.RLock()
	recipients := gameRoom.snapshotDeliveryRecipientsLocked("")
	gameRoom.mu.RUnlock()
	rm.sendCommandResultToCurrentRoomPublished(gameRoom, recipients, resultPlayerID, msg)
	return true
}

// BroadcastIfCurrentRoom publishes to the exact current roster as one ordered
// room event. It returns false when the RoomManager no longer owns gameRoom.
func (rm *RoomManager) BroadcastIfCurrentRoom(gameRoom *Room, msg *protocol.Message) bool {
	return rm.BroadcastBuiltIfCurrentRoom(gameRoom, func() *protocol.Message { return msg })
}

// BroadcastBuiltIfCurrentRoom constructs a message inside the room publication
// boundary. Full-roster snapshots such as GameStart therefore cannot be built
// before a newer presence transition and enqueued after it.
func (rm *RoomManager) BroadcastBuiltIfCurrentRoom(gameRoom *Room, build func() *protocol.Message) bool {
	if build == nil || !rm.lockPublishedRoom(gameRoom) {
		return false
	}
	defer gameRoom.publishMu.Unlock()
	message := build()
	if message == nil {
		return false
	}
	gameRoom.mu.RLock()
	recipients := gameRoom.snapshotDeliveryRecipientsLocked("")
	gameRoom.mu.RUnlock()
	rm.sendToCurrentRoomPublished(gameRoom, recipients, message)
	return true
}

// SendRoomJoinedSnapshotIfCurrent builds and enqueues the joining player's
// authoritative roster inside the same publication boundary as room changes.
func (rm *RoomManager) SendRoomJoinedSnapshotIfCurrent(gameRoom *Room, playerID string, client types.ClientInterface) (bool, error) {
	if !rm.lockPublishedRoom(gameRoom) {
		return false, nil
	}
	defer gameRoom.publishMu.Unlock()
	gameRoom.mu.RLock()
	player, current := gameRoom.playerInfoLocked(playerID)
	member := gameRoom.players[playerID]
	if !current || member == nil || member.Client != client {
		gameRoom.mu.RUnlock()
		return false, nil
	}
	players := gameRoom.allPlayersInfoLocked()
	gameRoom.mu.RUnlock()
	message := codec.MustNewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
		RoomCode: gameRoom.Code,
		Player:   player,
		Players:  players,
	})
	return rm.sendIfCurrentMemberPublished(gameRoom, playerID, client, message)
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
