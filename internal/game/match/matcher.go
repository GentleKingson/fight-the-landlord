package match

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// SessionRegistrationFunc 游戏会话注册回调
type SessionRegistrationFunc func(roomCode string, gs *session.GameSession)

// Matcher 匹配系统
type Matcher struct {
	roomManager     *room.RoomManager
	redisStore      *storage.RedisStore
	leaderboard     *storage.LeaderboardManager
	gameConfig      config.GameConfig
	botEngine       bot.DecisionEngine
	botCfg          config.BotConfig
	registerSession SessionRegistrationFunc
	queue           []types.ClientInterface
	botFillTimer    *time.Timer
	mu              sync.Mutex
}

// MatcherDeps 匹配器依赖
type MatcherDeps struct {
	RoomManager     *room.RoomManager
	RedisStore      *storage.RedisStore
	Leaderboard     *storage.LeaderboardManager
	GameConfig      config.GameConfig
	BotEngine       bot.DecisionEngine
	BotConfig       config.BotConfig
	RegisterSession SessionRegistrationFunc
}

// NewMatcher 创建匹配器
func NewMatcher(deps MatcherDeps) *Matcher {
	return &Matcher{
		roomManager:     deps.RoomManager,
		redisStore:      deps.RedisStore,
		leaderboard:     deps.Leaderboard,
		gameConfig:      deps.GameConfig,
		botEngine:       deps.BotEngine,
		botCfg:          deps.BotConfig,
		registerSession: deps.RegisterSession,
		queue:           make([]types.ClientInterface, 0),
	}
}

// AddToQueue 加入匹配队列
func (m *Matcher) AddToQueue(client types.ClientInterface) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已在队列中
	for _, c := range m.queue {
		if c.GetID() == client.GetID() {
			return
		}
	}

	m.queue = append(m.queue, client)
	log.Printf("🔍 玩家 %s 加入匹配队列，当前队列长度: %d", client.GetName(), len(m.queue))

	switch {
	case len(m.queue) >= 3:
		m.cancelBotFillTimer()
		m.tryMatch()
	case m.botCfg.Enabled && m.botEngine != nil && m.botFillTimer == nil:
		m.startBotFillTimer()
	default:
		m.tryMatch()
	}
}

// RemoveFromQueue 从匹配队列移除
func (m *Matcher) RemoveFromQueue(client types.ClientInterface) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, c := range m.queue {
		if c.GetID() == client.GetID() {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			log.Printf("🔍 玩家 %s 离开匹配队列", client.GetName())
			if len(m.queue) == 0 {
				m.cancelBotFillTimer()
			}
			return
		}
	}
}

func (m *Matcher) startBotFillTimer() {
	timeout := time.Duration(m.botCfg.BotFillTimeout) * time.Second
	log.Printf("🤖 等待玩家加入（%ds 后由 Bot 填充剩余座位）", m.botCfg.BotFillTimeout)
	m.botFillTimer = time.AfterFunc(timeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.botFillTimer = nil
		if len(m.queue) == 0 {
			return
		}
		for len(m.queue) < 3 {
			bot := bot.NewBotClient(m.botEngine)
			m.queue = append(m.queue, bot)
			log.Printf("🤖 Bot %s 加入匹配队列", bot.GetName())
		}
		m.tryMatch()
	})
}

func (m *Matcher) cancelBotFillTimer() {
	if m.botFillTimer != nil {
		m.botFillTimer.Stop()
		m.botFillTimer = nil
	}
}

// tryMatch 尝试匹配
func (m *Matcher) tryMatch() {
	if len(m.queue) < 3 {
		return
	}

	// 取出前 3 个玩家
	players := m.queue[:3]
	m.queue = m.queue[3:]

	// 创建房间
	go m.createMatchRoom(players)
}

// createMatchRoom 创建匹配房间
func (m *Matcher) createMatchRoom(players []types.ClientInterface) {
	// 创建房间（使用第一个玩家）
	room, err := m.roomManager.CreateRoom(players[0])
	if err != nil {
		log.Printf("匹配创建房间失败: %v", err)
		// 将玩家放回队列
		m.mu.Lock()
		m.queue = append(players, m.queue...) // 先到先匹配
		m.mu.Unlock()
		return
	}

	// 其他玩家加入房间
	for _, client := range players[1:] {
		if _, err := m.roomManager.JoinRoom(client, room.Code); err != nil {
			log.Printf("匹配加入房间失败: %v", err)
		}
	}

	log.Printf("🎮 匹配成功！房间 %s，玩家: %s, %s, %s",
		room.Code, players[0].GetName(), players[1].GetName(), players[2].GetName())

	// 给所有玩家发送匹配成功消息和房间信息
	time.Sleep(100 * time.Millisecond) // 短暂延迟确保房间状态同步

	for _, client := range players {
		// 发送加入房间成功消息
		client.SendMessage(codec.MustNewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
			RoomCode: room.Code,
			Player:   room.GetPlayerInfo(client.GetID()),
			Players:  room.GetAllPlayersInfo(),
		}))
	}

	// 自动准备所有玩家
	room.SetAllPlayersReady()

	// 广播所有玩家准备状态
	for _, player := range room.Players {
		room.Broadcast(codec.MustNewMessage(protocol.MsgPlayerReady, protocol.PlayerReadyPayload{
			PlayerID: player.Client.GetID(),
			Ready:    true,
		}))
	}

	// 开始游戏
	if err := room.StartGame(); err != nil {
		log.Printf("匹配开始游戏失败: %v", err)
		return
	}

	// 创建游戏会话并开始
	gs := session.NewGameSession(room, m.leaderboard, m.gameConfig)

	// 将 session 注入机器人（BotClient 通过 SessionInterface 回调出牌）
	for _, client := range players {
		if bot, ok := client.(*bot.BotClient); ok {
			bot.SetSession(gs)
		}
	}

	// 注册游戏会话
	if m.registerSession != nil {
		m.registerSession(room.Code, gs)
	}

	gs.Start()

	// 保存房间状态
	if m.redisStore != nil && m.redisStore.IsReady() {
		go func() { _ = m.redisStore.SaveRoom(context.Background(), room.Code, room.ToRoomData()) }()
	}
}

// PracticeMatch 人机练习：立即为玩家创建含 2 个机器人的房间
func (m *Matcher) PracticeMatch(client types.ClientInterface) {
	engine := m.botEngine
	if engine == nil {
		engine = bot.NewHeuristicEngine()
	}
	bot1 := bot.NewBotClient(engine)
	bot2 := bot.NewBotClient(engine)
	go m.createMatchRoom([]types.ClientInterface{client, bot1, bot2})
}

// GetQueueLength 获取队列长度
func (m *Matcher) GetQueueLength() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}
