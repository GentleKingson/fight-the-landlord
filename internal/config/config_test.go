package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_ValidConfig(t *testing.T) {
	t.Parallel()

	// Create a temp config file
	content := `
server:
  host: "127.0.0.1"
  port: 8080
  max_connections: 5000

redis:
  addr: "redis:6379"
  password: "secret"
  db: 1

game:
  turn_timeout: 60
  bid_timeout: 30
  room_timeout: 15
  shutdown_timeout: 60
  shutdown_check_interval: 30
  room_cleanup_delay: 60

security:
  allowed_origins:
    - "http://localhost:3000"
    - "https://example.com"
  rate_limit:
    max_per_second: 20
    max_per_minute: 120
    ban_duration: 120
  message_limit:
    max_per_second: 50
  chat_limit:
    max_per_second: 2
    max_per_minute: 60
    cooldown: 10
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(content), 0o600)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify loaded values
	assert.Equal(t, "127.0.0.1", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, 5000, cfg.Server.MaxConnections)
	assert.Equal(t, "redis:6379", cfg.Redis.Addr)
	assert.Equal(t, "secret", cfg.Redis.Password)
	assert.Equal(t, 1, cfg.Redis.DB)
	assert.Equal(t, 60, cfg.Game.TurnTimeout)
	assert.Equal(t, 30, cfg.Game.BidTimeout)
	assert.Len(t, cfg.Security.AllowedOrigins, 2)
}

func TestLoad_FileNotFound(t *testing.T) {
	t.Parallel()

	cfg, err := Load("/nonexistent/path/config.yaml")
	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestLoad_InvalidYAML(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")
	err := os.WriteFile(configPath, []byte("invalid: yaml: :::"), 0o600)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestLoad_AppliesDefaults(t *testing.T) {
	t.Parallel()

	// Empty config file - defaults should be applied
	content := `{}`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "empty.yaml")
	err := os.WriteFile(configPath, []byte(content), 0o600)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify defaults are applied
	assert.Equal(t, defaultHost, cfg.Server.Host)
	assert.Equal(t, defaultPort, cfg.Server.Port)
	assert.Equal(t, defaultMaxConnections, cfg.Server.MaxConnections)
	assert.Equal(t, defaultRedisAddr, cfg.Redis.Addr)
	assert.Equal(t, defaultTurnTimeout, cfg.Game.TurnTimeout)
	assert.Equal(t, defaultBidTimeout, cfg.Game.BidTimeout)
	assert.Equal(t, []string{"*"}, cfg.Security.AllowedOrigins)
	assert.True(t, cfg.Observability.MetricsEnabled)
	assert.Equal(t, defaultMetricsPath, cfg.Observability.MetricsPath)
	assert.Equal(t, defaultLogFormat, cfg.Observability.LogFormat)
}

func TestLoad_PreservesExplicitUnlimitedMaxConnections(t *testing.T) {
	previous, present := os.LookupEnv("SERVER_MAX_CONNECTIONS")
	require.NoError(t, os.Unsetenv("SERVER_MAX_CONNECTIONS"))
	t.Cleanup(func() {
		if present {
			require.NoError(t, os.Setenv("SERVER_MAX_CONNECTIONS", previous))
			return
		}
		require.NoError(t, os.Unsetenv("SERVER_MAX_CONNECTIONS"))
	})

	configPath := filepath.Join(t.TempDir(), "unlimited.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
server:
  max_connections: 0
`), 0o600))

	cfg, err := Load(configPath)
	require.NoError(t, err)
	assert.Zero(t, cfg.Server.MaxConnections)
}

