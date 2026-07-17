package config

import (
	"fmt"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// 默认配置值
const (
	defaultHost                  = "127.0.0.1"
	defaultEnvironment           = "development"
	defaultPort                  = 1780
	defaultMaxConnections        = 100
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
	defaultBotFillTimeout        = 30
	defaultDouZeroURL            = "http://localhost:2021"
	defaultMetricsPath           = "/metrics"
	defaultLogFormat             = "json"
)

var semanticVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// Config 服务端配置
type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Redis         RedisConfig         `yaml:"redis"`
	Game          GameConfig          `yaml:"game"`
	Security      SecurityConfig      `yaml:"security"`
	BOT           BotConfig           `yaml:"bot"`
	Observability ObservabilityConfig `yaml:"observability"`
	Admin         AdminConfig         `yaml:"-"`
}

// AdminConfig is environment-only so the management credential cannot be
// checked into config.yaml by accident. An empty key disables admin access in
// development and test; production requires an explicit strong value.
type AdminConfig struct {
	Key string `yaml:"-"`
}

// ObservabilityConfig controls the public Prometheus endpoint and server log
// encoding. Metric labels are fixed by the application and are not configurable.
type ObservabilityConfig struct {
	MetricsEnabled bool   `yaml:"metrics_enabled"`
	MetricsPath    string `yaml:"metrics_path"`
	LogFormat      string `yaml:"log_format"`
}

// BotConfig 机器人配置
type BotConfig struct {
	Enabled        bool `yaml:"enabled"`
	BotFillTimeout int  `yaml:"bot_fill_timeout"` // 等待玩家加入的超时秒数

	// DouZero 引擎配置；未启用时使用内置规则启发式机器人
	DouZeroEnabled bool   `yaml:"douzero_enabled"` // 使用 DouZero 神经网络引擎
	DouZeroURL     string `yaml:"douzero_url"`     // Python 服务地址
}

// ServerConfig WebSocket 服务器配置
type ServerConfig struct {
	Environment    string `yaml:"environment"`
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	MaxConnections int    `yaml:"max_connections"` // 最大并发连接数，<= 0 表示无限制
	// 要求的最低客户端版本（如 v1.2.0），空表示不限制。
	// 低于该版本的客户端启动时会被强制自动升级，用于发布不兼容变更时保证版本一致。
	MinClientVersion string `yaml:"min_client_version"`
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
	AllowedOrigins    []string           `yaml:"allowed_origins"`     // 允许的来源
	TrustedProxyCIDRs []string           `yaml:"trusted_proxy_cidrs"` // 允许提供转发 IP 头的代理网段
	RateLimit         RateLimitConfig    `yaml:"rate_limit"`          // 连接速率限制
	MessageLimit      MessageLimitConfig `yaml:"message_limit"`       // 消息速率限制
	ChatLimit         ChatLimitConfig    `yaml:"chat_limit"`          // 聊天消息速率限制
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

	// Decode over a complete default value so omitted fields inherit defaults
	// while explicit zero values retain their domain meaning.
	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	// 本地开发便利：自动加载 .env.local（仅本地，已 gitignore）。
	// .env 是 Docker 专用（含 REDIS_ADDR=redis:6379 等容器内地址），
	if err := godotenv.Load(".env.local"); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load .env.local: %w", err)
	}
	if err := loadFromEnv(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// --- 环境变量辅助函数 ---

func getEnvStr(key string, target *string, allowEmpty bool) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	if !allowEmpty && strings.TrimSpace(v) == "" {
		return fmt.Errorf("%s must not be empty", key)
	}
	*target = v
	return nil
}

func getEnvInt(key string, target *int) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("%s must be an integer: %w", key, err)
	}
	*target = n
	return nil
}

func getEnvStrSlice(key string, target *[]string, allowEmpty bool) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	if strings.TrimSpace(v) == "" {
		if !allowEmpty {
			return fmt.Errorf("%s must not be empty", key)
		}
		*target = nil
		return nil
	}

	parts := strings.Split(v, ",")
	values := make([]string, len(parts))
	for i, value := range parts {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s contains an empty item", key)
		}
		values[i] = value
	}
	*target = values
	return nil
}

func getEnvBool(key string, target *bool) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	*target = parsed
	return nil
}

