package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

type thresholds struct {
	MinConnectionSuccessRate float64 `json:"min_connection_success_rate"`
	MinReconnectSuccessRate  float64 `json:"min_reconnect_success_rate"`
	MinRoomSuccessRate       float64 `json:"min_room_success_rate"`
	MinMatchSuccessRate      float64 `json:"min_match_success_rate"`
	MinIdleSuccessRate       float64 `json:"min_idle_success_rate"`
	MaxP99LatencyMS          float64 `json:"max_p99_latency_ms"`
	MaxServerRSSBytes        uint64  `json:"max_server_rss_bytes"`
	MaxServerGoroutines      int     `json:"max_server_goroutines"`
	MaxFinalGoroutinesDelta  int     `json:"max_final_goroutines_delta"`
	MaxRedisErrors           int64   `json:"max_redis_errors"`
	MaxFinalConnectionsDelta int64   `json:"max_final_connections_delta"`
}

type config struct {
	URL                string
	MetricsURL         string
	Connections        int
	ConnectConcurrency int
	Duration           time.Duration
	Cooldown           time.Duration
	Reconnects         int
	RoomOperations     int
	MatchOperations    int
	MatchTimeouts      int
	MatchTimeoutWait   time.Duration
	OperationTimeout   time.Duration
	MetricsInterval    time.Duration
	ClientVersion      string
	JSONOut            string
	MarkdownOut        string
	Thresholds         thresholds
}

func parseConfig(args []string) (config, error) {
	cfg := config{}
	fs := flag.NewFlagSet("fight-landlord-load", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&cfg.URL, "url", "ws://127.0.0.1:1780/ws", "WebSocket endpoint")
	fs.StringVar(&cfg.MetricsURL, "metrics-url", "auto", "Prometheus endpoint, auto, or none")
	fs.IntVar(&cfg.Connections, "connections", 100, "number of concurrent idle connections")
	fs.IntVar(&cfg.ConnectConcurrency, "connect-concurrency", 10, "parallel connection attempts")
	fs.DurationVar(&cfg.Duration, "duration", 60*time.Second, "steady-state idle duration")
	fs.DurationVar(&cfg.Cooldown, "cooldown", 2*time.Second, "post-cleanup telemetry window")
	fs.IntVar(&cfg.Reconnects, "reconnects", 10, "clients to reconnect concurrently")
	fs.IntVar(&cfg.RoomOperations, "room-operations", 10, "create/join/leave room smoke iterations")
	fs.IntVar(&cfg.MatchOperations, "match-operations", 0, "quick-match enqueue/cancel operations, executed in concurrent pairs")
	fs.IntVar(&cfg.MatchTimeouts, "match-timeouts", 0, "server-side match queue timeouts to observe")
	fs.DurationVar(&cfg.MatchTimeoutWait, "match-timeout-wait", 35*time.Second, "maximum wait for one server-side match timeout")
	fs.DurationVar(&cfg.OperationTimeout, "operation-timeout", 10*time.Second, "timeout for one protocol operation")
	fs.DurationVar(&cfg.MetricsInterval, "metrics-interval", time.Second, "Prometheus sampling interval")
	fs.StringVar(&cfg.ClientVersion, "client-version", "ci", "client version sent during protocol negotiation")
	fs.StringVar(&cfg.JSONOut, "json-out", "artifacts/load/load-test.json", "JSON report path")
	fs.StringVar(&cfg.MarkdownOut, "markdown-out", "artifacts/load/load-test.md", "Markdown report path")

	fs.Float64Var(&cfg.Thresholds.MinConnectionSuccessRate, "min-connection-success-rate", 1, "minimum successful connection ratio")
	fs.Float64Var(&cfg.Thresholds.MinReconnectSuccessRate, "min-reconnect-success-rate", 1, "minimum successful reconnect ratio")
	fs.Float64Var(&cfg.Thresholds.MinRoomSuccessRate, "min-room-success-rate", 1, "minimum successful room scenario ratio")
	fs.Float64Var(&cfg.Thresholds.MinMatchSuccessRate, "min-match-success-rate", 1, "minimum successful match enqueue/cancel or timeout ratio")
	fs.Float64Var(&cfg.Thresholds.MinIdleSuccessRate, "min-idle-success-rate", 1, "minimum responsive connection ratio after the idle hold")
	fs.Float64Var(&cfg.Thresholds.MaxP99LatencyMS, "max-p99-ms", 0, "maximum combined p99 latency in milliseconds; 0 disables")
	fs.Uint64Var(&cfg.Thresholds.MaxServerRSSBytes, "max-server-rss-bytes", 0, "maximum observed server RSS; 0 disables")
	fs.IntVar(&cfg.Thresholds.MaxServerGoroutines, "max-server-goroutines", 0, "maximum observed server goroutines; 0 disables")
	fs.IntVar(&cfg.Thresholds.MaxFinalGoroutinesDelta, "max-final-goroutines-delta", -1, "maximum final server goroutines above baseline; -1 disables")
	fs.Int64Var(&cfg.Thresholds.MaxRedisErrors, "max-redis-errors", -1, "maximum Redis error delta; -1 disables")
	fs.Int64Var(&cfg.Thresholds.MaxFinalConnectionsDelta, "max-final-connections-delta", -1, "maximum connections remaining above baseline; -1 disables")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := cfg.validate(); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func (c *config) validate() error {
	parsed, err := normalizeWebSocketURL(c.URL)
	if err != nil {
		return err
	}
	c.URL = parsed.String()

	if err := c.normalizeMetricsURL(parsed); err != nil {
		return err
	}
	if err := c.validateWorkload(); err != nil {
		return err
	}
	return c.validateThresholds()
}

func normalizeWebSocketURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse WebSocket URL: %w", err)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return nil, errors.New("WebSocket URL must use ws or wss")
	}
	if parsed.Host == "" {
		return nil, errors.New("WebSocket URL must include a host")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/ws"
	}
	return parsed, nil
}