func TestLoad_EnvironmentCanDisableMaxConnections(t *testing.T) {
	t.Setenv("SERVER_MAX_CONNECTIONS", "0")

	configPath := filepath.Join(t.TempDir(), "default-limit.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`{}`), 0o600))

	cfg, err := Load(configPath)
	require.NoError(t, err)
	assert.Zero(t, cfg.Server.MaxConnections)
}

func TestConfigValidateRejectsUnsafeRuntimeValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
		field  string
	}{
		{name: "unknown environment", mutate: func(cfg *Config) { cfg.Server.Environment = "staging" }, field: "server.environment"},
		{name: "zero port", mutate: func(cfg *Config) { cfg.Server.Port = 0 }, field: "server.port"},
		{name: "large port", mutate: func(cfg *Config) { cfg.Server.Port = 65536 }, field: "server.port"},
		{name: "turn timeout", mutate: func(cfg *Config) { cfg.Game.TurnTimeout = 0 }, field: "game.turn_timeout"},
		{name: "turn timeout overflow", mutate: func(cfg *Config) {
			cfg.Game.TurnTimeout = overflowDurationValue(time.Second)
		}, field: "game.turn_timeout"},
		{name: "bid timeout", mutate: func(cfg *Config) { cfg.Game.BidTimeout = 0 }, field: "game.bid_timeout"},
		{name: "room timeout", mutate: func(cfg *Config) { cfg.Game.RoomTimeout = 0 }, field: "game.room_timeout"},
		{name: "room timeout overflow", mutate: func(cfg *Config) {
			cfg.Game.RoomTimeout = overflowDurationValue(time.Minute)
		}, field: "game.room_timeout"},
		{name: "shutdown timeout", mutate: func(cfg *Config) { cfg.Game.ShutdownTimeout = 0 }, field: "game.shutdown_timeout"},
		{name: "shutdown interval", mutate: func(cfg *Config) { cfg.Game.ShutdownCheckInterval = 0 }, field: "game.shutdown_check_interval"},
		{name: "cleanup delay", mutate: func(cfg *Config) { cfg.Game.RoomCleanupDelay = -1 }, field: "game.room_cleanup_delay"},
		{name: "offline timeout", mutate: func(cfg *Config) { cfg.Game.OfflineWaitTimeout = 0 }, field: "game.offline_wait_timeout"},
		{name: "bot fill timeout", mutate: func(cfg *Config) { cfg.BOT.BotFillTimeout = 0 }, field: "bot.bot_fill_timeout"},
		{name: "redis address", mutate: func(cfg *Config) { cfg.Redis.Addr = "redis" }, field: "redis.addr"},
		{name: "redis db", mutate: func(cfg *Config) { cfg.Redis.DB = -1 }, field: "redis.db"},
		{name: "douzero url", mutate: func(cfg *Config) { cfg.BOT.DouZeroURL = "file:///tmp/model" }, field: "bot.douzero_url"},
		{name: "minimum client semver", mutate: func(cfg *Config) { cfg.Server.MinClientVersion = "1.2" }, field: "server.min_client_version"},
		{name: "minimum client numeric prerelease", mutate: func(cfg *Config) { cfg.Server.MinClientVersion = "v1.2.3-01" }, field: "server.min_client_version"},
		{name: "empty origins", mutate: func(cfg *Config) { cfg.Security.AllowedOrigins = nil }, field: "security.allowed_origins"},
		{name: "origin with path", mutate: func(cfg *Config) { cfg.Security.AllowedOrigins = []string{"https://game.example/path"} }, field: "security.allowed_origins"},
		{name: "proxy cidr", mutate: func(cfg *Config) { cfg.Security.TrustedProxyCIDRs = []string{"10.0.0.1"} }, field: "security.trusted_proxy_cidrs"},
		{name: "rate per second", mutate: func(cfg *Config) { cfg.Security.RateLimit.MaxPerSecond = 0 }, field: "security.rate_limit.max_per_second"},
		{name: "rate per minute", mutate: func(cfg *Config) { cfg.Security.RateLimit.MaxPerMinute = 0 }, field: "security.rate_limit.max_per_minute"},
		{name: "ban duration", mutate: func(cfg *Config) { cfg.Security.RateLimit.BanDuration = 0 }, field: "security.rate_limit.ban_duration"},
		{name: "ban duration overflow", mutate: func(cfg *Config) {
			cfg.Security.RateLimit.BanDuration = overflowDurationValue(time.Second)
		}, field: "security.rate_limit.ban_duration"},
		{name: "message rate", mutate: func(cfg *Config) { cfg.Security.MessageLimit.MaxPerSecond = 0 }, field: "security.message_limit.max_per_second"},
		{name: "chat per second", mutate: func(cfg *Config) { cfg.Security.ChatLimit.MaxPerSecond = 0 }, field: "security.chat_limit.max_per_second"},
		{name: "chat per minute", mutate: func(cfg *Config) { cfg.Security.ChatLimit.MaxPerMinute = 0 }, field: "security.chat_limit.max_per_minute"},
		{name: "chat cooldown", mutate: func(cfg *Config) { cfg.Security.ChatLimit.Cooldown = -1 }, field: "security.chat_limit.cooldown"},
		{name: "chat cooldown overflow", mutate: func(cfg *Config) {
			cfg.Security.ChatLimit.Cooldown = overflowDurationValue(time.Second)
		}, field: "security.chat_limit.cooldown"},
		{name: "relative metrics path", mutate: func(cfg *Config) { cfg.Observability.MetricsPath = "metrics" }, field: "observability.metrics_path"},
		{name: "unclean metrics path", mutate: func(cfg *Config) { cfg.Observability.MetricsPath = "/ops/../metrics" }, field: "observability.metrics_path"},
		{name: "reserved metrics path", mutate: func(cfg *Config) { cfg.Observability.MetricsPath = "/readyz" }, field: "observability.metrics_path"},
		{name: "session commit metrics path", mutate: func(cfg *Config) { cfg.Observability.MetricsPath = "/session/commit" }, field: "observability.metrics_path"},
		{name: "session refresh metrics path", mutate: func(cfg *Config) { cfg.Observability.MetricsPath = "/session/refresh" }, field: "observability.metrics_path"},
		{name: "invalid log format", mutate: func(cfg *Config) { cfg.Observability.LogFormat = "yaml" }, field: "observability.log_format"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			cfg := validTestConfig()
			testCase.mutate(cfg)
			require.ErrorContains(t, cfg.Validate(), testCase.field)
		})
	}
}

