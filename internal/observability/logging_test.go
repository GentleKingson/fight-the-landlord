package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONLoggerIsMachineParseableAndRedactsSecrets(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger, err := NewLogger("json", &output)
	require.NoError(t, err)
	logger.Info("command completed",
		slog.String("event", "command_complete"),
		slog.String("request_id", "request-1"),
		slog.String("reconnect_token", "token-sentinel"),
		slog.String("web_session_ticket", "ticket-sentinel"),
		slog.String("redis.password", "password-sentinel"),
		slog.String("cookie", "cookie-sentinel"),
	)

	var record map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &record))
	assert.Equal(t, "INFO", record["level"])
	assert.Equal(t, "command_complete", record["event"])
	assert.Equal(t, "request-1", record["request_id"])
	assert.Equal(t, redactedValue, record["reconnect_token"])
	assert.Equal(t, redactedValue, record["web_session_ticket"])
	assert.Equal(t, redactedValue, record["redis.password"])
	assert.Equal(t, redactedValue, record["cookie"])
	assert.NotContains(t, output.String(), "token-sentinel")
	assert.NotContains(t, output.String(), "ticket-sentinel")
	assert.NotContains(t, output.String(), "password-sentinel")
	assert.NotContains(t, output.String(), "cookie-sentinel")
}

func TestTextLoggerIncludesStructuredFields(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger, err := NewLogger("text", &output)
	require.NoError(t, err)
	logger.Info("ready", "event", "readiness_changed", "duration_ms", 12)

	line := strings.TrimSpace(output.String())
	assert.Contains(t, line, "level=INFO")
	assert.Contains(t, line, "event=readiness_changed")
	assert.Contains(t, line, "duration_ms=12")
}

func TestLoggerRejectsUnknownFormat(t *testing.T) {
	t.Parallel()
	_, err := NewLogger("yaml", &bytes.Buffer{})
	require.Error(t, err)
}
