package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeLatencyUsesNearestRank(t *testing.T) {
	summary := summarizeLatency([]float64{100, 1, 50, 10, 5})
	assert.Equal(t, 5, summary.Count)
	assert.Equal(t, 10.0, summary.P50)
	assert.Equal(t, 100.0, summary.P95)
	assert.Equal(t, 100.0, summary.P99)
	assert.Equal(t, 100.0, summary.Max)
}

func TestThresholdsFailClosedWhenRequiredTelemetryIsMissing(t *testing.T) {
	report := loadReport{
		ConnectionSuccessRate: 1,
		IdleSuccessRate:       1,
		Thresholds: thresholds{
			MinConnectionSuccessRate: 1,
			MinIdleSuccessRate:       1,
			MaxServerRSSBytes:        1,
			MaxRedisErrors:           0,
			MaxFinalGoroutinesDelta:  -1,
			MaxFinalConnectionsDelta: -1,
		},
	}
	report.evaluateThresholds()
	assert.Equal(t, "failed", report.Status)
	require.Len(t, report.ThresholdFailures, 2)
	assert.Contains(t, report.ThresholdFailures[0]+report.ThresholdFailures[1], "telemetry is unavailable")
}

func TestThresholdsPassWithDisabledPerformanceLimits(t *testing.T) {
	report := loadReport{
		ConnectionSuccessRate: 1,
		IdleSuccessRate:       1,
		Thresholds: thresholds{
			MinConnectionSuccessRate: 1,
			MinIdleSuccessRate:       1,
			MaxRedisErrors:           -1,
			MaxFinalGoroutinesDelta:  -1,
			MaxFinalConnectionsDelta: -1,
		},
	}
	report.evaluateThresholds()
	assert.Equal(t, "passed", report.Status)
	assert.Empty(t, report.ThresholdFailures)
}

func TestMatchThresholdIncludesCancellationAndTimeoutOutcomes(t *testing.T) {
	report := loadReport{
		ConnectionSuccessRate:     1,
		IdleSuccessRate:           1,
		MatchOperationsAttempted:  2,
		MatchOperationsSuccessful: 2,
		MatchTimeoutsAttempted:    1,
		MatchTimeoutsObserved:     0,
		MatchSuccessRate:          2.0 / 3.0,
		Thresholds: thresholds{
			MinConnectionSuccessRate: 1,
			MinIdleSuccessRate:       1,
			MinMatchSuccessRate:      1,
			MaxRedisErrors:           -1,
			MaxFinalGoroutinesDelta:  -1,
			MaxFinalConnectionsDelta: -1,
		},
	}
	report.evaluateThresholds()
	assert.Equal(t, "failed", report.Status)
	assert.Contains(t, report.ThresholdFailures, "match success rate 0.6667 is below 1.0000")
}

func TestFinalGoroutineThresholdUsesBaselineDelta(t *testing.T) {
	baseline := 12
	final := 15
	report := loadReport{
		ConnectionSuccessRate: 1,
		IdleSuccessRate:       1,
		BaselineGoroutines:    &baseline,
		FinalGoroutines:       &final,
		Thresholds: thresholds{
			MinConnectionSuccessRate: 1,
			MinIdleSuccessRate:       1,
			MaxFinalGoroutinesDelta:  2,
			MaxRedisErrors:           -1,
			MaxFinalConnectionsDelta: -1,
		},
	}
	report.evaluateThresholds()
	assert.Equal(t, "failed", report.Status)
	assert.Contains(t, report.ThresholdFailures, "final goroutine delta 3 exceeds 2")
}