func TestConfigValidateProductionSecurityPolicy(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Server.Environment = "production"
	cfg.Security.AllowedOrigins = []string{"https://game.example"}
	cfg.Redis.Password = ""
	require.ErrorContains(t, cfg.Validate(), "redis.password")

	cfg.Redis.Password = "secret"
	cfg.Security.AllowedOrigins = []string{"*"}
	require.ErrorContains(t, cfg.Validate(), "security.allowed_origins")

	cfg.Security.AllowedOrigins = []string{"https://game.example"}
	require.NoError(t, cfg.Validate())
}

func TestConfigValidateAllowsUnlimitedConnectionsAndRuntimeZeros(t *testing.T) {
	t.Parallel()

	cfg := validTestConfig()
	cfg.Server.MaxConnections = -1
	cfg.Game.RoomCleanupDelay = 0
	cfg.Security.ChatLimit.Cooldown = 0
	require.NoError(t, cfg.Validate())

	cfg.Server.MaxConnections = 0
	require.NoError(t, cfg.Validate())

	cfg.Server.MinClientVersion = "v1.2.3-rc.10+server.7"
	require.NoError(t, cfg.Validate())
}

func TestLoadRejectsInvalidEnvironmentOverrides(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{key: "SERVER_PORT", value: "not-a-number"},
		{key: "BOT_ENABLED", value: "sometimes"},
		{key: "SECURITY_ALLOWED_ORIGINS", value: "https://one.example,,https://two.example"},
		{key: "SECURITY_TRUSTED_PROXY_CIDRS", value: "not-a-cidr"},
	}

	for index, testCase := range tests {
		t.Run(testCase.key, func(t *testing.T) {
			t.Setenv(testCase.key, testCase.value)
			configPath := filepath.Join(t.TempDir(), fmt.Sprintf("invalid-env-%d.yaml", index))
			require.NoError(t, os.WriteFile(configPath, []byte(`{}`), 0o600))

			_, err := Load(configPath)
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), testCase.key) || strings.Contains(err.Error(), "trusted_proxy_cidrs"), err)
		})
	}
}

