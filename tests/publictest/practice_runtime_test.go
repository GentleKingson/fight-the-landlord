package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

func TestPracticeMatchRulesBotsCompleteOneGame(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("PUBLIC_TEST_PRACTICE_URL"))
	if endpoint == "" {
		t.Skip("set PUBLIC_TEST_PRACTICE_URL to run the live practice-match acceptance test")
	}

	const operationTimeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cfg := config{
		URL:              endpoint,
		MetricsURL:       strings.TrimSpace(os.Getenv("PUBLIC_TEST_PRACTICE_METRICS_URL")),
		OperationTimeout: operationTimeout,
		ClientVersion:    "public-test-practice-runtime",
		Seed:             1,
	}
	if cfg.MetricsURL == "" {
		cfg.MetricsURL = "auto"
	}
	require.NoError(t, cfg.normalizeEndpoint())
	run := newRunState(cfg)
	room := newGameRoom(run)

	client, _, err := connectGameClient(ctx, cfg, run, 0)
	require.NoError(t, err)
	defer client.close()
	leftRoom := false
	defer func() {
		if leftRoom {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		waitForPracticeRoomIdle(cleanupCtx, room)
		_, _ = client.leaveRoom(cleanupCtx)
	}()
	room.clients = []*gameClient{client}
	client.room = room

	baselineRooms, _, err := client.roomList(ctx)
	require.NoError(t, err)
	baselineRoomMetric, err := readPracticeMetric(ctx, cfg.MetricsURL, metricRoomsCurrent)
	require.NoError(t, err)

	baseline, _, err := client.stats(ctx)
	require.NoError(t, err)

	queued, _, err := client.command(ctx, protocol.MsgPracticeMatch, protocol.MsgMatchQueued, nil, "", 0)
	require.NoError(t, err)
	queuedPayload, err := codec.ParsePayload[protocol.MatchQueuedPayload](queued.message())
	require.NoError(t, err)
	assert.True(t, queuedPayload.Practice, "server did not identify the match as practice mode")

	require.NoError(t, waitForPracticeGame(ctx, run, room))
	finalStats, err := waitForPracticeStats(ctx, client, baseline.TotalGames+1)
	require.NoError(t, err)
	assert.Equal(t, baseline.TotalGames+1, finalStats.TotalGames)

	// Leave a short observation window for a repeated terminal event or an
	// accidental bot-ready rematch to become visible before asserting counts.
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	stableStats, _, err := client.stats(ctx)
	require.NoError(t, err)
	require.Equal(t, baseline.TotalGames+1, stableStats.TotalGames, "practice settlement changed during the quiet window")

	started, completed, failed, active, pending := room.counts()
	assert.Equal(t, 1, started)
	assert.Equal(t, 1, completed)
	assert.Zero(t, failed)
	assert.Zero(t, active)
	assert.Zero(t, pending)

	errorsSeen, duplicateSettlements := practiceRunOutcome(run)
	assert.Empty(t, errorsSeen)
	assert.Zero(t, duplicateSettlements)
	assert.Equal(t, 1, clientSettlementCount(client))

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cleanupCancel()
	_, err = client.leaveRoom(cleanupCtx)
	require.NoError(t, err)
	leftRoom = true
	require.NoError(t, waitForPracticeCleanup(
		cleanupCtx,
		client,
		len(baselineRooms.Rooms),
		cfg.MetricsURL,
		baselineRoomMetric,
	))
}

func waitForPracticeRoomIdle(ctx context.Context, room *gameRoom) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		started, completed, _, active, _ := room.counts()
		if started == 0 || completed > 0 || active == 0 {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func waitForPracticeCleanup(
	ctx context.Context,
	client *gameClient,
	baselineRoomList int,
	metricsURL string,
	baselineRoomMetric int64,
) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		roomList, _, roomListErr := client.roomList(ctx)
		roomMetric, metricErr := readPracticeMetric(ctx, metricsURL, metricRoomsCurrent)
		if roomListErr == nil && metricErr == nil && len(roomList.Rooms) == baselineRoomList && roomMetric == baselineRoomMetric {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf(
				"practice room cleanup did not restore baselines (room list %d, metric %d): %w",
				baselineRoomList,
				baselineRoomMetric,
				ctx.Err(),
			)
		}
	}
}

func readPracticeMetric(ctx context.Context, metricsURL, metricName string) (int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("create metrics request: %w", err)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return 0, fmt.Errorf("request metrics: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("metrics endpoint returned HTTP %d", response.StatusCode)
	}
	values, err := parsePrometheus(response.Body)
	if err != nil {
		return 0, fmt.Errorf("parse metrics: %w", err)
	}
	value, ok := values[metricName]
	if !ok {
		return 0, fmt.Errorf("metrics endpoint omitted %s", metricName)
	}
	return int64(value), nil
}

func waitForPracticeGame(ctx context.Context, run *runState, room *gameRoom) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		started, completed, _, _, _ := room.counts()
		errorsSeen, _ := practiceRunOutcome(run)
		if len(errorsSeen) > 0 {
			return fmt.Errorf("practice client failed: %s", strings.Join(errorsSeen, "; "))
		}
		if completed > 0 {
			if started != 1 || completed != 1 {
				return fmt.Errorf("expected one completed game, got %d started and %d completed", started, completed)
			}
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("waiting for practice game completion: %w", ctx.Err())
		}
	}
}

func waitForPracticeStats(
	ctx context.Context,
	client *gameClient,
	expectedTotalGames int,
) (protocol.StatsResultPayload, error) {
	deadline := time.Now().Add(client.cfg.OperationTimeout)
	for {
		stats, _, err := client.stats(ctx)
		if err != nil {
			return protocol.StatsResultPayload{}, fmt.Errorf("query practice stats: %w", err)
		}
		if stats.TotalGames == expectedTotalGames {
			return stats, nil
		}
		if stats.TotalGames > expectedTotalGames {
			return stats, fmt.Errorf("practice game settled more than once: total_games=%d, expected=%d", stats.TotalGames, expectedTotalGames)
		}
		if time.Now().After(deadline) {
			return stats, fmt.Errorf("practice settlement did not reach total_games=%d", expectedTotalGames)
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return stats, fmt.Errorf("waiting for practice settlement: %w", ctx.Err())
		}
	}
}

func practiceRunOutcome(run *runState) (errorsSeen []string, duplicateSettlements int) {
	run.mu.Lock()
	defer run.mu.Unlock()
	return append([]string(nil), run.errors...), run.duplicateSettlements
}

func clientSettlementCount(client *gameClient) int {
	client.settlementMu.Lock()
	defer client.settlementMu.Unlock()
	return len(client.settlements)
}
