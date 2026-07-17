package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfigNormalizesEndpoints(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--url", "wss://game.example.com",
		"--connections", "5",
		"--reconnects", "2",
	})
	require.NoError(t, err)
	assert.Equal(t, "wss://game.example.com/ws", cfg.URL)
	assert.Equal(t, "https://game.example.com/metrics", cfg.MetricsURL)
	assert.Equal(t, 5, cfg.Connections)
	assert.Equal(t, 2, cfg.Reconnects)
}

func TestParseConfigRejectsUnsafeBounds(t *testing.T) {
	for _, args := range [][]string{
		{"--url", "https://game.example.com/ws"},
		{"--connections", "0"},
		{"--duration", "0s"},
		{"--min-connection-success-rate", "1.1"},
		{"--match-operations", "-1"},
		{"--match-timeout-wait", "0s"},
		{"--max-final-goroutines-delta", "-2"},
		{"--max-redis-errors", "-2"},
	} {
		_, err := parseConfig(args)
		require.Error(t, err, "args: %v", args)
	}
}
