package room

import (
	"context"
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// CreateRoom 创建房间
func (rm *RoomManager) CreateRoom(client types.ClientInterface) (*Room, error) {
	creator := newRoomPlayer(client, 0)
	rm.mu.Lock()
	code := rm.generateRoomCode()
	room := newRoom(code, time.Now())
	room.players[creator.ID] = creator
	room.playerOrder = append(room.playerOrder, creator.ID)
	client.SetRoom(code)
	rm.rooms[code] = room
	rm.mu.Unlock()

	// 保存到 Redis
	rm.saveRoomAsync(room)

	log.Printf("🏠 房间 %s 已创建，玩家 %s", code, client.GetName())

	return room, nil
}

// JoinRoom 加入房间
func (rm *RoomManager) JoinRoom(client types.ClientInterface, code string) (*Room, error) {
	joining := newRoomPlayer(client, 0)
	rm.mu.RLock()
	room, exists := rm.rooms[code]
	if !exists {
		rm.mu.RUnlock()
		return nil, apperrors.ErrRoomNotFound
	}

	room.mu.Lock()
	if len(room.players) >= 3 {
		room.mu.Unlock()
		rm.mu.RUnlock()
		return nil, apperrors.ErrRoomFull
	}

	if room.state != RoomStateWaiting {
		room.mu.Unlock()
		rm.mu.RUnlock()
		return nil, apperrors.ErrGameStarted
	}

	if _, duplicate := room.players[joining.ID]; duplicate {
		room.mu.Unlock()
		rm.mu.RUnlock()
		return nil, apperrors.ErrNotInRoom
	}
	joining.Seat = room.nextAvailableSeatLocked()
	room.players[joining.ID] = joining
	room.insertPlayerOrderLocked(joining.ID)
	client.SetRoom(code)
	joinedInfo, _ := room.playerInfoLocked(joining.ID)
	recipients := room.snapshotRecipientsLocked(joining.ID)
	room.mu.Unlock()
	rm.mu.RUnlock()

	log.Printf("👤 玩家 %s 加入房间 %s", client.GetName(), code)

	sendToRecipients(recipients, codec.MustNewMessage(protocol.MsgPlayerJoined, protocol.PlayerJoinedPayload{
		Player: joinedInfo,
	}))

	// 保存到 Redis
	rm.saveRoomAsync(room)

	return room, nil
}

func (r *Room) nextAvailableSeatLocked() int {
	used := [3]bool{}
	for _, player := range r.players {
		if player != nil && player.Seat >= 0 && player.Seat < len(used) {
			used[player.Seat] = true
		}
	}
	for seat, occupied := range used {
		if !occupied {
			return seat
		}
	}
	return len(r.players)
}

func (r *Room) insertPlayerOrderLocked(playerID string) {
	player := r.players[playerID]
	insertAt := len(r.playerOrder)
	for index, currentID := range r.playerOrder {
		current := r.players[currentID]
		if current != nil && current.Seat > player.Seat {
			insertAt = index
			break
		}
	}
	r.playerOrder = append(r.playerOrder, "")
	copy(r.playerOrder[insertAt+1:], r.playerOrder[insertAt:])
	r.playerOrder[insertAt] = playerID
}

// LeaveRoom 离开房间。返回值表示客户端的房间身份是否已被权威清除。
func (rm *RoomManager) LeaveRoom(client types.ClientInterface) bool {
	roomCode := client.GetRoom()
	if roomCode == "" {
		return false
	}

	rm.mu.Lock()
	room, exists := rm.rooms[roomCode]
	if !exists {
		rm.mu.Unlock()
		return false
	}
	room.mu.Lock()

	player, exists := room.players[client.GetID()]
	if !exists || player == nil || player.Client != client {
		room.mu.Unlock()
		rm.mu.Unlock()
		return false
	}
	if room.state != RoomStateWaiting {
		room.mu.Unlock()
		rm.mu.Unlock()
		return false
	}
	client.SetRoom("")
	removed, _ := room.removePlayerLocked(client.GetID())

	empty := len(room.players) == 0
	if empty {
		delete(rm.rooms, roomCode)
	}
	recipients := room.snapshotRecipientsLocked(client.GetID())
	room.mu.Unlock()
	rm.mu.Unlock()

	sendToRecipients(recipients, codec.MustNewMessage(protocol.MsgPlayerLeft, protocol.PlayerLeftPayload{
		PlayerID:   removed.ID,
		PlayerName: removed.Name,
	}))
	log.Printf("👋 玩家 %s 离开房间 %s (座位 %d)", removed.Name, roomCode, removed.Seat)

	// 如果房间空了，删除房间
	if empty {
		// 从 Redis 删除
		rm.deleteRoomAsync(roomCode)
		log.Printf("🏠 房间 %s 已解散", roomCode)
	} else {
		rm.saveRoomAsync(room)
	}

	return true
}

// SetPlayerReady 设置玩家准备状态
func (rm *RoomManager) SetPlayerReady(client types.ClientInterface, ready bool) error {
	roomCode := client.GetRoom()
	if roomCode == "" {
		return apperrors.ErrNotInRoom
	}

	rm.mu.RLock()
	room, exists := rm.rooms[roomCode]
	if !exists {
		rm.mu.RUnlock()
		return apperrors.ErrRoomNotFound
	}

	room.mu.Lock()
	player, exists := room.players[client.GetID()]
	if !exists || player == nil || player.Client != client {
		room.mu.Unlock()
		rm.mu.RUnlock()
		return apperrors.ErrNotInRoom
	}
	if room.state != RoomStateWaiting {
		room.mu.Unlock()
		rm.mu.RUnlock()
		return apperrors.ErrGameStarted
	}

	player.Ready = ready
	recipients := room.snapshotRecipientsLocked("")
	shouldStart := room.checkAllReadyLocked()
	var startPlayers []PlayerSnapshot
	if shouldStart {
		if err := room.startGameLocked(); err != nil {
			room.mu.Unlock()
			rm.mu.RUnlock()
			return err
		}
		startPlayers = room.snapshotPlayersLocked()
	}
	callback := rm.onGameStart
	room.mu.Unlock()
	rm.mu.RUnlock()

	sendToRecipients(recipients, codec.MustNewMessage(protocol.MsgPlayerReady, protocol.PlayerReadyPayload{
		PlayerID: client.GetID(),
		Ready:    ready,
	}))

	if shouldStart {
		// The callback may acquire GameSession.mu and therefore must never run
		// while Room.mu is held. This removes the room -> game-session lock edge.
		if callback != nil {
			callback(room, startPlayers)
		}

		// 保存房间状态
		rm.saveRoomAsync(room)
	}

	return nil
}

func (rm *RoomManager) saveRoomAsync(room *Room) {
	if rm.redisStore == nil || !rm.redisStore.IsReady() {
		return
	}
	code := room.Code
	data := room.ToRoomData()
	go func() {
		if err := rm.redisStore.SaveRoom(context.Background(), code, data); err != nil {
			log.Printf("保存房间 %s 到 Redis 失败: %v", code, err)
		}
	}()
}

func (rm *RoomManager) deleteRoomAsync(code string) {
	if rm.redisStore == nil || !rm.redisStore.IsReady() {
		return
	}
	go func() {
		if err := rm.redisStore.DeleteRoom(context.Background(), code); err != nil {
			log.Printf("从 Redis 删除房间 %s 失败: %v", code, err)
		}
	}()
}

func (rm *RoomManager) SetOnGameStart(callback func(*Room, []PlayerSnapshot)) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.onGameStart = callback
}

