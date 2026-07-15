package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/server/handler"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
)

// Version 是服务端版本号，可由 cmd/server 在启动时通过编译注入的值覆盖。
var Version = "dev"

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许所有来源，生产环境需要限制
	},
	// 启用 permessage-deflate 压缩扩展
	// 可减少 40-70% 流量，gorilla/websocket 会自动协商压缩参数
	// 压缩会对CPU和内存造成压力，只有在大文件压缩才有收益，大量小文件反而是负优化
	EnableCompression: false,
}

// Server WebSocket 服务器
type Server struct {
	config         *config.Config
	redis          *redis.Client
	redisStore     *storage.RedisStore
	leaderboard    *storage.LeaderboardManager
	roomManager    *room.RoomManager
	matcher        *match.Matcher
	sessionManager *session.SessionManager
	clients        map[string]*Client
	clientsMu      sync.RWMutex
	handler        *handler.Handler

	// 安全组件
	rateLimiter    *RateLimiter
	originChecker  *OriginChecker
	messageLimiter *MessageRateLimiter
	chatLimiter    *ChatRateLimiter
	ipFilter       *IPFilter

	// 连接控制
	maxConnections        int
	connectionLimiterOnce sync.Once
	connectionLimiter     *connectionLimiter
	slowClientDisconnects atomic.Int64

	// 维护模式
	maintenanceMode bool
	maintenanceMu   sync.RWMutex
}

// NewServer 创建服务器实例
func NewServer(cfg *config.Config) (*Server, error) {
	// 初始化 Redis 客户端
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	// 测试 Redis 连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis 连接失败: %w", err)
	}

	s := &Server{
		config:         cfg,
		redis:          rdb,
		redisStore:     storage.NewRedisStore(rdb),
		leaderboard:    storage.NewLeaderboardManager(rdb),
		clients:        make(map[string]*Client),
		sessionManager: session.NewSessionManager(),
		// 初始化安全组件
		rateLimiter: NewRateLimiter(
			cfg.Security.RateLimit.MaxPerSecond,
			cfg.Security.RateLimit.MaxPerMinute,
			cfg.Security.RateLimit.BanDurationTime(),
		),
		originChecker:  NewOriginChecker(cfg.Security.AllowedOrigins),
		messageLimiter: NewMessageRateLimiter(cfg.Security.MessageLimit.MaxPerSecond),
		chatLimiter: NewChatRateLimiter(
			cfg.Security.ChatLimit.MaxPerSecond,
			cfg.Security.ChatLimit.MaxPerMinute,
			cfg.Security.ChatLimit.CooldownDuration(),
		),
		ipFilter: NewIPFilter(),
		// 初始化连接控制
		maxConnections:    cfg.Server.MaxConnections,
		connectionLimiter: newConnectionLimiter(cfg.Server.MaxConnections),
	}

	// 初始化房间管理器
	s.roomManager = room.NewRoomManager(s.redisStore, cfg.Game)

	// 初始化机器人 (未启用时为 nil）
	var botEngine bot.DecisionEngine
	if cfg.BOT.Enabled {
		if cfg.BOT.DouZeroEnabled {
			botEngine = bot.NewDouZeroEngine(cfg.BOT.DouZeroURL)
			log.Printf("🎮 DouZero 引擎已启用（服务地址: %s，等待超时: %ds）", cfg.BOT.DouZeroURL, cfg.BOT.BotFillTimeout)
		} else {
			botEngine = bot.NewHeuristicEngine()
			log.Printf("🤖 规则启发式机器人已启用（等待超时: %ds）", cfg.BOT.BotFillTimeout)
		}
	}

	// 初始化匹配器
	s.matcher = match.NewMatcher(match.MatcherDeps{
		RoomManager:         s.roomManager,
		RedisStore:          s.redisStore,
		Leaderboard:         s.leaderboard,
		GameConfig:          cfg.Game,
		BotEngine:           botEngine,
		BotConfig:           cfg.BOT,
		ResolveActiveClient: s.GetClientByID,
		RegisterSession: func(roomCode string, gs *session.GameSession) bool {
			return s.handler.SetGameSession(roomCode, gs)
		},
	})

	// 初始化消息处理器
	s.handler = handler.NewHandler(handler.HandlerDeps{
		Server:         s,
		RoomManager:    s.roomManager,
		Matcher:        s.matcher,
		ChatLimiter:    s.chatLimiter,
		Leaderboard:    s.leaderboard,
		SessionManager: s.sessionManager,
	})

	// 设置房间游戏开始回调
	s.roomManager.SetOnGameStart(func(r *room.Room, players []room.PlayerSnapshot) {
		gs := session.NewGameSessionWithPlayers(r, players, s.leaderboard, s.config.Game)
		if !s.handler.SetGameSession(r.Code, gs) {
			return
		}
		for _, player := range players {
			if player.Client == nil {
				continue
			}
			if botClient, ok := player.Client.(*bot.BotClient); ok {
				botClient.SetSession(gs)
			}
		}
		gs.Start()
	})

	log.Printf("🔒 安全配置: 连接限制=%d/s, 消息限制=%d/s, 聊天限制=%d/s, 最大连接数=%d",
		cfg.Security.RateLimit.MaxPerSecond, cfg.Security.MessageLimit.MaxPerSecond, cfg.Security.ChatLimit.MaxPerSecond, cfg.Server.MaxConnections)

	return s, nil
}

// Start 启动服务器
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)
	handler := s.httpHandler(loadWebAssets())

	// 启动监控 goroutine
	go s.monitorStats()

	log.Printf("🚀 服务器启动在 ws://%s/ws (CPU核心数: %d)", addr, runtime.NumCPU())
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second, // 防止 Slowloris 攻击
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return server.ListenAndServe()
}
