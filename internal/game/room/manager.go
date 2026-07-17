package room

import (
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// CreateRoom 创建房间
func (rm *RoomManager) CreateRoom(client types.ClientInterface) (*Room, error) {
	return rm.createRoom(client, false)
}

// CreateRoomWithResponse publishes the creator's success response before the
// room can accept a join whose PlayerJoined event would otherwise overtake it.
func (rm *RoomManager) CreateRoomWithResponse(client types.ClientInterface) (*Room, error) {
	return rm.createRoom(client, true)
}

func (rm *RoomManager) createRoom(client types.ClientInterface, publishResponse bool) (*Room, error) {
	creator := newRoomPlayer(client, 0)
	rm.mu.Lock()
	if rm.closed {
		rm.mu.Unlock()
		return nil, ErrRoomManagerClosed
	}
	code := rm.generateRoomCode()
	room := newRoom(code, time.Now())
	room.publishMu.Lock()
	if !types.CompareAndSetRoom(client, creator.ID, "", code) {
		room.publishMu.Unlock()
		rm.mu.Unlock()
		return nil, apperrors.ErrNotInRoom
	}
	room.players[creator.ID] = creator
	room.playerOrder = append(room.playerOrder, creator.ID)
	rm.rooms[code] = room
	rm.mu.Unlock()
	rm.reportRoomCount()
	if publishResponse {
		message := codec.MustNewMessage(protocol.MsgRoomCreated, protocol.RoomCreatedPayload{
			RoomCode: room.Code,
			Player: protocol.PlayerInfo{
				ID:     creator.ID,
				Name:   creator.Name,
				Seat:   creator.Seat,
				Online: true,
				IsBot:  creator.IsBot,
			},
		})
		if _, err := rm.sendCommandResultIfCurrentMemberPublished(room, creator.ID, client, message); err != nil {
			log.Printf("发送房间 %s 创建结果给玩家 %s 失败: %v", room.Code, creator.ID, err)
		}
	}
	room.publishMu.Unlock()

	// 保存到 Redis
	rm.saveRoomAsync(room)

	rm.structuredLogger().Info("room created",
		"event", "room_created",
		"room_id", code,
		"player_id", creator.ID,
		"result", "created",
	)

	return room, nil
}

// JoinRoom 加入房间
func (rm *RoomManager) JoinRoom(client types.ClientInterface, code string) (*Room, error) {
	return rm.joinRoom(client, code, false)
}

// JoinRoomWithResponse commits membership and publishes the joining player's
// full roster before any later room mutation can publish an incremental event.
func (rm *RoomManager) JoinRoomWithResponse(client types.ClientInterface, code string) (*Room, error) {
	return rm.joinRoom(client, code, true)
}

func (rm *RoomManager) joinRoom(client types.ClientInterface, code string, publishResponse bool) (*Room, error) {
	joining := newRoomPlayer(client, 0)
	rm.mu.RLock()
	room, exists := rm.rooms[code]
	if !exists {
		rm.mu.RUnlock()
		return nil, apperrors.ErrRoomNotFound
	}
	room.publishMu.Lock()

	room.mu.Lock()
	if len(room.players) >= 3 {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.RUnlock()
		return nil, apperrors.ErrRoomFull
	}

	if room.state != RoomStateWaiting {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.RUnlock()
		return nil, apperrors.ErrGameStarted
	}

	if _, duplicate := room.players[joining.ID]; duplicate {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.RUnlock()
		return nil, apperrors.ErrNotInRoom
	}
	if !types.CompareAndSetRoom(client, joining.ID, "", code) {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.RUnlock()
		return nil, apperrors.ErrNotInRoom
	}
	joining.Seat = room.nextAvailableSeatLocked()
	room.players[joining.ID] = joining
	room.insertPlayerOrderLocked(joining.ID)
	joinedInfo, _ := room.playerInfoLocked(joining.ID)
	recipients := room.snapshotDeliveryRecipientsLocked(joining.ID)
	var joinedMessage *protocol.Message
	if publishResponse {
		joinedMessage = codec.MustNewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
			RoomCode: room.Code,
			Player:   joinedInfo,
			Players:  room.allPlayersInfoLocked(),
		})
	}
	room.mu.Unlock()
	rm.mu.RUnlock()

	rm.structuredLogger().Info("room joined",
		"event", "room_joined",
		"room_id", code,
		"player_id", joining.ID,
		"seat", joining.Seat,
		"result", "joined",
	)
	if joinedMessage != nil {
		if _, err := rm.sendCommandResultIfCurrentMemberPublished(room, joining.ID, client, joinedMessage); err != nil {
			log.Printf("发送房间 %s 加入结果给玩家 %s 失败: %v", room.Code, joining.ID, err)
		}
	}

	rm.sendToCurrentRoomPublished(room, recipients, codec.MustNewMessage(protocol.MsgPlayerJoined, protocol.PlayerJoinedPayload{
		Player: joinedInfo,
	}))
	room.publishMu.Unlock()

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
	playerID := client.GetID()
	if roomCode == "" {
		return false
	}

	rm.mu.Lock()
	room, exists := rm.rooms[roomCode]
	if !exists {
		rm.mu.Unlock()
		return false
	}
	room.publishMu.Lock()
	room.mu.Lock()

	player, exists := room.players[playerID]
	if !exists || player == nil || player.Client != client {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.Unlock()
		return false
	}
	if room.state != RoomStateWaiting {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.Unlock()
		return false
	}
	removed := snapshotPlayer(player)
	empty := len(room.players) == 1
	var removal roomRemovalDispatch
	if empty {
		removal, _ = rm.removePublishedRoomLocked(room, RoomRemovalLeft)
	} else {
		types.CompareAndSetRoom(client, removed.ID, roomCode, "")
		room.removePlayerLocked(removed.ID)
	}
	recipients := room.snapshotDeliveryRecipientsLocked(removed.ID)
	room.mu.Unlock()
	rm.mu.Unlock()

	rm.sendToCurrentRoomPublished(room, recipients, codec.MustNewMessage(protocol.MsgPlayerLeft, protocol.PlayerLeftPayload{
		PlayerID:   removed.ID,
		PlayerName: removed.Name,
	}))
	room.publishMu.Unlock()
	rm.structuredLogger().Info("room left",
		"event", "room_left",
		"room_id", roomCode,
		"player_id", removed.ID,
		"seat", removed.Seat,
		"result", "left",
	)

	// 如果房间空了，删除房间
	if empty {
		rm.dispatchRoomRemoval(removal)
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
	room.publishMu.Lock()

	room.mu.Lock()
	player, exists := room.players[client.GetID()]
	if !exists || player == nil || player.Client != client {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.RUnlock()
		return apperrors.ErrNotInRoom
	}
	if room.state != RoomStateWaiting {
		room.mu.Unlock()
		room.publishMu.Unlock()
		rm.mu.RUnlock()
		return apperrors.ErrGameStarted
	}

	previousReady := player.Ready
	player.Ready = ready
	playerID := player.ID
	recipients := room.snapshotDeliveryRecipientsLocked("")
	shouldStart := room.checkAllReadyLocked()
	var startPlayers []PlayerSnapshot
	var releaseStartLease func()
	if shouldStart {
		if rm.startAdmission != nil {
			var admitted bool
			releaseStartLease, admitted = rm.startAdmission()
			if !admitted {
				player.Ready = previousReady
				room.mu.Unlock()
				room.publishMu.Unlock()
				rm.mu.RUnlock()
				return ErrGameStartAdmissionRejected
			}
		}
		if err := room.startGameLocked(); err != nil {
			if releaseStartLease != nil {
				releaseStartLease()
			}
			player.Ready = previousReady
			room.mu.Unlock()
			room.publishMu.Unlock()
			rm.mu.RUnlock()
			return err
		}
		startPlayers = room.snapshotPlayersLocked()
	}
	callback := rm.onGameStart
	room.mu.Unlock()
	rm.mu.RUnlock()

	rm.sendCommandResultToCurrentRoomPublished(room, recipients, playerID, codec.MustNewMessage(protocol.MsgPlayerReady, protocol.PlayerReadyPayload{
		PlayerID: playerID,
		Ready:    ready,
	}))
	room.publishMu.Unlock()

	if shouldStart {
		// The callback may acquire GameSession.mu and therefore must never run
		// while Room.mu is held. This removes the room -> game-session lock edge.
		if callback != nil {
			callback(room, startPlayers, releaseStartLease)
		} else if releaseStartLease != nil {
			releaseStartLease()
		}

		// 保存房间状态
		rm.saveRoomAsync(room)
	}

	return nil
}

func (rm *RoomManager) SetOnGameStart(callback func(*Room, []PlayerSnapshot, func())) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.onGameStart = callback
}

// SetStartAdmission installs the authoritative lease gate used when the last
// player readies a room. The callback is invoked while the ready transition is
// still protected by the room lock.
func (rm *RoomManager) SetStartAdmission(callback func() (func(), bool)) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.startAdmission = callback
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
