package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/observability"
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
	metrics        *observability.Metrics
	logger         *slog.Logger

	// 安全组件
	rateLimiter    *RateLimiter
	originChecker  *OriginChecker
	messageLimiter *MessageRateLimiter
	chatLimiter    *ChatRateLimiter
	ipFilter       *IPFilter
	ipResolver     *ClientIPResolver

	// 连接控制
	maxConnections        int
	connectionLimiterOnce sync.Once
	connectionLimiter     *connectionLimiter
	slowClientDisconnects atomic.Int64
	commandCacheOnce      sync.Once
	commandCache          *commandCache
	webSessionTicketsOnce sync.Once
	webSessionTickets     *webSessionTicketManager
	sessionAuthorityMu    sync.RWMutex
	// retiredBrowserClients is guarded by sessionAuthorityMu. A generation is
	// retained only until its last pre-replacement command crosses webCommandMu.
	retiredBrowserClients map[string]map[*Client]string
	browserRevokeDrains   map[string]*browserRevokeDrain
	readinessCheck        func(context.Context) error
	shuttingDown          atomic.Bool
	runtimeCtx            context.Context
	runtimeCancel         context.CancelFunc
	monitorOnce           sync.Once
	monitorWG             sync.WaitGroup
	shutdownOnce          sync.Once
	httpServerMu          sync.Mutex
	httpServer            *http.Server
	httpServeWG           sync.WaitGroup
	clientPumpsMu         sync.Mutex
	clientPumpsClosed     bool
	clientPumpsWG         sync.WaitGroup

	// 运营控制
	operationalMu    sync.Mutex
	operationalState atomic.Uint32
	moderationOnce   sync.Once
	moderation       *moderationStore
	adminLimiterOnce sync.Once
	adminLimiter     *adminRateLimiter
}

type browserRevokeDrain struct {
	clients      map[*Client]struct{}
	credentials  map[string]struct{}
	dependencies map[*browserRevokeDrain]struct{}
	done         chan struct{}
	completeOnce sync.Once
}

// NewServer 创建服务器实例
func NewServer(cfg *config.Config) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	ipResolver, err := NewClientIPResolver(cfg.Security.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	metrics := observability.NewMetrics(cfg.Observability.MetricsEnabled)
	rdb, err := connectRedis(cfg, metrics)
	if err != nil {
		return nil, err
	}
	runtimeCtx, runtimeCancel := context.WithCancel(context.Background()) //nolint:gosec // Server.Shutdown owns runtimeCancel.
	s := newServerRuntime(cfg, rdb, ipResolver, runtimeCtx, runtimeCancel, metrics)
	s.initializeGameRuntime(cfg)

	log.Printf("🔒 安全配置: 连接限制=%d/s, 消息限制=%d/s, 聊天限制=%d/s, 最大连接数=%d",
		cfg.Security.RateLimit.MaxPerSecond, cfg.Security.MessageLimit.MaxPerSecond, cfg.Security.ChatLimit.MaxPerSecond, cfg.Server.MaxConnections)
	log.Printf("🔒 WebSocket Origin 白名单: %s", strings.Join(cfg.Security.AllowedOrigins, ", "))
	return s, nil
}

func connectRedis(cfg *config.Config, metrics *observability.Metrics) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB,
	})
	if metrics != nil && metrics.Enabled() {
		rdb.AddHook(observability.NewRedisHook(metrics))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis 连接失败: %w", err)
	}
	metrics.SetReady(true)
	return rdb, nil
}

