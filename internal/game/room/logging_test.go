package room

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/observability"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestRoomLifecycleLogsStructuredIdentifiersWithoutPlayerNames(t *testing.T) {
	var output bytes.Buffer
	logger, err := observability.NewLogger("json", &output)
	require.NoError(t, err)
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 1})
	rm.logger = logger.With("component", "room")
	rm.roomCodeFunc = func() string { return "123456" }
	t.Cleanup(func() { require.NoError(t, rm.Close()) })

	host := testutil.NewSimpleClient("host-1", "host-name-sentinel")
	guest := testutil.NewSimpleClient("guest-1", "guest-name-sentinel")
	gameRoom, err := rm.CreateRoom(host)
	require.NoError(t, err)
	_, err = rm.JoinRoom(guest, gameRoom.Code)
	require.NoError(t, err)
	require.True(t, rm.LeaveRoom(guest))
	gameRoom.SetCreatedAtForTest(time.Now().Add(-2 * time.Hour))
	rm.cleanup()

	records := decodeRoomLogRecords(t, output.Bytes())
	require.Len(t, records, 4)
	require.Equal(t, "room_created", records[0]["event"])
	require.Equal(t, "123456", records[0]["room_id"])
	require.Equal(t, "host-1", records[0]["player_id"])
	require.Equal(t, "created", records[0]["result"])

	require.Equal(t, "room_joined", records[1]["event"])
	require.Equal(t, "guest-1", records[1]["player_id"])
	require.EqualValues(t, 1, records[1]["seat"])
	require.Equal(t, "joined", records[1]["result"])

	require.Equal(t, "room_left", records[2]["event"])
	require.Equal(t, "guest-1", records[2]["player_id"])
	require.Equal(t, "left", records[2]["result"])

	require.Equal(t, "room_cleaned", records[3]["event"])
	require.Equal(t, string(RoomRemovalTimeout), records[3]["reason"])
	require.EqualValues(t, 1, records[3]["player_count"])
	require.Equal(t, "removed", records[3]["result"])
	require.NotContains(t, output.String(), "host-name-sentinel")
	require.NotContains(t, output.String(), "guest-name-sentinel")
}

func decodeRoomLogRecords(t *testing.T, data []byte) []map[string]any {
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
