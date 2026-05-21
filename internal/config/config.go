package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// 默认配置值
const (
	defaultHost                  = "0.0.0.0"
	defaultPort                  = 1780
	defaultMaxConnections        = 10000
	defaultRedisAddr             = "localhost:6379"
	defaultTurnTimeout           = 30
	defaultBidTimeout            = 15
	defaultRoomTimeout           = 10
	defaultShutdownTimeout       = 30
	defaultShutdownCheckInterval = 15
	defaultRoomCleanupDelay      = 30
	defaultOfflineWaitTimeout    = 30
	defaultRateLimitPerSecond    = 10
	defaultRateLimitPerMinute    = 60
	defaultBanDuration           = 60
	defaultMessageLimitPerSecond = 20
	defaultChatLimitPerSecond    = 1
	defaultChatLimitPerMinute    = 30
	defaultChatCooldown          = 5
)

// Config 服务端配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Redis    RedisConfig    `yaml:"redis"`
	Game     GameConfig     `yaml:"game"`
	Security SecurityConfig `yaml:"security"`
	AI       AIConfig       `yaml:"ai"`
}

// AIConfig AI 机器人配置
type AIConfig struct {
	Enabled        bool   `yaml:"enabled"`
	BaseURL        string `yaml:"base_url"`
	APIKey         string `yaml:"api_key"`
	Model          string `yaml:"model"`
	BotFillTimeout int    `yaml:"bot_fill_timeout"` // 等待玩家加入的超时秒数
	MaxRetries     int    `yaml:"max_retries"`      // LLM 校验失败重试次数
	Debug          bool   `yaml:"debug"`            // 打印完整的 LLM 请求与响应
}

// ServerConfig WebSocket 服务器配置
type ServerConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	MaxConnections int    `yaml:"max_connections"` // 最大并发连接数，0 表示无限制
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// GameConfig 游戏配置
type GameConfig struct {
	TurnTimeout           int `yaml:"turn_timeout"`            // 出牌超时（秒）
	BidTimeout            int `yaml:"bid_timeout"`             // 叫地主超时（秒）
	RoomTimeout           int `yaml:"room_timeout"`            // 房间等待超时（分钟）
	ShutdownTimeout       int `yaml:"shutdown_timeout"`        // 优雅关闭超时（分钟）
	ShutdownCheckInterval int `yaml:"shutdown_check_interval"` // 优雅关闭检测间隔（秒）
	RoomCleanupDelay      int `yaml:"room_cleanup_delay"`      // 游戏结束后服务器关闭延迟（秒）
	OfflineWaitTimeout    int `yaml:"offline_wait_timeout"`    // 玩家离线等待超时（秒）
}

// SecurityConfig 安全配置
type SecurityConfig struct {
	AllowedOrigins []string           `yaml:"allowed_origins"` // 允许的来源
	RateLimit      RateLimitConfig    `yaml:"rate_limit"`      // 连接速率限制
	MessageLimit   MessageLimitConfig `yaml:"message_limit"`   // 消息速率限制
	ChatLimit      ChatLimitConfig    `yaml:"chat_limit"`      // 聊天消息速率限制
}

// RateLimitConfig 连接速率限制配置
type RateLimitConfig struct {
	MaxPerSecond int `yaml:"max_per_second"` // 每秒最大连接数
	MaxPerMinute int `yaml:"max_per_minute"` // 每分钟最大连接数
	BanDuration  int `yaml:"ban_duration"`   // 封禁时长（秒）
}

// MessageLimitConfig 消息速率限制配置
type MessageLimitConfig struct {
	MaxPerSecond int `yaml:"max_per_second"` // 每秒最大消息数
}

// ChatLimitConfig 聊天消息速率限制配置
type ChatLimitConfig struct {
	MaxPerSecond int `yaml:"max_per_second"` // 每秒最大聊天消息数
	MaxPerMinute int `yaml:"max_per_minute"` // 每分钟最大聊天消息数
	Cooldown     int `yaml:"cooldown"`       // 冷却时间（秒）
}

// Duration 方法
func (c *GameConfig) TurnTimeoutDuration() time.Duration {
	return time.Duration(c.TurnTimeout) * time.Second
}

func (c *GameConfig) BidTimeoutDuration() time.Duration {
	return time.Duration(c.BidTimeout) * time.Second
}