// loadFromEnv 从环境变量加载配置（覆盖文件配置）
func loadFromEnv(cfg *Config) error {
	loaders := []func() error{
		func() error { return getEnvStr("SERVER_ENV", &cfg.Server.Environment, false) },
		func() error { return getEnvStr("SERVER_HOST", &cfg.Server.Host, false) },
		func() error { return getEnvInt("SERVER_PORT", &cfg.Server.Port) },
		func() error { return getEnvInt("SERVER_MAX_CONNECTIONS", &cfg.Server.MaxConnections) },
		func() error { return getEnvStr("SERVER_MIN_CLIENT_VERSION", &cfg.Server.MinClientVersion, true) },
		func() error { return getEnvStr("REDIS_ADDR", &cfg.Redis.Addr, false) },
		func() error { return getEnvStr("REDIS_PASSWORD", &cfg.Redis.Password, true) },
		func() error { return getEnvInt("REDIS_DB", &cfg.Redis.DB) },
		func() error { return getEnvInt("GAME_TURN_TIMEOUT", &cfg.Game.TurnTimeout) },
		func() error { return getEnvInt("GAME_BID_TIMEOUT", &cfg.Game.BidTimeout) },
		func() error { return getEnvInt("GAME_ROOM_TIMEOUT", &cfg.Game.RoomTimeout) },
		func() error { return getEnvInt("GAME_SHUTDOWN_TIMEOUT", &cfg.Game.ShutdownTimeout) },
		func() error { return getEnvInt("GAME_SHUTDOWN_CHECK_INTERVAL", &cfg.Game.ShutdownCheckInterval) },
		func() error { return getEnvInt("GAME_ROOM_CLEANUP_DELAY", &cfg.Game.RoomCleanupDelay) },
		func() error { return getEnvInt("GAME_OFFLINE_WAIT_TIMEOUT", &cfg.Game.OfflineWaitTimeout) },
		func() error { return getEnvBool("BOT_ENABLED", &cfg.BOT.Enabled) },
		func() error { return getEnvInt("BOT_FILL_TIMEOUT", &cfg.BOT.BotFillTimeout) },
		func() error { return getEnvBool("DOUZERO_ENABLED", &cfg.BOT.DouZeroEnabled) },
		func() error { return getEnvStr("DOUZERO_URL", &cfg.BOT.DouZeroURL, false) },
		func() error { return getEnvBool("OBSERVABILITY_METRICS_ENABLED", &cfg.Observability.MetricsEnabled) },
		func() error { return getEnvStr("OBSERVABILITY_METRICS_PATH", &cfg.Observability.MetricsPath, false) },
		func() error { return getEnvStr("OBSERVABILITY_LOG_FORMAT", &cfg.Observability.LogFormat, false) },
		func() error { return getEnvStr("ADMIN_KEY", &cfg.Admin.Key, true) },
		func() error { return getEnvStrSlice("SECURITY_ALLOWED_ORIGINS", &cfg.Security.AllowedOrigins, false) },
		func() error {
			return getEnvStrSlice("SECURITY_TRUSTED_PROXY_CIDRS", &cfg.Security.TrustedProxyCIDRs, true)
		},
		func() error { return getEnvInt("SECURITY_RATE_LIMIT_PER_SECOND", &cfg.Security.RateLimit.MaxPerSecond) },
		func() error { return getEnvInt("SECURITY_RATE_LIMIT_PER_MINUTE", &cfg.Security.RateLimit.MaxPerMinute) },
		func() error {
			return getEnvInt("SECURITY_RATE_LIMIT_BAN_DURATION", &cfg.Security.RateLimit.BanDuration)
		},
		func() error {
			return getEnvInt("SECURITY_MESSAGE_LIMIT_PER_SECOND", &cfg.Security.MessageLimit.MaxPerSecond)
		},
		func() error { return getEnvInt("SECURITY_CHAT_LIMIT_PER_SECOND", &cfg.Security.ChatLimit.MaxPerSecond) },
		func() error { return getEnvInt("SECURITY_CHAT_LIMIT_PER_MINUTE", &cfg.Security.ChatLimit.MaxPerMinute) },
		func() error { return getEnvInt("SECURITY_CHAT_COOLDOWN", &cfg.Security.ChatLimit.Cooldown) },
	}
	for _, load := range loaders {
		if err := load(); err != nil {
			return err
		}
	}
	return nil
}

