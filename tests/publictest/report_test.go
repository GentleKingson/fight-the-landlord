package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportThresholdsPassOnlyCompleteCleanRun(t *testing.T) {
	t.Parallel()

	zero64 := int64(0)
	initialGo, finalGo := 20, 25
	linear := false
	started, finished := int64(2), int64(2)
	report := publicTestReport{
		Configuration: reportConfiguration{DisconnectRate: 0.02},
		GamesStarted:  2, CompletedGames: 2, CleanCompletedGames: 2, CompleteGameSuccessRate: 1,
		ReconnectsAttempted: 2, ReconnectsSuccessful: 2, ReconnectSuccessRate: 1,
		RedisErrors: &zero64, RemainingConnections: &zero64, RemainingRooms: &zero64,
		InitialGoroutines: &initialGo, FinalGoroutines: &finalGo,
		LinearMemoryGrowth: &linear, TotalGamesReconciled: true, LeaderboardVerified: true,
		MetricGamesStarted: &started, MetricGamesFinished: &finished,
		Thresholds: publicTestThresholds,
	}
	report.evaluateThresholds()
	assert.Equal(t, "passed", report.Status)
	assert.Empty(t, report.ThresholdFailures)
}

func TestReportThresholdsRejectCleanupAndSettlementFailures(t *testing.T) {
	t.Parallel()

	one64 := int64(1)
	initialGo, finalGo := 20, 31
	linear := true
	started, finished := int64(1), int64(1)
	report := publicTestReport{
		GamesStarted: 1, CleanCompletedGames: 1, CompleteGameSuccessRate: 1,
		DuplicateSettlements: 1,
		RedisErrors:          &one64, RemainingConnections: &one64, RemainingRooms: &one64,
		ProtocolRoomsRemaining: 1,
		InitialGoroutines:      &initialGo, FinalGoroutines: &finalGo,
		LinearMemoryGrowth: &linear, TotalGamesReconciled: true, LeaderboardVerified: true,
		MetricGamesStarted: &started, MetricGamesFinished: &finished,
		Thresholds: publicTestThresholds,
	}
	report.evaluateThresholds()
	assert.Equal(t, "failed", report.Status)
	require.NotEmpty(t, report.ThresholdFailures)
	assert.Contains(t, report.ThresholdFailures[0], "duplicate settlements")
}

func TestDetectLinearMemoryGrowth(t *testing.T) {
	t.Parallel()

	started := time.Now()
	points := make([]resourcePoint, 8)
	for index := range points {
		points[index] = resourcePoint{
			at:  started.Add(time.Duration(index) * time.Minute),
			rss: float64(10*1024*1024 + index*2*1024*1024),
		}
	}
	assessed, linear, slope, r2 := detectLinearMemoryGrowth(points)
	assert.True(t, assessed)
	assert.True(t, linear)
	assert.Greater(t, slope, float64(1024*1024))
	assert.GreaterOrEqual(t, r2, 0.8)
}

func TestSummarizeLatency(t *testing.T) {
	t.Parallel()

	summary := summarizeLatency([]float64{5, 1, 3, 2, 4})
	assert.Equal(t, 3.0, summary.P50)
	assert.Equal(t, 5.0, summary.P95)
	assert.Equal(t, 5.0, summary.P99)
}
