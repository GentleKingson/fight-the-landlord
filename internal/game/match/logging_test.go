package match

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/observability"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

func TestMatcherLogsBoundedEnqueueAndRollbackResults(t *testing.T) {
	var output lockedLogBuffer
	logger, err := observability.NewLogger("json", &output)
	require.NoError(t, err)
	clients := newMatcherClients("log-rollback", 3)
	for _, client := range clients {
		client.name = "player-name-must-not-be-logged"
	}
	registry := newActiveClientRegistry(clients...)
	matcher := NewMatcher(MatcherDeps{
		Logger:              logger,
		QueueTimeout:        time.Hour,
		BotFillDelay:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(context.Context, types.ClientInterface) (RoomAssembly, error) {
			return nil, errors.New("assembly-detail-must-not-be-structured")
		},
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	require.True(t, matcher.AddToQueue(clients[0]))
	require.False(t, matcher.AddToQueue(clients[0]))
	require.True(t, matcher.AddToQueue(clients[1]))
	require.True(t, matcher.AddToQueue(clients[2]))
	for _, client := range clients {
		client.waitForMessage(t, "match_cancelled")
	}
	matcher.workers.Wait()

	records := decodeMatchLogRecords(t, output.Bytes())
	require.Len(t, recordsByMatchEvent(records, "match_enqueue"), 4)
	rejected := firstMatchRecord(t, records, "match_enqueue", "rejected")
	require.Equal(t, "already_queued", rejected["reason"])
	require.Equal(t, "standard", rejected["mode"])
	rollback := firstMatchRecord(t, records, "match_rollback", "rolled_back")
	require.Equal(t, "assembly_failed", rollback["reason"])
	require.Equal(t, "begin", rollback["stage"])
	require.EqualValues(t, 3, rollback["participant_count"])
	require.NotContains(t, output.String(), "player-name-must-not-be-logged")
	require.NotContains(t, output.String(), "assembly-detail-must-not-be-structured")
}

func TestMatcherLogsCommittedMatchWithoutPlayerNames(t *testing.T) {
	var output lockedLogBuffer
	logger, err := observability.NewLogger("json", &output)
	require.NoError(t, err)
	clients := newMatcherClients("log-success", 3)
	for _, client := range clients {
		client.name = "success-name-must-not-be-logged"
	}
	registry := newActiveClientRegistry(clients...)
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	registered := make(chan *session.GameSession, 1)
	matcher := NewMatcher(MatcherDeps{
		Logger:              logger,
		RoomManager:         roomManager,
		QueueTimeout:        time.Hour,
		BotFillDelay:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		GameConfig:          config.GameConfig{TurnTimeout: 3600, BidTimeout: 3600},
		RegisterSession: func(_ string, game *session.GameSession) bool {
			registered <- game
			return true
		},
	})
	t.Cleanup(func() {
		require.NoError(t, matcher.Close())
		require.NoError(t, roomManager.Close())
	})

	addThreeToMatcher(t, matcher, clients)
	game := waitValue(t, registered, "registered logging game")
	t.Cleanup(game.StopAllTimers)
	matcher.workers.Wait()

	records := decodeMatchLogRecords(t, output.Bytes())
	success := firstMatchRecord(t, records, "match_success", "committed")
	require.NotEmpty(t, success["room_id"])
	require.Equal(t, "standard", success["mode"])
	require.EqualValues(t, 3, success["participant_count"])
	require.GreaterOrEqual(t, success["duration_ms"].(float64), float64(0))
	require.NotContains(t, output.String(), "success-name-must-not-be-logged")
}

type lockedLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedLogBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *lockedLogBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return bytes.Clone(buffer.buffer.Bytes())
}

func (buffer *lockedLogBuffer) String() string {
	return string(buffer.Bytes())
}

func decodeMatchLogRecords(t *testing.T, data []byte) []map[string]any {
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

func recordsByMatchEvent(records []map[string]any, event string) []map[string]any {
	matching := make([]map[string]any, 0)
	for _, record := range records {
		if record["event"] == event {
			matching = append(matching, record)
		}
	}
	return matching
}

func firstMatchRecord(t *testing.T, records []map[string]any, event, result string) map[string]any {
	t.Helper()
	for _, record := range records {
		if record["event"] == event && record["result"] == result {
			return record
		}
	}
	t.Fatalf("missing log record event=%s result=%s", event, result)
	return nil
}
