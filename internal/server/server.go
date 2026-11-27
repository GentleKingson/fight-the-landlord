package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync"
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
	"github.com/palemoky/fight-the-landlord/internal/types"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return NewOriginChecker(nil).Check(r)
	},
	// 启用 permessage-deflate 压缩扩展
	// 可减少 40-70% 流量，gorilla/websocket 会自动协商压缩参数
	// 压缩会对CPU和内存造成压力，只有在大文件压缩才有收益，大量小文件反而是负优化
	EnableCompression: false,
}

// Server WebSocket 服务器
type Server struct {
	recoveryCancel context.CancelFunc
	recoveryLoaded bool // Startup restoration is idempotent within this process.
	config         *config.Config
	redis          *redis.Client
	redisStore     *storage.RedisStore
	leaderboard    *storage.LeaderboardManager
	roomManager    *room.RoomManager
	matcher        *match.Matcher
	sessionManager *session.SessionManager
	clients        map[string]*Client
	clientsMu      sync.RWMutex
	connectionMu   sync.Mutex // Serializes identity replacement with all disconnect side effects.
	handler        *handler.Handler

	// 安全组件
	rateLimiter    *RateLimiter
	originChecker  *OriginChecker
	ipResolver     *ClientIPResolver
	messageLimiter *MessageRateLimiter
	ipFilter       *IPFilter

	// 连接控制与 HTTP 服务
	maxConnections int
	semaphore      chan struct{} // 信号量控制并发连接数（nil 表示无限制）
	httpMu         sync.Mutex
	httpServer     *http.Server

	// 维护模式
	maintenanceMode bool
	maintenanceMu   sync.RWMutex
}

func connectRedis(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		ContextTimeoutEnabled: true,
		Addr:                  cfg.Addr,
		Password:              cfg.Password,
		DB:                    cfg.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis 连接失败: %w", err)
	}
	return rdb, nil
}

func newBotEngine(cfg config.BotConfig) bot.DecisionEngine {
	if !cfg.Enabled {
		return nil
	}
	if cfg.DouZeroEnabled {
		log.Printf("🎮 DouZero 引擎已启用（服务地址: %s，等待超时: %ds）", cfg.DouZeroURL, cfg.BotFillTimeout)
		return bot.NewDouZeroEngine(cfg.DouZeroURL)
	}
	log.Printf("🤖 规则启发式机器人已启用（等待超时: %ds）", cfg.BotFillTimeout)
	return bot.NewHeuristicEngine()
}

// NewServer 创建服务器实例
func NewServer(cfg *config.Config) (*Server, error) {
	if cfg.Server.IdentityTTL < 0 || cfg.Game.OfflineRoomRetention < 0 || cfg.Game.ResultRetention < 0 || cfg.Game.OfflineWaitTimeout < 0 {
		return nil, fmt.Errorf("identity and retention durations must be non-negative")
	}
	if err := validateWebConfig(cfg); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb, err := connectRedis(ctx, cfg.Redis)
	if err != nil {
		return nil, err
	}

	var sem chan struct{}
	if cfg.Server.MaxConnections > 0 {
		sem = make(chan struct{}, cfg.Server.MaxConnections)
	}

	s := &Server{
		config:         cfg,
		redis:          rdb,
		redisStore:     storage.NewRedisStore(rdb),
		leaderboard:    storage.NewLeaderboardManager(rdb),
		clients:        make(map[string]*Client),
		sessionManager: session.NewSessionManager(cfg.Game.ResultRetentionDuration()),
		rateLimiter: NewRateLimiter(
			cfg.Security.RateLimit.MaxPerSecond,
			cfg.Security.RateLimit.MaxPerMinute,
			cfg.Security.RateLimit.BanDurationTime(),
		),
		originChecker:  NewOriginChecker(cfg.Security.AllowedOrigins),
		ipResolver:     NewClientIPResolver(cfg.Security.TrustedProxies),
		messageLimiter: NewMessageRateLimiter(cfg.Security.MessageLimit.MaxPerSecond),
		ipFilter:       NewIPFilter(),
		maxConnections: cfg.Server.MaxConnections,
		semaphore:      sem,
	}

	s.roomManager = room.NewRoomManager(s.redisStore, cfg.Game)
	botEngine := newBotEngine(cfg.BOT)

	s.matcher = match.NewMatcher(match.MatcherDeps{
		RoomManager: s.roomManager,
		WithClients: s.withActiveClients,
		RedisStore:  s.redisStore,
		Leaderboard: s.leaderboard,
		GameConfig:  cfg.Game,
		BotEngine:   botEngine,
		BotConfig:   cfg.BOT,
		RegisterSession: func(roomCode string, gs *session.GameSession) {
			s.handler.SetGameSession(roomCode, gs)
		},
	})

	s.handler = handler.NewHandler(handler.HandlerDeps{
		Server:         s,
		RoomManager:    s.roomManager,
		Matcher:        s.matcher,
		Leaderboard:    s.leaderboard,
		SessionManager: s.sessionManager,
	})

	if botEngine == nil {
		botEngine = bot.NewHeuristicEngine()
	}
	s.roomManager.SetBotFactory(func() types.ClientInterface {
		return bot.NewBotClient(botEngine)
	})
	s.roomManager.SetOnGameStart(s.startRoomGame)
	if err := s.restoreGames(ctx, botEngine); err != nil {
		s.SuspendRecovery()
		_ = rdb.Close()
		return nil, fmt.Errorf("restore games: %w", err)
	}
	recoveryCtx, recoveryCancel := context.WithCancel(context.Background())
	s.recoveryCancel = recoveryCancel
	go s.settlementLoop(recoveryCtx)

	log.Printf("🔒 安全配置: 连接限制=%d/s, 消息限制=%d/s, 最大连接数=%d",
		cfg.Security.RateLimit.MaxPerSecond, cfg.Security.MessageLimit.MaxPerSecond, cfg.Server.MaxConnections)

	return s, nil
}

func (s *Server) startRoomGame(r *room.Room) {
	gs := session.NewGameSession(r, s.leaderboard, s.config.Game)
	for _, client := range r.GetClients() {
		if b, ok := client.(*bot.BotClient); ok {
			b.SetSession(gs)
		}
	}
	s.handler.SetGameSession(r.Code, gs)
	gs.Start()
}

// Start 启动服务器
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)

	// 启动监控 goroutine
	go s.monitorStats()

	log.Printf("🚀 服务器启动在 ws://%s/ws (CPU核心数: %d)", addr, runtime.NumCPU())
	server := &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second, // 防止 Slowloris 攻击
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	s.httpMu.Lock()
	s.httpServer = server
	s.httpMu.Unlock()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