func (c *GameConfig) RoomTimeoutDuration() time.Duration {
	return time.Duration(c.RoomTimeout) * time.Minute
}

func (c *GameConfig) ShutdownTimeoutDuration() time.Duration {
	return time.Duration(c.ShutdownTimeout) * time.Minute
}

func (c *GameConfig) ShutdownCheckIntervalDuration() time.Duration {
	return time.Duration(c.ShutdownCheckInterval) * time.Second
}

func (c *GameConfig) RoomCleanupDelayDuration() time.Duration {
	return time.Duration(c.RoomCleanupDelay) * time.Second
}

func (c *GameConfig) OfflineWaitTimeoutDuration() time.Duration {
	return time.Duration(c.OfflineWaitTimeout) * time.Second
}

func (c *RateLimitConfig) BanDurationTime() time.Duration {
	return time.Duration(c.BanDuration) * time.Second
}

func (c *ChatLimitConfig) CooldownDuration() time.Duration {
	return time.Duration(c.Cooldown) * time.Second
}

// Load 加载配置文件
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	setDefaults(&cfg)

	// 本地开发便利：自动加载 .env.local（仅本地，已 gitignore）。
	// .env 是 Docker 专用（含 REDIS_ADDR=redis:6379 等容器内地址），
	if err := godotenv.Load(".env.local"); err != nil && !os.IsNotExist(err) {
		log.Printf("⚠️  加载 .env.local 失败: %v", err)
	}
	loadFromEnv(&cfg)

	if cfg.AI.Enabled && cfg.AI.APIKey == "" {
		log.Print("⚠️  AI 已启用但未配置 API Key，将回退到本地规则出牌。" +
			"请通过环境变量 AI_API_KEY 或 AI_API_KEY_FILE（Docker/K8s secret）配置")
	}

	return &cfg, nil
}

// --- 环境变量辅助函数 ---

func getEnvStr(key string, target *string) {
	if v := os.Getenv(key); v != "" {
		*target = v
	}
}

func getEnvInt(key string, target *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*target = n
		}
	}
}

// getEnvSecret 优先从 <KEY>_FILE 指向的文件读取密钥（Docker/K8s secret 推荐方式，
// 避免密钥出现在配置文件或进程环境变量中），否则回退到 <KEY> 环境变量。
func getEnvSecret(key string, target *string) {
	if path := os.Getenv(key + "_FILE"); path != "" {
		data, err := os.ReadFile(filepath.Clean(path)) // #nosec G703 -- config secret path is provided by the operator and read as a local file
		if err != nil {
			log.Printf("⚠️  读取 %s_FILE 失败: %v", key, err)
		} else if v := strings.TrimSpace(string(data)); v != "" {
			*target = v
			return
		} else {
			log.Printf("⚠️  %s_FILE 指向的文件为空: %s", key, path)
		}
	}
	getEnvStr(key, target)
}

func getEnvStrSlice(key string, target *[]string) {
	if v := os.Getenv(key); v != "" {
		*target = strings.Split(v, ",")
	}
}

// loadFromEnv 从环境变量加载配置（覆盖文件配置）
func loadFromEnv(cfg *Config) {
	// Server
	getEnvStr("SERVER_HOST", &cfg.Server.Host)
	getEnvInt("SERVER_PORT", &cfg.Server.Port)
	getEnvInt("SERVER_MAX_CONNECTIONS", &cfg.Server.MaxConnections)

	// Redis
	getEnvStr("REDIS_ADDR", &cfg.Redis.Addr)
	getEnvStr("REDIS_PASSWORD", &cfg.Redis.Password)
	getEnvInt("REDIS_DB", &cfg.Redis.DB)

	// Game
	getEnvInt("GAME_TURN_TIMEOUT", &cfg.Game.TurnTimeout)
	getEnvInt("GAME_BID_TIMEOUT", &cfg.Game.BidTimeout)
	getEnvInt("GAME_ROOM_TIMEOUT", &cfg.Game.RoomTimeout)
	getEnvInt("GAME_SHUTDOWN_TIMEOUT", &cfg.Game.ShutdownTimeout)
	getEnvInt("GAME_SHUTDOWN_CHECK_INTERVAL", &cfg.Game.ShutdownCheckInterval)
	getEnvInt("GAME_ROOM_CLEANUP_DELAY", &cfg.Game.RoomCleanupDelay)

	// AI
	if v := os.Getenv("AI_ENABLED"); v == "true" || v == "1" {
		cfg.AI.Enabled = true
	}
	getEnvSecret("AI_API_KEY", &cfg.AI.APIKey)
	getEnvStr("AI_BASE_URL", &cfg.AI.BaseURL)
	getEnvStr("AI_MODEL", &cfg.AI.Model)
	getEnvInt("AI_BOT_FILL_TIMEOUT", &cfg.AI.BotFillTimeout)
	getEnvInt("AI_MAX_RETRIES", &cfg.AI.MaxRetries)
	if v := os.Getenv("AI_DEBUG"); v == "true" || v == "1" {
		cfg.AI.Debug = true
	}

	// Security
	getEnvStrSlice("SECURITY_ALLOWED_ORIGINS", &cfg.Security.AllowedOrigins)
	getEnvInt("SECURITY_RATE_LIMIT_PER_SECOND", &cfg.Security.RateLimit.MaxPerSecond)
	getEnvInt("SECURITY_MESSAGE_LIMIT_PER_SECOND", &cfg.Security.MessageLimit.MaxPerSecond)
}