func TestLoadPreservesExplicitInvalidTimeoutForValidation(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "zero-timeout.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
game:
  turn_timeout: 0
`), 0o600))

	_, err := Load(configPath)
	require.ErrorContains(t, err, "game.turn_timeout")
}

func TestDefault(t *testing.T) {
	// Note: Not parallel because Default() reads from filesystem

	cfg := Default()
	require.NotNil(t, cfg)

	// Verify defaults are set
	assert.Equal(t, defaultHost, cfg.Server.Host)
	assert.Equal(t, defaultPort, cfg.Server.Port)
	assert.Equal(t, defaultTurnTimeout, cfg.Game.TurnTimeout)
	assert.True(t, cfg.Observability.MetricsEnabled)
	assert.Equal(t, "/metrics", cfg.Observability.MetricsPath)
	assert.Equal(t, "json", cfg.Observability.LogFormat)
}

func TestShippedConfigUsesJSONLogs(t *testing.T) {
	t.Parallel()

	cfg, err := Load(filepath.Join("..", "..", "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "json", cfg.Observability.LogFormat)
}

func TestGameConfig_DurationMethods(t *testing.T) {
	t.Parallel()

	cfg := &GameConfig{
		TurnTimeout:           30,
		BidTimeout:            15,
		RoomTimeout:           10,
		ShutdownTimeout:       60,
		ShutdownCheckInterval: 5,
		RoomCleanupDelay:      20,
		OfflineWaitTimeout:    8,
	}

	assert.Equal(t, 30*time.Second, cfg.TurnTimeoutDuration())
	assert.Equal(t, 15*time.Second, cfg.BidTimeoutDuration())
	assert.Equal(t, 10*time.Minute, cfg.RoomTimeoutDuration())
	assert.Equal(t, 60*time.Minute, cfg.ShutdownTimeoutDuration())
	assert.Equal(t, 5*time.Second, cfg.ShutdownCheckIntervalDuration())
	assert.Equal(t, 20*time.Second, cfg.RoomCleanupDelayDuration())
	assert.Equal(t, 8*time.Second, cfg.OfflineWaitTimeoutDuration())
}

func TestRateLimitConfig_BanDurationTime(t *testing.T) {
	t.Parallel()

	cfg := &RateLimitConfig{BanDuration: 120}
	assert.Equal(t, 120*time.Second, cfg.BanDurationTime())
}

func TestChatLimitConfig_CooldownDuration(t *testing.T) {
	t.Parallel()

	cfg := &ChatLimitConfig{Cooldown: 10}
	assert.Equal(t, 10*time.Second, cfg.CooldownDuration())
}

func TestLoadFromEnv(t *testing.T) {
	// Not parallel because it modifies environment variables

	// Set environment variables
	t.Setenv("SERVER_HOST", "env-host")
	t.Setenv("SERVER_PORT", "9999")
	t.Setenv("SERVER_MIN_CLIENT_VERSION", "")
	t.Setenv("REDIS_ADDR", "env-redis:6380")
	t.Setenv("GAME_TURN_TIMEOUT", "120")
	t.Setenv("GAME_OFFLINE_WAIT_TIMEOUT", "7")
	t.Setenv("SECURITY_ALLOWED_ORIGINS", " http://a.com, http://b.com ")
	t.Setenv("SECURITY_RATE_LIMIT_PER_SECOND", "11")
	t.Setenv("SECURITY_RATE_LIMIT_PER_MINUTE", "61")
	t.Setenv("SECURITY_RATE_LIMIT_BAN_DURATION", "62")
	t.Setenv("SECURITY_MESSAGE_LIMIT_PER_SECOND", "21")
	t.Setenv("SECURITY_CHAT_LIMIT_PER_SECOND", "2")
	t.Setenv("SECURITY_CHAT_LIMIT_PER_MINUTE", "31")
	t.Setenv("SECURITY_CHAT_COOLDOWN", "6")
	t.Setenv("BOT_ENABLED", "false")
	t.Setenv("DOUZERO_ENABLED", "false")
	t.Setenv("OBSERVABILITY_METRICS_ENABLED", "false")
	t.Setenv("OBSERVABILITY_METRICS_PATH", "/internal/metrics")
	t.Setenv("OBSERVABILITY_LOG_FORMAT", "text")

	// Create minimal config file
	content := `
server:
  min_client_version: "v9.9.9"
bot:
  enabled: true
  douzero_enabled: true
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "env.yaml")
	err := os.WriteFile(configPath, []byte(content), 0o600)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify env vars override defaults
	assert.Equal(t, "env-host", cfg.Server.Host)
	assert.Equal(t, 9999, cfg.Server.Port)
	assert.Empty(t, cfg.Server.MinClientVersion)
	assert.Equal(t, "env-redis:6380", cfg.Redis.Addr)
	assert.Equal(t, 120, cfg.Game.TurnTimeout)
	assert.Equal(t, 7, cfg.Game.OfflineWaitTimeout)
	assert.Equal(t, []string{"http://a.com", "http://b.com"}, cfg.Security.AllowedOrigins)
	assert.Equal(t, 11, cfg.Security.RateLimit.MaxPerSecond)
	assert.Equal(t, 61, cfg.Security.RateLimit.MaxPerMinute)
	assert.Equal(t, 62, cfg.Security.RateLimit.BanDuration)
	assert.Equal(t, 21, cfg.Security.MessageLimit.MaxPerSecond)
	assert.Equal(t, 2, cfg.Security.ChatLimit.MaxPerSecond)
	assert.Equal(t, 31, cfg.Security.ChatLimit.MaxPerMinute)
	assert.Equal(t, 6, cfg.Security.ChatLimit.Cooldown)
	assert.False(t, cfg.BOT.Enabled)
	assert.False(t, cfg.BOT.DouZeroEnabled)
	assert.False(t, cfg.Observability.MetricsEnabled)
	assert.Equal(t, "/internal/metrics", cfg.Observability.MetricsPath)
	assert.Equal(t, "text", cfg.Observability.LogFormat)
}

func validTestConfig() *Config {
	cfg := Default()
	cfg.Server.Environment = "development"
	cfg.Server.MinClientVersion = "v1.2.3"
	cfg.Security.AllowedOrigins = []string{"https://game.example"}
	cfg.Security.TrustedProxyCIDRs = []string{"10.0.0.0/8", "fd00::/8"}
	return cfg
}

func overflowDurationValue(unit time.Duration) int {
	maximum := int64(math.MaxInt64 / unit)
	if int64(math.MaxInt) <= maximum {
		// The platform int cannot express an overflowing positive value.
		return -1
	}
	return int(maximum + 1)
}