func (c *config) normalizeMetricsURL(webSocketURL *url.URL) error {
	switch strings.ToLower(strings.TrimSpace(c.MetricsURL)) {
	case "auto", "":
		metrics := *webSocketURL
		if metrics.Scheme == "ws" {
			metrics.Scheme = "http"
		} else {
			metrics.Scheme = "https"
		}
		metrics.Path = "/metrics"
		metrics.RawQuery = ""
		metrics.Fragment = ""
		c.MetricsURL = metrics.String()
	case "none", "off", "disabled":
		c.MetricsURL = ""
	default:
		metrics, metricsErr := url.Parse(c.MetricsURL)
		if metricsErr != nil || (metrics.Scheme != "http" && metrics.Scheme != "https") || metrics.Host == "" {
			return errors.New("metrics URL must use http or https, or be auto/none")
		}
	}
	return nil
}

func (c config) validateWorkload() error {
	if c.Connections < 1 {
		return errors.New("connections must be at least 1")
	}
	if c.ConnectConcurrency < 1 {
		return errors.New("connect-concurrency must be at least 1")
	}
	if c.Duration <= 0 {
		return errors.New("duration must be positive")
	}
	if c.Cooldown < 0 {
		return errors.New("cooldown cannot be negative")
	}
	if c.Reconnects < 0 || c.RoomOperations < 0 || c.MatchOperations < 0 || c.MatchTimeouts < 0 {
		return errors.New("reconnects, room-operations, match-operations, and match-timeouts cannot be negative")
	}
	if c.OperationTimeout <= 0 || c.MetricsInterval <= 0 || c.MatchTimeoutWait <= 0 {
		return errors.New("operation-timeout, metrics-interval, and match-timeout-wait must be positive")
	}
	if strings.TrimSpace(c.ClientVersion) == "" {
		return errors.New("client-version cannot be empty")
	}
	if strings.TrimSpace(c.JSONOut) == "" || strings.TrimSpace(c.MarkdownOut) == "" {
		return errors.New("both report paths are required")
	}
	return nil
}

func (c config) validateThresholds() error {
	for name, value := range map[string]float64{
		"min-connection-success-rate": c.Thresholds.MinConnectionSuccessRate,
		"min-reconnect-success-rate":  c.Thresholds.MinReconnectSuccessRate,
		"min-room-success-rate":       c.Thresholds.MinRoomSuccessRate,
		"min-match-success-rate":      c.Thresholds.MinMatchSuccessRate,
		"min-idle-success-rate":       c.Thresholds.MinIdleSuccessRate,
	} {
		if value < 0 || value > 1 {
			return fmt.Errorf("%s must be between 0 and 1", name)
		}
	}
	if c.Thresholds.MaxP99LatencyMS < 0 || c.Thresholds.MaxServerGoroutines < 0 || c.Thresholds.MaxFinalGoroutinesDelta < -1 || c.Thresholds.MaxRedisErrors < -1 || c.Thresholds.MaxFinalConnectionsDelta < -1 {
		return errors.New("maximum thresholds cannot be below their disabled value")
	}
	return nil
}
