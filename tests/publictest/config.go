package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	presetSmoke      = "smoke"
	presetPublicTest = "public-test"
)

var presetDurations = map[string]time.Duration{
	presetSmoke:      10 * time.Minute,
	presetPublicTest: time.Hour,
}

type config struct {
	Preset           string
	URL              string
	MetricsURL       string
	Duration         time.Duration
	Players          int
	DisconnectRate   float64
	DouZero          bool
	OperationTimeout time.Duration
	Cooldown         time.Duration
	MetricsInterval  time.Duration
	ClientVersion    string
	JSONOut          string
	MarkdownOut      string
	Seed             int64
}

type explicitBool struct {
	value *bool
}

func (b explicitBool) String() string {
	if b.value == nil {
		return "false"
	}
	return strconv.FormatBool(*b.value)
}

func (b explicitBool) Set(raw string) error {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("must be true or false")
	}
	*b.value = value
	return nil
}

func parseConfig(args []string) (config, error) {
	cfg := config{}
	var durationText string
	fs := flag.NewFlagSet("fight-landlord-public-test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&cfg.Preset, "preset", presetSmoke, "smoke or public-test")
	fs.StringVar(&cfg.URL, "url", "ws://127.0.0.1:1780/ws", "WebSocket endpoint")
	fs.StringVar(&cfg.MetricsURL, "metrics-url", "auto", "Prometheus endpoint or auto")
	fs.StringVar(&durationText, "duration", "", "workload duration (defaults to the preset duration)")
	fs.IntVar(&cfg.Players, "players", 18, "simulated players; must be a multiple of three")
	fs.Float64Var(&cfg.DisconnectRate, "disconnect-rate", 0.02, "probability of reconnecting before an action")
	fs.Var(explicitBool{value: &cfg.DouZero}, "douzero", "whether the target was started with DouZero")
	fs.DurationVar(&cfg.OperationTimeout, "operation-timeout", 10*time.Second, "timeout for one protocol command")
	fs.DurationVar(&cfg.Cooldown, "cooldown", 35*time.Second, "cleanup telemetry window")
	fs.DurationVar(&cfg.MetricsInterval, "metrics-interval", time.Second, "Prometheus sampling interval")
	fs.StringVar(&cfg.ClientVersion, "client-version", "public-test-smoke", "client version used for negotiation")
	fs.StringVar(&cfg.JSONOut, "json-out", "artifacts/public-test/smoke-report.json", "JSON report path")
	fs.StringVar(&cfg.MarkdownOut, "markdown-out", "artifacts/public-test/smoke-report.md", "Markdown report path")
	fs.Int64Var(&cfg.Seed, "seed", 1, "random seed")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	presetDuration, ok := presetDurations[cfg.Preset]
	if !ok {
		return config{}, errors.New("preset must be exactly smoke or public-test")
	}
	if durationText == "" {
		cfg.Duration = presetDuration
	} else {
		duration, err := time.ParseDuration(durationText)
		if err != nil {
			return config{}, fmt.Errorf("parse duration: %w", err)
		}
		cfg.Duration = duration
	}

	if err := cfg.normalizeEndpoint(); err != nil {
		return config{}, err
	}
	if err := cfg.validate(); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func (c *config) normalizeEndpoint() error {
	endpoint, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil {
		return fmt.Errorf("parse WebSocket URL: %w", err)
	}
	if endpoint.Scheme != "ws" && endpoint.Scheme != "wss" {
		return errors.New("WebSocket URL must use ws or wss")
	}
	if endpoint.Host == "" {
		return errors.New("WebSocket URL must include a host")
	}
	if endpoint.Path == "" || endpoint.Path == "/" {
		endpoint.Path = "/ws"
	}
	c.URL = endpoint.String()

	switch strings.ToLower(strings.TrimSpace(c.MetricsURL)) {
	case "", "auto":
		metrics := *endpoint
		if metrics.Scheme == "ws" {
			metrics.Scheme = "http"
		} else {
			metrics.Scheme = "https"
		}
		metrics.Path = "/metrics"
		metrics.RawQuery = ""
		metrics.Fragment = ""
		c.MetricsURL = metrics.String()
	default:
		metrics, metricsErr := url.Parse(c.MetricsURL)
		if metricsErr != nil || (metrics.Scheme != "http" && metrics.Scheme != "https") || metrics.Host == "" {
			return errors.New("metrics URL must use http or https, or be auto")
		}
	}
	return nil
}

func (c config) validate() error {
	if c.Duration <= 0 {
		return errors.New("duration must be positive")
	}
	if c.Players < 3 || c.Players > 48 || c.Players%3 != 0 {
		return errors.New("players must be a multiple of three between 3 and 48")
	}
	if c.DisconnectRate < 0 || c.DisconnectRate > 1 {
		return errors.New("disconnect-rate must be between 0 and 1")
	}
	if c.OperationTimeout <= 0 || c.MetricsInterval <= 0 || c.Cooldown < 0 {
		return errors.New("operation-timeout and metrics-interval must be positive; cooldown cannot be negative")
	}
	if strings.TrimSpace(c.ClientVersion) == "" {
		return errors.New("client-version cannot be empty")
	}
	if strings.TrimSpace(c.JSONOut) == "" || strings.TrimSpace(c.MarkdownOut) == "" {
		return errors.New("both report paths are required")
	}
	return nil
}