// --- 默认值辅助函数 ---

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Environment:    defaultEnvironment,
			Host:           defaultHost,
			Port:           defaultPort,
			MaxConnections: defaultMaxConnections,
		},
		Redis: RedisConfig{Addr: defaultRedisAddr},
		Game: GameConfig{
			TurnTimeout:           defaultTurnTimeout,
			BidTimeout:            defaultBidTimeout,
			RoomTimeout:           defaultRoomTimeout,
			ShutdownTimeout:       defaultShutdownTimeout,
			ShutdownCheckInterval: defaultShutdownCheckInterval,
			RoomCleanupDelay:      defaultRoomCleanupDelay,
			OfflineWaitTimeout:    defaultOfflineWaitTimeout,
		},
		Security: SecurityConfig{
			AllowedOrigins: []string{"*"},
			RateLimit: RateLimitConfig{
				MaxPerSecond: defaultRateLimitPerSecond,
				MaxPerMinute: defaultRateLimitPerMinute,
				BanDuration:  defaultBanDuration,
			},
			MessageLimit: MessageLimitConfig{MaxPerSecond: defaultMessageLimitPerSecond},
			ChatLimit: ChatLimitConfig{
				MaxPerSecond: defaultChatLimitPerSecond,
				MaxPerMinute: defaultChatLimitPerMinute,
				Cooldown:     defaultChatCooldown,
			},
		},
		BOT: BotConfig{
			BotFillTimeout: defaultBotFillTimeout,
			DouZeroURL:     defaultDouZeroURL,
		},
		Observability: ObservabilityConfig{
			MetricsEnabled: true,
			MetricsPath:    defaultMetricsPath,
			LogFormat:      defaultLogFormat,
		},
	}
}

// Default 返回默认配置
func Default() *Config {
	return defaultConfig()
}

// Validate rejects configuration values that cannot be used safely at runtime.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config must not be nil")
	}
	environment, err := validateServerConfig(c.Server)
	if err != nil {
		return err
	}
	if err := validateConfiguredDurations(c); err != nil {
		return err
	}
	if err := validateDependencies(c, environment); err != nil {
		return err
	}
	if err := validateSecurityConfig(c.Security, environment); err != nil {
		return err
	}
	if err := validateObservabilityConfig(c.Observability); err != nil {
		return err
	}
	if err := validateAdminConfig(c.Admin, environment); err != nil {
		return err
	}
	return validatePositiveLimits(c.Security)
}

func validateAdminConfig(admin AdminConfig, environment string) error {
	key := admin.Key
	if key == "" {
		if environment == "production" {
			return fmt.Errorf("ADMIN_KEY must not be empty in production")
		}
		return nil
	}
	if key != strings.TrimSpace(key) || strings.ContainsAny(key, "\x00\r\n") {
		return fmt.Errorf("ADMIN_KEY must not contain surrounding whitespace or control characters")
	}
	if len(key) < 32 {
		return fmt.Errorf("ADMIN_KEY must be at least 32 bytes")
	}
	if len(key) > 1024 {
		return fmt.Errorf("ADMIN_KEY must not exceed 1024 bytes")
	}
	return nil
}

func validateObservabilityConfig(observability ObservabilityConfig) error {
	metricsPath := observability.MetricsPath
	if metricsPath == "" || metricsPath != strings.TrimSpace(metricsPath) || !strings.HasPrefix(metricsPath, "/") ||
		path.Clean(metricsPath) != metricsPath || strings.HasSuffix(metricsPath, "/") {
		return fmt.Errorf("observability.metrics_path must be a clean absolute HTTP path")
	}
	switch metricsPath {
	case "/", "/ws", "/health", "/livez", "/readyz", "/version", "/session/commit", "/session/refresh", "/session/revoke",
		"/admin/status", "/admin/drain", "/admin/maintenance", "/admin/resume", "/admin/disconnect", "/admin/mute",
		"/admin/unmute", "/admin/ban", "/admin/unban":
		return fmt.Errorf("observability.metrics_path conflicts with reserved route %q", metricsPath)
	}
	if strings.ContainsAny(metricsPath, "?#") {
		return fmt.Errorf("observability.metrics_path must not contain a query or fragment")
	}
	if format := strings.ToLower(strings.TrimSpace(observability.LogFormat)); format != "json" && format != "text" {
		return fmt.Errorf("observability.log_format must be json or text")
	}
	return nil
}

func validateServerConfig(server ServerConfig) (string, error) {
	environment := strings.ToLower(strings.TrimSpace(server.Environment))
	if environment != "development" && environment != "production" && environment != "test" {
		return "", fmt.Errorf("server.environment must be development, production, or test")
	}
	if strings.TrimSpace(server.Host) == "" || server.Host != strings.TrimSpace(server.Host) {
		return "", fmt.Errorf("server.host must not be empty")
	}
	if server.Port < 1 || server.Port > 65535 {
		return "", fmt.Errorf("server.port must be between 1 and 65535")
	}
	// MaxConnections <= 0 intentionally means unlimited.
	return environment, nil
}

