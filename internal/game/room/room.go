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
	Client     types.ClientInterface
	Seat       int  // 座位号 0-2
	Ready      bool // 是否准备
	IsLandlord bool // 是否是地主
	Offline    bool // Retain the closed client for identity and safe no-op sends
}

// Room 游戏房间
type Room struct {
	Code         string                 // 房间号
	OwnerID      string                 // 房主玩家 ID
	State        RoomState              // 房间状态
	Players      map[string]*RoomPlayer // 玩家列表
	PlayerOrder  []string               // 玩家顺序（按座位）
	AllOfflineAt time.Time              // 真人全部离线的时间；机器人不延长保留期
	EndedAt      time.Time              // 结算保留期从结束时开始
	CreatedAt    time.Time              // 创建时间

	offlineRetention time.Duration
	resultRetention  time.Duration
	mu               sync.RWMutex
}

// RoomManager 房间管理器
type RoomManager struct {
	redisStore    *storage.RedisStore
	roomTimeout   time.Duration
	gameConfig    config.GameConfig
	botFactory    func() types.ClientInterface
	onGameStart   func(*Room)
	onRoomRemoved func(*Room)
	rooms         map[string]*Room
	mu            sync.RWMutex
}

// NewRoomManager 创建房间管理器
func NewRoomManager(rs *storage.RedisStore, gameConfig config.GameConfig) *RoomManager {
	if gameConfig.RoomTimeout <= 0 {
		gameConfig.RoomTimeout = 10
	}
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