func newServerRuntime(
	cfg *config.Config,
	rdb *redis.Client,
	ipResolver *ClientIPResolver,
	runtimeCtx context.Context,
	runtimeCancel context.CancelFunc,
	metrics *observability.Metrics,
) *Server {
	s := &Server{
		config:         cfg,
		redis:          rdb,
		redisStore:     storage.NewRedisStore(rdb),
		leaderboard:    storage.NewLeaderboardManager(rdb),
		clients:        make(map[string]*Client),
		sessionManager: session.NewSessionManagerWithContext(runtimeCtx),
		// 初始化安全组件
		rateLimiter: NewRateLimiterWithContext(
			runtimeCtx,
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
		ipFilter:   NewIPFilter(),
		ipResolver: ipResolver,
		// 初始化连接控制
		maxConnections: cfg.Server.MaxConnections,
		connectionLimiter: newConnectionLimiter(cfg.Server.MaxConnections, func(active int) {
			if metrics != nil {
				metrics.SetConnectionsCurrent(active)
			}
		}),
		commandCache:      newCommandCache(defaultCommandCacheCapacity, defaultCommandCacheTTL),
		webSessionTickets: newWebSessionTicketManager(),
		runtimeCtx:        runtimeCtx,
		runtimeCancel:     runtimeCancel,
		metrics:           metrics,
		logger:            slog.Default().With("component", "server"),
	}
	s.readinessCheck = func(ctx context.Context) error { return rdb.Ping(ctx).Err() }
	return s
}

func (s *Server) initializeGameRuntime(cfg *config.Config) {
	s.roomManager = room.NewRoomManagerWithContext(s.runtimeCtx, s.redisStore, cfg.Game)
	s.roomManager.SetMetrics(s.metrics)
	botEngine := configuredBotEngine(cfg, s.metrics)
	s.matcher = match.NewMatcher(match.MatcherDeps{
		Context:             s.runtimeCtx,
		Metrics:             s.metrics,
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
	s.handler = handler.NewHandler(handler.HandlerDeps{
		Server:         s,
		RoomManager:    s.roomManager,
		Matcher:        s.matcher,
		ChatLimiter:    s.chatLimiter,
		Leaderboard:    s.leaderboard,
		SessionManager: s.sessionManager,
		Metrics:        s.metrics,
	})
	s.roomManager.SetOnGameStart(func(r *room.Room, players []room.PlayerSnapshot) {
		gs := session.NewGameSessionWithPlayers(r, players, s.leaderboard, s.config.Game)
		gs.SetMetrics(s.metrics)
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
}

func configuredBotEngine(cfg *config.Config, metrics *observability.Metrics) bot.DecisionEngine {
	if !cfg.BOT.Enabled {
		return nil
	}
	if cfg.BOT.DouZeroEnabled {
		log.Printf("🎮 DouZero 引擎已启用（服务地址: %s，等待超时: %ds）", cfg.BOT.DouZeroURL, cfg.BOT.BotFillTimeout)
		engine := bot.NewDouZeroEngine(cfg.BOT.DouZeroURL)
		engine.SetMetrics(metrics)
		return bot.InstrumentEngine(engine, "douzero", metrics)
	}
	log.Printf("🤖 规则启发式机器人已启用（等待超时: %ds）", cfg.BOT.BotFillTimeout)
	return bot.InstrumentEngine(bot.NewHeuristicEngine(), "heuristic", metrics)
}

func (s *Server) activeCommandCache() *commandCache {
	s.commandCacheOnce.Do(func() {
		if s.commandCache == nil {
			s.commandCache = newCommandCache(defaultCommandCacheCapacity, defaultCommandCacheTTL)
		}
	})
	return s.commandCache
}

func (s *Server) LegacyChatMessages() int64 {
	if s == nil || s.handler == nil {
		return 0
	}
	return s.handler.LegacyChatMessages()
}

// Start 启动服务器
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)
	handler := s.httpHandler(loadWebAssets())

	log.Printf("🚀 服务器启动在 ws://%s/ws (CPU核心数: %d)", addr, runtime.NumCPU())
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second, // 防止 Slowloris 攻击
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	s.httpServerMu.Lock()
	if s.shuttingDown.Load() {
		s.httpServerMu.Unlock()
		return nil
	}
	s.httpServer = httpServer
	s.httpServeWG.Add(1)
	s.startMonitorStats()
	s.httpServerMu.Unlock()
	defer func() {
		s.httpServerMu.Lock()
		if s.httpServer == httpServer {
			s.httpServer = nil
		}
		s.httpServerMu.Unlock()
		s.httpServeWG.Done()
	}()

	err := httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
