package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsHandlerExposesChangingValuesAndBoundedLabels(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics(true)
	metrics.SetConnectionsCurrent(2)
	metrics.ConnectionAccepted()
	metrics.SetReady(true)
	metrics.ObserveCommand("player-controlled-command", "player-controlled-result", 25*time.Millisecond)
	metrics.ReconnectFailure("player-controlled-reason")
	metrics.ReconnectFailure("ticket")
	metrics.ReconnectFailure("superseded")
	metrics.ReconnectFailure("authority_race")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody)
	metrics.Handler().ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Content-Type"), "text/plain")
	body := recorder.Body.String()
	assert.Contains(t, body, "fight_landlord_websocket_connections_current 2")
	assert.Contains(t, body, "fight_landlord_websocket_connections_total 1")
	assert.Contains(t, body, "fight_landlord_readiness_status 1")
	assert.Contains(t, body, `fight_landlord_commands_total{result="other",type="other"} 1`)
	assert.Contains(t, body, `fight_landlord_reconnect_failure_total{reason="ticket"} 1`)
	assert.Contains(t, body, `fight_landlord_reconnect_failure_total{reason="superseded"} 1`)
	assert.Contains(t, body, `fight_landlord_reconnect_failure_total{reason="authority_race"} 1`)
	assert.NotContains(t, body, "player-controlled")
	for _, metricName := range []string{
		"fight_landlord_websocket_connections_current",
		"fight_landlord_websocket_connections_total",
		"fight_landlord_websocket_rejected_total",
		"fight_landlord_slow_client_disconnects_total",
		"fight_landlord_reconnect_attempts_total",
		"fight_landlord_reconnect_success_total",
		"fight_landlord_reconnect_failure_total",
		"fight_landlord_rooms_current",
		"fight_landlord_games_current",
		"fight_landlord_games_started_total",
		"fight_landlord_games_finished_total",
		"fight_landlord_game_duration_seconds",
		"fight_landlord_room_cleanup_total",
		"fight_landlord_match_queue_current",
		"fight_landlord_match_wait_seconds",
		"fight_landlord_match_cancelled_total",
		"fight_landlord_match_transaction_rollback_total",
		"fight_landlord_commands_total",
		"fight_landlord_command_latency_seconds",
		"fight_landlord_protocol_errors_total",
		"fight_landlord_idempotency_cache_hits_total",
		"fight_landlord_idempotency_conflicts_total",
		"fight_landlord_redis_operation_seconds",
		"fight_landlord_redis_errors_total",
		"fight_landlord_readiness_status",
		"fight_landlord_bot_decision_seconds",
		"fight_landlord_bot_timeouts_total",
		"fight_landlord_bot_fallback_total",
	} {
		assert.Contains(t, body, metricName)
	}
	for _, forbidden := range []string{"player_id", "room_id", "game_id", "nickname"} {
		assert.NotContains(t, body, forbidden)
	}

	// A second manager uses a private registry and must not panic.
	require.NotNil(t, NewMetrics(true))
}

func TestDisabledMetricsHandlerIsNotFound(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	NewMetrics(false).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestRedisHookRecordsLatencyAndErrorsWithoutArguments(t *testing.T) {
	t.Parallel()
	metrics := NewMetrics(true)
	hook := NewRedisHook(metrics)
	command := redis.NewStatusCmd(context.Background(), "PING", "credential-sentinel")
	wantErr := errors.New("redis unavailable")
	wrapped := hook.ProcessHook(func(context.Context, redis.Cmder) error { return wantErr })
	require.ErrorIs(t, wrapped(context.Background(), command), wantErr)

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body := recorder.Body.String()
	assert.Contains(t, body, `fight_landlord_redis_errors_total{operation="ping"} 1`)
	assert.Contains(t, body, `fight_landlord_redis_operation_seconds_count{operation="ping"} 1`)
	assert.False(t, strings.Contains(body, "credential-sentinel"))
}
