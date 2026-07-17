package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePrometheusSumsLabelledCounters(t *testing.T) {
	values, err := parsePrometheus(strings.NewReader(`
# HELP fight_landlord_redis_errors_total Redis errors.
fight_landlord_redis_errors_total{operation="get"} 2
fight_landlord_redis_errors_total{operation="set"} 3
process_resident_memory_bytes 4096
go_goroutines 17
`))
	require.NoError(t, err)
	assert.Equal(t, 5.0, values[metricRedisErrors])
	assert.Equal(t, 4096.0, values[metricProcessRSS])
	assert.Equal(t, 17.0, values[metricGoRoutines])
}

func TestParsePrometheusIgnoresOptionalTimestamp(t *testing.T) {
	values, err := parsePrometheus(strings.NewReader("process_resident_memory_bytes 123 999999999\n"))
	require.NoError(t, err)
	assert.Equal(t, 123.0, values[metricProcessRSS])
}
