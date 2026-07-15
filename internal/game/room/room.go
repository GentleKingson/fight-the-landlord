package room

import (
	"context"
	"sync"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

const (
	roomCodeLength = 6            // 房间号长度
	roomCodeChars  = "0123456789" // 房间号字符集
)

// RoomPlayer 房间中的玩家
type RoomPlayer struct {
	ID         string
	Name       string
	IsBot      bool
	Client     types.ClientInterface
	Seat       int  // 座位号 0-2
	Ready      bool // 是否准备
	IsLandlord bool // 是否是地主
}

// PlayerSnapshot is an immutable copy of one room member. The Client value is
// a delivery handle captured at the same membership revision as the metadata;
// callers must not retain it as authoritative membership state.
type PlayerSnapshot struct {
	ID         string
	Name       string
	IsBot      bool
	Client     types.ClientInterface
	Seat       int
	Ready      bool
	IsLandlord bool
}

// RoomRemovalReason identifies the authoritative transition that retired a
// published room.
type RoomRemovalReason string

const (
	RoomRemovalLeft       RoomRemovalReason = "left"
	RoomRemovalAllOffline RoomRemovalReason = "all_offline"
	RoomRemovalTimeout    RoomRemovalReason = "timeout"
	RoomRemovalRollback   RoomRemovalReason = "rollback"
	RoomRemovalShutdown   RoomRemovalReason = "shutdown"
)

// RoomRemoval carries both the room code and exact in-memory identity. The
// pointer prevents delayed callbacks from retiring a replacement that reused
// the same code.
type RoomRemoval struct {
	Code    string
	Room    *Room
	Players []PlayerSnapshot
	Reason  RoomRemovalReason
}

// Room 游戏房间
type Room struct {
	Code      string    // 房间号
	CreatedAt time.Time // 创建时间

	state       RoomState
	players     map[string]*RoomPlayer
	playerOrder []string

	// Lock order is GameSession.mu -> Room.mu. Room methods never call back into
	// GameSession, and no network delivery is performed while this lock is held.
	// RoomManager ownership is acquired before publishMu. publishMu serializes
	// one room mutation with its immutable outbound events, while room.mu is
	// released before any client method is called.
	publishMu sync.Mutex
	mu        sync.RWMutex
}

func newRoom(code string, createdAt time.Time) *Room {
	return &Room{
		Code:        code,
		CreatedAt:   createdAt,
		state:       RoomStateWaiting,
		players:     make(map[string]*RoomPlayer),
		playerOrder: make([]string, 0, 3),
	}
}

func newRoomPlayer(client types.ClientInterface, seat int) *RoomPlayer {
	return &RoomPlayer{
		ID:     client.GetID(),
		Name:   client.GetName(),
		IsBot:  client.IsBot(),
		Client: client,
		Seat:   seat,
	}
}

// RoomManager 房间管理器
type RoomManager struct {
	redisStore    *storage.RedisStore
	roomTimeout   time.Duration
	gameConfig    config.GameConfig
	onGameStart   func(*Room, []PlayerSnapshot)
	onRoomRemoved func(RoomRemoval)
	onPresence    func(*Room, string, bool)
	rooms         map[string]*Room
	pendingRooms  map[string]*MatchRoomTransaction
	retiringRooms map[string]*Room
	roomCodeFunc  func() string
	mu            sync.RWMutex

	persistenceMu     sync.Mutex
	persistenceQueues map[string]*roomPersistenceQueue
	saveRoomFunc      func(context.Context, string, *storage.RoomData) error
	deleteRoomFunc    func(context.Context, string) error
}

// NewRoomManager 创建房间管理器
func NewRoomManager(rs *storage.RedisStore, gameConfig config.GameConfig) *RoomManager {
	rm := &RoomManager{
		redisStore:        rs,
		roomTimeout:       gameConfig.RoomTimeoutDuration(),
		gameConfig:        gameConfig,
		rooms:             make(map[string]*Room),
		pendingRooms:      make(map[string]*MatchRoomTransaction),
		retiringRooms:     make(map[string]*Room),
		persistenceQueues: make(map[string]*roomPersistenceQueue),
	}

	// 启动房间清理协程
	go rm.cleanupLoop()

	return rm
}
