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

	mu sync.RWMutex
}

// NewGameSession 创建游戏会话
func NewGameSession(r *room.Room, lb *storage.LeaderboardManager, gameCfg config.GameConfig) *GameSession {
	playerOrder := r.PlayerOrder
	players := make([]*GamePlayer, len(playerOrder))
	for i, id := range playerOrder {
		rp := r.Players[id]
		name := id
		ready := false
		isBot := false
		if rp != nil {
			ready = rp.Ready
			if rp.Client != nil {
				name = rp.Client.GetName()
				isBot = rp.Client.IsBot()
			}
		}
		players[i] = &GamePlayer{
			ID:    id,
			Name:  name,
			Seat:  i,
			Ready: ready,
			IsBot: isBot,
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
