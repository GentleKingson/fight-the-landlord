package room

import (
	"cmp"
	"context"
	"log"
	"slices"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// CreateRoom 创建房间
func (rm *RoomManager) CreateRoom(client types.ClientInterface) (*Room, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if client.GetRoom() != "" {
		return nil, apperrors.ErrAlreadyInRoom
	}

	// 生成唯一房间号
	code := rm.generateRoomCode()

	room := &Room{
		Code:        code,
		OwnerID:     client.GetID(),
		State:       RoomStateWaiting,
		Players:     make(map[string]*RoomPlayer),
		PlayerOrder: make([]string, 0, 3),
		CreatedAt:   time.Now(),
	}

	// 添加创建者
	player := &RoomPlayer{
		Client: client,
		Seat:   0,
		Ready:  false,
	}
	room.Players[client.GetID()] = player
	room.PlayerOrder = append(room.PlayerOrder, client.GetID())
	client.SetRoom(code)

	rm.rooms[code] = room

	// 保存到 Redis
	if rm.redisStore != nil && rm.redisStore.IsReady() {
		go func() { _ = rm.redisStore.SaveRoom(context.Background(), room.Code, room.ToRoomData()) }()
	}

	log.Printf("🏠 房间 %s 已创建，玩家 %s", code, client.GetName())

	return room, nil
}

// JoinRoom 加入房间
func (rm *RoomManager) JoinRoom(client types.ClientInterface, code string) (*Room, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if client.GetRoom() != "" {
		return nil, apperrors.ErrAlreadyInRoom
	}

	room, exists := rm.rooms[code]
	if !exists {
		return nil, apperrors.ErrRoomNotFound
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	if len(room.Players) >= 3 {
		return nil, apperrors.ErrRoomFull
	}

	if room.State != RoomStateWaiting {
		return nil, apperrors.ErrGameStarted
	}

	// 分配座位
	used := [3]bool{}
	for _, p := range room.Players {
		used[p.Seat] = true
	}
	seat := slices.Index(used[:], false)
	player := &RoomPlayer{
		Client: client,
		Seat:   seat,
		Ready:  false,
	}
	room.Players[client.GetID()] = player
	if !client.IsBot() {
		room.AllOfflineAt = time.Time{}
	}
	room.PlayerOrder = append(room.PlayerOrder, client.GetID())
	slices.SortFunc(room.PlayerOrder, func(a, b string) int {
		return cmp.Compare(room.Players[a].Seat, room.Players[b].Seat)
	})
	client.SetRoom(code)

	log.Printf("👤 玩家 %s 加入房间 %s", client.GetName(), code)

	// 通知房间内其他玩家
	room.BroadcastExcept(client.GetID(), codec.MustNewMessage(protocol.MsgPlayerJoined, protocol.PlayerJoinedPayload{
		Player: room.GetPlayerInfo(client.GetID()),
	}))

	// 保存到 Redis
	if rm.redisStore != nil && rm.redisStore.IsReady() {
		go func() { _ = rm.redisStore.SaveRoom(context.Background(), room.Code, room.ToRoomData()) }()
	}

	return room, nil
}

// LeaveRoom rejects membership changes once the room has committed to starting.
// Lock ordering is always manager -> room, also used by joins and cleanup.
func (rm *RoomManager) LeaveRoom(client types.ClientInterface) error {
	rm.mu.Lock()
	removed, err := rm.leaveRoomLocked(client)
	rm.mu.Unlock()
	if removed != nil {
		rm.finishRemoval(removed)
	}
	return err
}

func (rm *RoomManager) leaveRoomLocked(client types.ClientInterface) (*Room, error) {
	code := client.GetRoom()
	r := rm.rooms[code]
	if r == nil {
		client.SetRoom("")
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.State != RoomStateWaiting {
		return nil, apperrors.ErrGameStarted
	}
	player := r.Players[client.GetID()]
	if player == nil || player.Client != client {
		return nil, apperrors.ErrNotInRoom
	}
	delete(r.Players, client.GetID())
	r.PlayerOrder = slices.DeleteFunc(r.PlayerOrder, func(id string) bool { return id == client.GetID() })
	client.SetRoom("")
	if len(r.Players) == 0 {
		delete(rm.rooms, code)
		return r, nil
	}
	var resetReadyPlayerID string
	if r.OwnerID == client.GetID() {
		r.OwnerID = ""
		for _, id := range r.PlayerOrder {
			if p := r.Players[id]; p != nil && !p.Client.IsBot() && !p.Offline {
				r.OwnerID = id
				break
			}
		}
		if r.OwnerID == "" {
			for _, id := range r.PlayerOrder {
				if p := r.Players[id]; p != nil && !p.Client.IsBot() {
					r.OwnerID = id
					break
				}
			}
		}
		if newOwner := r.Players[r.OwnerID]; newOwner != nil && newOwner.Ready {
			newOwner.Ready = false
			resetReadyPlayerID = r.OwnerID
		}
	}
	r.Broadcast(codec.MustNewMessage(protocol.MsgPlayerLeft, protocol.PlayerLeftPayload{
		PlayerID:   client.GetID(),
		PlayerName: client.GetName(),
		OwnerID:    r.OwnerID,
	}))
	if resetReadyPlayerID != "" {
		r.Broadcast(codec.MustNewMessage(protocol.MsgPlayerReady, protocol.PlayerReadyPayload{
			PlayerID: resetReadyPlayerID,
			Ready:    false,
		}))
	}
	allOffline := true
	for _, p := range r.Players {
		if !p.Client.IsBot() && !p.Offline {
			allOffline = false
			break
		}
	}
	if allOffline && r.AllOfflineAt.IsZero() {
		r.AllOfflineAt = time.Now()
	}
	if rm.redisStore != nil && rm.redisStore.IsReady() {
		go func() { _ = rm.redisStore.SaveRoom(context.Background(), r.Code, r.ToRoomData()) }()
	}
	return nil, nil
}

// SetPlayerReady 设置玩家准备状态
func (rm *RoomManager) SetPlayerReady(client types.ClientInterface, ready bool) error {
	roomCode := client.GetRoom()
	if roomCode == "" {
		return apperrors.ErrNotInRoom
	}

	rm.mu.RLock()
	room, exists := rm.rooms[roomCode]
	onGameStart := rm.onGameStart
	rm.mu.RUnlock()
	if !exists {
		return apperrors.ErrRoomNotFound
	}

	room.mu.Lock()
	if room.State != RoomStateWaiting {
		room.mu.Unlock()
		return apperrors.ErrGameStarted
	}

	player, exists := room.Players[client.GetID()]
	if !exists {
		room.mu.Unlock()
		return apperrors.ErrNotInRoom
	}

	player.Ready = ready

	// 广播准备状态
	room.Broadcast(codec.MustNewMessage(protocol.MsgPlayerReady, protocol.PlayerReadyPayload{
		PlayerID: client.GetID(),
		Ready:    ready,
	}))

	// 检查是否所有人都准备好了
	if room.checkAllReady() {
		if err := room.startGameLocked(); err != nil {
			log.Printf("开始游戏失败: %v", err)
			room.mu.Unlock()
			return nil
		}

		room.mu.Unlock()
		// 创建游戏会话并开始
		if onGameStart != nil {
			onGameStart(room)
		}

		// 保存房间状态
		if rm.redisStore != nil && rm.redisStore.IsReady() {
			go func() { _ = rm.redisStore.SaveRoom(context.Background(), room.Code, room.ToRoomData()) }()
		}
	} else {
		room.mu.Unlock()
	}

	return nil
}

// StartRoom 由房主开始游戏；当真人玩家未满 3 人时自动补齐机器人
func (rm *RoomManager) StartRoom(client types.ClientInterface) error {
	roomCode := client.GetRoom()
	if roomCode == "" {
		return apperrors.ErrNotInRoom
	}

	rm.mu.RLock()
	room, exists := rm.rooms[roomCode]
	botFactory := rm.botFactory
	onGameStart := rm.onGameStart
	rm.mu.RUnlock()
	if !exists {
		return apperrors.ErrRoomNotFound
	}

	room.mu.Lock()
	if room.State != RoomStateWaiting {
		room.mu.Unlock()
		return apperrors.ErrGameStarted
	}

	player := room.Players[client.GetID()]
	if player == nil || player.Client != client {
		room.mu.Unlock()
		return apperrors.ErrNotInRoom
	}

	ownerID := room.OwnerID
	if ownerID == "" && len(room.PlayerOrder) > 0 {
		ownerID = room.PlayerOrder[0]
	}
	if client.GetID() != ownerID {
		room.mu.Unlock()
		return apperrors.ErrNotRoomOwner
	}

	for id, p := range room.Players {
		if id == client.GetID() || p.Client.IsBot() {
			continue
		}
		if p.Offline {
			room.mu.Unlock()
			return apperrors.ErrPlayerOffline
		}
		if !p.Ready {
			room.mu.Unlock()
			return apperrors.ErrPlayersNotReady
		}
	}

	if len(room.Players) < 3 && botFactory == nil {
		room.mu.Unlock()
		return &apperrors.GameError{Code: protocol.ErrCodeUnknown, Message: "人机服务暂未就绪"}
	}

	player.Ready = true
	for len(room.Players) < 3 {
		used := [3]bool{}
		for _, p := range room.Players {
			used[p.Seat] = true
		}
		seat := slices.Index(used[:], false)
		botClient := botFactory()
		botClient.SetRoom(room.Code)
		room.Players[botClient.GetID()] = &RoomPlayer{
			Client: botClient,
			Seat:   seat,
			Ready:  true,
		}
		room.PlayerOrder = append(room.PlayerOrder, botClient.GetID())
		log.Printf("🤖 Bot %s 补位加入房间 %s (座位 %d)", botClient.GetName(), room.Code, seat)
	}
	slices.SortFunc(room.PlayerOrder, func(a, b string) int {
		return cmp.Compare(room.Players[a].Seat, room.Players[b].Seat)
	})

	if err := room.startGameLocked(); err != nil {
		log.Printf("开始游戏失败: %v", err)
		room.mu.Unlock()
		return err
	}
	room.mu.Unlock()

	if onGameStart != nil {
		onGameStart(room)
	}

	if rm.redisStore != nil && rm.redisStore.IsReady() {
		go func() { _ = rm.redisStore.SaveRoom(context.Background(), room.Code, room.ToRoomData()) }()
	}

	return nil
}

func (rm *RoomManager) SetBotFactory(factory func() types.ClientInterface) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.botFactory = factory
}

func (rm *RoomManager) SetOnGameStart(callback func(*Room)) {
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
		if room.State == RoomStateWaiting && len(room.Players) < 3 {
			rooms = append(rooms, protocol.RoomListItem{
				RoomCode:    code,
				PlayerCount: len(room.Players),
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
		_, exists := room.Players[playerID]
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
		switch room.State {
		case RoomStateDealing, RoomStateBidding, RoomStatePlaying:
			count++
		}
		room.mu.RUnlock()
	}
	return count
}
