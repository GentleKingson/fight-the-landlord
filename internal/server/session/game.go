package session

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/observability"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
)

// GameState 游戏状态
type GameState int

const (
	GameStateInit GameState = iota
	GameStateBidding
	GameStatePlaying
	GameStateEnded

	baseScore = 1
)

var gameIDCounter = time.Now().UnixNano()

func nextGameID() string {
	return fmt.Sprintf("game-%d", atomic.AddInt64(&gameIDCounter, 1))
}

// GamePlayer 游戏中的玩家
type GamePlayer struct {
	ID         string
	Name       string
	Seat       int
	Ready      bool
	Hand       []card.Card
	IsLandlord bool
	IsOffline  bool // 是否离线
	IsBot      bool
}

// GameSession 游戏会话
type GameSession struct {
	room        *room.Room
	roomManager atomic.Pointer[room.RoomManager]
	leaderboard *storage.LeaderboardManager
	gameConfig  config.GameConfig
	state       GameState
	players     []*GamePlayer // 按座位顺序

	deck        card.Deck
	bottomCards []card.Card
	playedCards [][]card.Card // 按玩家座位记录本局所有已公开出牌

	// 权威快照标识。gameID 每次重新发牌都会更新；turnID 和
	// snapshotVersion 在 GameSession 生命周期内只增不减。
	// snapshotVersion 是已提交状态/已发送事件的水位，读取快照不会改变它。
	gameID          string
	turnID          int64
	snapshotVersion int64

	// 叫抢地主相关
	currentBidder     int // 当前叫/抢地主的玩家索引
	landlordCaller    int // 第一个叫地主的玩家索引，-1 表示尚无人叫
	landlordCandidate int // 当前暂定地主索引，-1 表示尚无
	bidPasses         int // 连续"不叫/不抢"次数（用于流局与结束判断）
	grabActions       int // 抢地主阶段已进行的决策次数（每人最多一次，最多 3 次后强制结束）
	bidMultiplier     int // 叫抢阶段产生的底倍
	redealCount       int // 已发生的流局次数（达到上限后随机强制指定地主）

	// 倍数相关（出牌阶段累计）
	bombCount         int // 已打出的炸弹+王炸数量，每个翻一倍
	landlordPlays     int // 地主实际出牌次数（用于反春天判断）
	farmerPlays       int // 农民实际出牌次数（用于春天判断）
	settledMultiplier int // 游戏结束后的最终倍数（含春天/反春天）
	settlement        *protocol.GameSettlementDTO
	pendingDeliveries []pendingDelivery
	pendingResults    []pendingGameResult
	pendingRoomReset  bool
	retired           bool // guarded by mu; retirement is serialized by actionMu
	metrics           *observability.Metrics
	metricStartedAt   time.Time
	metricStarted     bool
	metricFinished    bool
	quiescenceMu      sync.Mutex
	quiescenceRelease func()
	quiescenceDone    bool

	// 出牌相关
	currentPlayer     int             // 当前出牌玩家索引
	lastPlayedHand    rule.ParsedHand // 上家出牌
	lastPlayerIdx     int             // 上家索引
	consecutivePasses int             // 连续 PASS 次数

	// 超时控制
	turnTimer        *time.Timer
	offlineWaitTimer *time.Timer   // 离线等待计时器
	remainingTime    time.Duration // 暂停时剩余的时间
	turnDeadline     time.Time     // 当前回合的服务端绝对截止时间
	timerMu          sync.Mutex

	// actionMu preserves commit/delivery order without holding mu during client
	// delivery. Reentrant state reads remain possible while messages are sent.
	actionMu sync.Mutex
	mu       sync.RWMutex
}

// SetQuiescenceRelease transfers one server start lease to the session. A
// lease attached after an early retirement is released immediately.
func (gs *GameSession) SetQuiescenceRelease(release func()) {
	if gs == nil || release == nil {
		return
	}
	gs.quiescenceMu.Lock()
	if gs.quiescenceDone {
		gs.quiescenceMu.Unlock()
		release()
		return
	}
	if gs.quiescenceRelease != nil {
		gs.quiescenceMu.Unlock()
		release()
		return
	}
	gs.quiescenceRelease = release
	gs.quiescenceMu.Unlock()
}

func (gs *GameSession) releaseQuiescence() {
	if gs == nil {
		return
	}
	gs.quiescenceMu.Lock()
	if gs.quiescenceDone {
		gs.quiescenceMu.Unlock()
		return
	}
	gs.quiescenceDone = true
	release := gs.quiescenceRelease
	gs.quiescenceRelease = nil
	gs.quiescenceMu.Unlock()
	if release != nil {
		release()
	}
}

// SetMetrics installs the process-local recorder before Start.
func (gs *GameSession) SetMetrics(metrics *observability.Metrics) {
	if gs == nil {
		return
	}
	gs.mu.Lock()
	gs.metrics = metrics
	gs.mu.Unlock()
}

// NewGameSession 创建游戏会话
func NewGameSession(r *room.Room, lb *storage.LeaderboardManager, gameCfg config.GameConfig) *GameSession {
	return NewGameSessionWithPlayers(r, r.SnapshotPlayers(), lb, gameCfg)
}

// NewGameSessionWithPlayers builds a session from the membership snapshot
// committed with the room's Ready transition. Membership may change after the
// room lock is released, but it cannot shrink the starting game roster.
func NewGameSessionWithPlayers(r *room.Room, roomPlayers []room.PlayerSnapshot, lb *storage.LeaderboardManager, gameCfg config.GameConfig) *GameSession {
	players := make([]*GamePlayer, len(roomPlayers))
	for i, rp := range roomPlayers {
		players[i] = &GamePlayer{
			ID:        rp.ID,
			Name:      rp.Name,
			Seat:      rp.Seat,
			Ready:     rp.Ready,
			IsOffline: rp.Client == nil,
			IsBot:     rp.IsBot,
		}
	}

	return &GameSession{
		room:              r,
		leaderboard:       lb,
		gameConfig:        gameCfg,
		state:             GameStateInit,
		players:           players,
		landlordCaller:    -1,
		landlordCandidate: -1,
		lastPlayerIdx:     -1,
		bidMultiplier:     1,
	}
}

// RoomIdentity returns the immutable Room pointer this session was created
// from. Lifecycle registries use pointer identity to reject stale sessions when
// a room code is reused.
func (gs *GameSession) RoomIdentity() *room.Room {
	if gs == nil {
		return nil
	}
	return gs.room
}

// SetRoomManager installs the exact room lifecycle used for delivery. The
// pointer is atomic because registration may race a retiring test seam, while
// normal production registration sets it before Start.
func (gs *GameSession) SetRoomManager(manager *room.RoomManager) {
	if gs != nil {
		gs.roomManager.Store(manager)
	}
}

// SyncRoomPresence applies a room membership snapshot while a newly-created
// session is being registered. The handler serializes this with disconnect and
// reconnect lookups so an event in the construction window is not lost.
func (gs *GameSession) SyncRoomPresence(roomPlayers []room.PlayerSnapshot) {
	online := make(map[string]bool, len(roomPlayers))
	for _, player := range roomPlayers {
		online[player.ID] = player.Client != nil
	}

	gs.mu.Lock()
	defer gs.mu.Unlock()
	for _, player := range gs.players {
		player.IsOffline = !online[player.ID]
	}
}
