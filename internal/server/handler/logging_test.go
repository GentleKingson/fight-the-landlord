package handler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/observability"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestCommandDispatchLogsStructuredMetadataWithoutPayload(t *testing.T) {
	var output bytes.Buffer
	logger, err := observability.NewLogger("json", &output)
	require.NoError(t, err)
	h := NewHandler(HandlerDeps{Logger: logger})
	client := testutil.NewSimpleClient("player-1", "name-must-not-be-logged")

	ping := codec.MustNewMessage(protocol.MsgPing, protocol.PingPayload{Timestamp: 42})
	ping.Command = &protocol.CommandMeta{RequestID: "request-ok"}
	h.Handle(client, ping)
	codec.PutMessage(ping)

	unknown := &protocol.Message{
		Type:    protocol.MessageType("unsupported_command"),
		Payload: []byte(`{"reconnect_token":"payload-token-sentinel"}`),
		Command: &protocol.CommandMeta{RequestID: "request-rejected"},
	}
	h.Handle(client, unknown)

	records := decodeJSONLogRecords(t, output.Bytes())
	require.Len(t, records, 2)
	require.Equal(t, "command_dispatch", records[0]["event"])
	require.Equal(t, "request-ok", records[0]["request_id"])
	require.Equal(t, "player-1", records[0]["player_id"])
	require.Equal(t, protocol.ClientKindTUI, records[0]["client_kind"])
	require.Equal(t, string(protocol.MsgPing), records[0]["type"])
	require.Equal(t, "completed", records[0]["result"])
	require.EqualValues(t, 0, records[0]["error_code"])
	require.GreaterOrEqual(t, records[0]["duration_ms"].(float64), float64(0))

	require.Equal(t, "command_dispatch", records[1]["event"])
	require.Equal(t, "request-rejected", records[1]["request_id"])
	require.Equal(t, "rejected", records[1]["result"])
	require.EqualValues(t, protocol.ErrCodeInvalidMsg, records[1]["error_code"])
	require.NotContains(t, output.String(), "payload-token-sentinel")
	require.NotContains(t, output.String(), "name-must-not-be-logged")
	for _, record := range records {
		_, hasPayload := record["payload"]
		require.False(t, hasPayload)
	}
}

func decodeJSONLogRecords(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	records := make([]map[string]any, 0)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var record map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
		records = append(records, record)
	}
	require.NoError(t, scanner.Err())
	return records
}