func validateConfiguredDurations(c *Config) error {
	durations := []struct {
		name  string
		value int
		unit  time.Duration
		zero  bool
	}{
		{name: "game.turn_timeout", value: c.Game.TurnTimeout, unit: time.Second},
		{name: "game.bid_timeout", value: c.Game.BidTimeout, unit: time.Second},
		{name: "game.room_timeout", value: c.Game.RoomTimeout, unit: time.Minute},
		{name: "game.shutdown_timeout", value: c.Game.ShutdownTimeout, unit: time.Minute},
		{name: "game.shutdown_check_interval", value: c.Game.ShutdownCheckInterval, unit: time.Second},
		{name: "game.room_cleanup_delay", value: c.Game.RoomCleanupDelay, unit: time.Second, zero: true},
		{name: "game.offline_wait_timeout", value: c.Game.OfflineWaitTimeout, unit: time.Second},
		{name: "bot.bot_fill_timeout", value: c.BOT.BotFillTimeout, unit: time.Second},
		{name: "security.rate_limit.ban_duration", value: c.Security.RateLimit.BanDuration, unit: time.Second},
		{name: "security.chat_limit.cooldown", value: c.Security.ChatLimit.Cooldown, unit: time.Second, zero: true},
	}
	for _, duration := range durations {
		if err := validateDuration(duration.name, duration.value, duration.unit, duration.zero); err != nil {
			return err
		}
	}
	return nil
}

func validateDependencies(c *Config, environment string) error {
	if err := validateRedisAddress(c.Redis.Addr); err != nil {
		return fmt.Errorf("redis.addr: %w", err)
	}
	if c.Redis.DB < 0 {
		return fmt.Errorf("redis.db must not be negative")
	}
	if environment == "production" && strings.TrimSpace(c.Redis.Password) == "" {
		return fmt.Errorf("redis.password must not be empty in production")
	}
	if err := validateHTTPURL(c.BOT.DouZeroURL); err != nil {
		return fmt.Errorf("bot.douzero_url: %w", err)
	}
	if c.Server.MinClientVersion != "" && !isSemanticVersion(c.Server.MinClientVersion) {
		return fmt.Errorf("server.min_client_version must be a semantic version")
	}
	return nil
}

func validateSecurityConfig(security SecurityConfig, environment string) error {
	if len(security.AllowedOrigins) == 0 {
		return fmt.Errorf("security.allowed_origins must not be empty")
	}
	for _, origin := range security.AllowedOrigins {
		if origin == "*" {
			if environment == "production" {
				return fmt.Errorf("security.allowed_origins must not contain wildcard in production")
			}
			continue
		}
		if err := validateOrigin(origin); err != nil {
			return fmt.Errorf("security.allowed_origins: %w", err)
		}
	}
	for _, cidr := range security.TrustedProxyCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("security.trusted_proxy_cidrs contains invalid CIDR %q: %w", cidr, err)
		}
	}
	return nil
}

func validatePositiveLimits(security SecurityConfig) error {
	positiveLimits := []struct {
		name  string
		value int
	}{
		{name: "security.rate_limit.max_per_second", value: security.RateLimit.MaxPerSecond},
		{name: "security.rate_limit.max_per_minute", value: security.RateLimit.MaxPerMinute},
		{name: "security.message_limit.max_per_second", value: security.MessageLimit.MaxPerSecond},
		{name: "security.chat_limit.max_per_second", value: security.ChatLimit.MaxPerSecond},
		{name: "security.chat_limit.max_per_minute", value: security.ChatLimit.MaxPerMinute},
	}
	for _, limit := range positiveLimits {
		if limit.value <= 0 {
			return fmt.Errorf("%s must be greater than zero", limit.name)
		}
	}
	return nil
}

func validateDuration(name string, value int, unit time.Duration, allowZero bool) error {
	if value < 0 || (!allowZero && value == 0) {
		if allowZero {
			return fmt.Errorf("%s must not be negative", name)
		}
		return fmt.Errorf("%s must be greater than zero", name)
	}
	if int64(value) > math.MaxInt64/int64(unit) {
		return fmt.Errorf("%s exceeds the maximum supported duration", name)
	}
	return nil
}

func isSemanticVersion(value string) bool {
	if !semanticVersionPattern.MatchString(value) {
		return false
	}
	if !strings.HasPrefix(value, "v") {
		value = "v" + value
	}
	return semver.IsValid(value)
}

func validateRedisAddress(address string) error {
	if address != strings.TrimSpace(address) {
		return fmt.Errorf("must not contain surrounding whitespace")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("must be a host:port address: %w", err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("host must not be empty")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

func validateHTTPURL(value string) error {
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("must not contain surrounding whitespace")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	if (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) || parsed.Hostname() == "" {
		return fmt.Errorf("must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must not contain credentials, a query, or a fragment")
	}
	return nil
}

func validateOrigin(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid origin %q: %w", value, err)
	}
	if (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) || parsed.Hostname() == "" {
		return fmt.Errorf("invalid origin %q", value)
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("origin %q must contain only scheme and authority", value)
	}
	return nil
}
