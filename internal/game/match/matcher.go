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

const queueTimeout = 30 * time.Second

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
	inflight        map[string]struct{}
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
		inflight:        make(map[string]struct{}),
	}
}

// AddToQueue 加入匹配队列
func (m *Matcher) AddToQueue(client types.ClientInterface) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 同一个玩家不能同时出现在等待队列或已经出队的匹配中。
	if _, matching := m.inflight[client.GetID()]; matching {
		return false
	}
	for _, c := range m.queue {
		if c.GetID() == client.GetID() {
			return false
		}
	}

	m.queue = append(m.queue, client)
	log.Printf("🔍 玩家 %s 加入匹配队列，当前队列长度: %d", client.GetName(), len(m.queue))
	client.SendMessage(codec.MustNewMessage(protocol.MsgMatchQueued, protocol.MatchQueuedPayload{
		DeadlineMS: time.Now().Add(queueTimeout).UnixMilli(),
		Practice:   false,
	}))

	switch {
	case len(m.queue) >= 3:
		m.cancelBotFillTimer()
		m.tryMatch()
	case m.botCfg.Enabled && m.botEngine != nil && m.botFillTimer == nil:
		m.startBotFillTimer()
	default:
		m.tryMatch()
	}

	return true
}

// RemoveFromQueue 从匹配队列移除
func (m *Matcher) RemoveFromQueue(client types.ClientInterface) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, c := range m.queue {
		if c.GetID() == client.GetID() {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			log.Printf("🔍 玩家 %s 离开匹配队列", client.GetName())
			if len(m.queue) == 0 {
				m.cancelBotFillTimer()
			}
			return true
		}
	}

	return false
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
	for _, client := range players {
		m.inflight[client.GetID()] = struct{}{}
	}

	// 创建房间
	go m.createMatchRoom(players)
}

// createMatchRoom 创建匹配房间
func (m *Matcher) createMatchRoom(players []types.ClientInterface) {
	defer m.finishMatch(players)

	// 创建房间（使用第一个玩家）
	room, err := m.roomManager.CreateRoom(players[0])
	if err != nil {
		log.Printf("匹配创建房间失败: %v", err)
		// 将玩家放回队列
		m.mu.Lock()
		for _, client := range players {
			delete(m.inflight, client.GetID())
		}
		m.queue = append(players, m.queue...) // 先到先匹配
		if m.botCfg.Enabled && m.botEngine != nil && m.botFillTimer == nil {
			m.startBotFillTimer()
		}
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

	// 给所有玩家发送匹配成功消息和房间信息。
	for _, client := range players {
		playerInfo, ok := room.GetPlayerInfo(client.GetID())
		if !ok {
			log.Printf("匹配房间 %s 缺少玩家 %s", room.Code, client.GetID())
			continue
		}
		// 发送加入房间成功消息
		client.SendMessage(codec.MustNewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
			RoomCode: room.Code,
			Player:   playerInfo,
			Players:  room.GetAllPlayersInfo(),
		}))
	}

	// 自动准备所有玩家
	room.SetAllPlayersReady()

	// 广播所有玩家准备状态
	for _, player := range room.SnapshotPlayers() {
		room.Broadcast(codec.MustNewMessage(protocol.MsgPlayerReady, protocol.PlayerReadyPayload{
			PlayerID: player.ID,
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
		code := room.Code
		data := room.ToRoomData()
		go func() {
			if err := m.redisStore.SaveRoom(context.Background(), code, data); err != nil {
				log.Printf("保存匹配房间 %s 到 Redis 失败: %v", code, err)
			}
		}()
	}
}

func (m *Matcher) finishMatch(players []types.ClientInterface) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, client := range players {
		delete(m.inflight, client.GetID())
	}
}

// PracticeMatch 人机练习：立即为玩家创建含 2 个机器人的房间。
func (m *Matcher) PracticeMatch(client types.ClientInterface) bool {
	m.mu.Lock()
	if _, matching := m.inflight[client.GetID()]; matching {
		m.mu.Unlock()
		return false
	}
	for _, queued := range m.queue {
		if queued.GetID() == client.GetID() {
			m.mu.Unlock()
			return false
		}
	}
	m.inflight[client.GetID()] = struct{}{}
	m.mu.Unlock()

	engine := m.botEngine
	if engine == nil {
		engine = bot.NewHeuristicEngine()
	}
	bot1 := bot.NewBotClient(engine)
	bot2 := bot.NewBotClient(engine)
	client.SendMessage(codec.MustNewMessage(protocol.MsgMatchQueued, protocol.MatchQueuedPayload{
		DeadlineMS: time.Now().Add(queueTimeout).UnixMilli(),
		Practice:   true,
	}))
	go m.createMatchRoom([]types.ClientInterface{client, bot1, bot2})
	return true
}

// GetQueueLength 获取队列长度
func (m *Matcher) GetQueueLength() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}