// --- 默认值辅助函数 ---

func setDefaultStr(target *string, defaultVal string) {
	if *target == "" {
		*target = defaultVal
	}
}

func setDefaultInt(target *int, defaultVal int) {
	if *target == 0 {
		*target = defaultVal
	}
}

func setDefaultStrSlice(target *[]string, defaultVal []string) {
	if len(*target) == 0 {
		*target = defaultVal
	}
}

// setDefaults 设置默认值
func setDefaults(cfg *Config) {
	// Server
	setDefaultStr(&cfg.Server.Host, defaultHost)
	setDefaultInt(&cfg.Server.Port, defaultPort)
	setDefaultInt(&cfg.Server.MaxConnections, defaultMaxConnections)

	// Redis
	setDefaultStr(&cfg.Redis.Addr, defaultRedisAddr)

	// Game
	setDefaultInt(&cfg.Game.TurnTimeout, defaultTurnTimeout)
	setDefaultInt(&cfg.Game.BidTimeout, defaultBidTimeout)
	setDefaultInt(&cfg.Game.RoomTimeout, defaultRoomTimeout)
	setDefaultInt(&cfg.Game.ShutdownTimeout, defaultShutdownTimeout)
	setDefaultInt(&cfg.Game.ShutdownCheckInterval, defaultShutdownCheckInterval)
	setDefaultInt(&cfg.Game.RoomCleanupDelay, defaultRoomCleanupDelay)
	setDefaultInt(&cfg.Game.OfflineWaitTimeout, defaultOfflineWaitTimeout)

	// Security
	setDefaultStrSlice(&cfg.Security.AllowedOrigins, []string{"*"})
	setDefaultInt(&cfg.Security.RateLimit.MaxPerSecond, defaultRateLimitPerSecond)
	setDefaultInt(&cfg.Security.RateLimit.MaxPerMinute, defaultRateLimitPerMinute)
	setDefaultInt(&cfg.Security.RateLimit.BanDuration, defaultBanDuration)
	setDefaultInt(&cfg.Security.MessageLimit.MaxPerSecond, defaultMessageLimitPerSecond)
	setDefaultInt(&cfg.Security.ChatLimit.MaxPerSecond, defaultChatLimitPerSecond)
	setDefaultInt(&cfg.Security.ChatLimit.MaxPerMinute, defaultChatLimitPerMinute)
	setDefaultInt(&cfg.Security.ChatLimit.Cooldown, defaultChatCooldown)

	// AI
	setDefaultStr(&cfg.AI.BaseURL, "https://api.deepseek.com")
	setDefaultStr(&cfg.AI.Model, "deepseek-v4-flash")
	setDefaultInt(&cfg.AI.BotFillTimeout, 30)
	setDefaultInt(&cfg.AI.MaxRetries, 3)
}

// Default 返回默认配置
func Default() *Config {
	// 尝试加载默认配置文件
	if cfg, err := Load("config.yaml"); err == nil {
		return cfg
	} else {
		log.Printf("无法加载默认配置文件，使用最小默认值: %v", err)
	}

	// 使用 setDefaults 设置的默认值
	cfg := &Config{}
	setDefaults(cfg)
	return cfg
}
