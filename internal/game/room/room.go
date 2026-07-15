package room

import (
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

// Room 游戏房间
type Room struct {
	Code      string    // 房间号
	CreatedAt time.Time // 创建时间

	state       RoomState
	players     map[string]*RoomPlayer
	playerOrder []string

	// Lock order is GameSession.mu -> Room.mu. Room methods never call back into
	// GameSession, and no network delivery is performed while this lock is held.
	mu sync.RWMutex
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
	redisStore  *storage.RedisStore
	roomTimeout time.Duration
	gameConfig  config.GameConfig
	onGameStart func(*Room, []PlayerSnapshot)
	rooms       map[string]*Room
	mu          sync.RWMutex
}

// NewRoomManager 创建房间管理器
func NewRoomManager(rs *storage.RedisStore, gameConfig config.GameConfig) *RoomManager {
	rm := &RoomManager{
		redisStore:  rs,
		roomTimeout: gameConfig.RoomTimeoutDuration(),
		gameConfig:  gameConfig,
		rooms:       make(map[string]*Room),
	}

	// 启动房间清理协程
	go rm.cleanupLoop()

	return rm
}