// GetRoom 获取房间
func (rm *RoomManager) GetRoom(code string) *Room {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.rooms[code]
}

// GetRoomList 获取可加入的房间列表
func (rm *RoomManager) GetRoomList() []protocol.RoomListItem {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var rooms []protocol.RoomListItem
	for code, room := range rm.rooms {
		room.mu.RLock()
		// 只返回等待中且未满的房间
		if room.state == RoomStateWaiting && len(room.players) < 3 {
			rooms = append(rooms, protocol.RoomListItem{
				RoomCode:    code,
				PlayerCount: len(room.players),
				MaxPlayers:  3,
			})
		}
		room.mu.RUnlock()
	}
	return rooms
}

// GetRoomByPlayerID 通过玩家 ID 获取房间
func (rm *RoomManager) GetRoomByPlayerID(playerID string) *Room {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	for _, room := range rm.rooms {
		room.mu.RLock()
		_, exists := room.players[playerID]
		room.mu.RUnlock()
		if exists {
			return room
		}
	}
	return nil
}

// GetActiveGamesCount 获取进行中的游戏数量
func (rm *RoomManager) GetActiveGamesCount() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	count := 0
	for _, room := range rm.rooms {
		room.mu.RLock()
		// 只统计正在游戏中的房间（叫地主、出牌）
		// RoomStateEnded 不计入，因为游戏已结束只是等待清理
		switch room.state {
		case RoomStateBidding, RoomStatePlaying:
			count++
		}
		room.mu.RUnlock()
	}
	return count
}
