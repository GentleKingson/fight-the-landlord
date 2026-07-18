package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfigPresetsAndExplicitBoolean(t *testing.T) {
	t.Parallel()

	smoke, err := parseConfig([]string{"--preset", "smoke", "--douzero", "false"})
	require.NoError(t, err)
	assert.Equal(t, 10*time.Minute, smoke.Duration)
	assert.False(t, smoke.DouZero)

	publicTest, err := parseConfig([]string{"--preset", "public-test", "--douzero", "true"})
	require.NoError(t, err)
	assert.Equal(t, time.Hour, publicTest.Duration)
	assert.True(t, publicTest.DouZero)
}

func TestParseConfigAllowsDurationOverride(t *testing.T) {
	t.Parallel()

	cfg, err := parseConfig([]string{"--preset", "smoke", "--duration", "15s", "--players", "6"})
	require.NoError(t, err)
	assert.Equal(t, 15*time.Second, cfg.Duration)
	assert.Equal(t, 6, cfg.Players)
}

func TestParseConfigRejectsUnsupportedPresetAndIncompleteRoom(t *testing.T) {
	t.Parallel()

	_, err := parseConfig([]string{"--preset", "soak"})
	assert.ErrorContains(t, err, "exactly smoke or public-test")

	_, err = parseConfig([]string{"--players", "10"})
	assert.ErrorContains(t, err, "multiple of three")
}

func TestParseConfigRejectsInvalidDisconnectRate(t *testing.T) {
	t.Parallel()

	_, err := parseConfig([]string{"--disconnect-rate", "1.01"})
	assert.ErrorContains(t, err, "between 0 and 1")
}
