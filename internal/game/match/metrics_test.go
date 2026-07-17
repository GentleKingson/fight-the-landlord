package match

import (
	"context"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/observability"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

func TestMatcherMetricsTrackQueueCancellationAndTimeout(t *testing.T) {
	metrics := observability.NewMetrics(true)
	matcher := NewMatcher(MatcherDeps{
		Metrics:      metrics,
		QueueTimeout: 25 * time.Millisecond,
		BotFillDelay: time.Hour,
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	cancelled := newMatcherClient("metrics-cancelled", false)
	require.True(t, matcher.AddToQueue(cancelled))
	require.Equal(t, float64(1), matcherMetricValue(t, metrics, "fight_landlord_match_queue_current", nil))
	require.True(t, matcher.RemoveFromQueue(cancelled))
	require.Equal(t, float64(0), matcherMetricValue(t, metrics, "fight_landlord_match_queue_current", nil))
	require.Equal(t, float64(1), matcherMetricValue(t, metrics, "fight_landlord_match_cancelled_total", map[string]string{"reason": protocol.MatchCancelReason}))

	timedOut := newMatcherClient("metrics-timeout", false)
	require.True(t, matcher.AddToQueue(timedOut))
	timedOut.waitForMessage(t, protocol.MsgMatchCancelled)
	require.Eventually(t, func() bool {
		return matcherMetricValue(t, metrics, "fight_landlord_match_queue_current", nil) == 0
	}, matcherTestDeadline, 5*time.Millisecond)
	require.Equal(t, float64(1), matcherMetricValue(t, metrics, "fight_landlord_match_cancelled_total", map[string]string{"reason": "timeout"}))
}

func TestMatcherMetricsRecordBeginRollback(t *testing.T) {
	metrics := observability.NewMetrics(true)
	clients := newMatcherClients("metrics-rollback", 3)
	registry := newActiveClientRegistry(clients...)
	matcher := NewMatcher(MatcherDeps{
		Metrics:             metrics,
		QueueTimeout:        time.Hour,
		BotFillDelay:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(context.Context, types.ClientInterface) (RoomAssembly, error) {
			return nil, errInjectedMatchAssembly
		},
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	addThreeToMatcher(t, matcher, clients)
	for _, client := range clients {
		client.waitForMessage(t, protocol.MsgMatchCancelled)
	}
	matcher.workers.Wait()

	require.Equal(t, float64(1), matcherMetricValue(t, metrics, "fight_landlord_match_transaction_rollback_total", map[string]string{"stage": "begin"}))
	require.Equal(t, float64(1), matcherMetricValue(t, metrics, "fight_landlord_match_cancelled_total", map[string]string{"reason": "assembly_failed"}))
	require.Equal(t, float64(0), matcherMetricValue(t, metrics, "fight_landlord_match_queue_current", nil))
}

func TestMatcherMetricsObserveCommittedWait(t *testing.T) {
	metrics := observability.NewMetrics(true)
	clients := newMatcherClients("metrics-commit", 3)
	registry := newActiveClientRegistry(clients...)
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	registered := make(chan *session.GameSession, 1)
	matcher := NewMatcher(MatcherDeps{
		Metrics:             metrics,
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
	for _, client := range clients {
		client.waitForMessage(t, protocol.MsgRoomJoined)
	}
	game := waitValue(t, registered, "registered metrics game")
	t.Cleanup(game.StopAllTimers)

	count, sum := matcherHistogramValue(t, metrics, "fight_landlord_match_wait_seconds", nil)
	require.EqualValues(t, 3, count)
	require.GreaterOrEqual(t, sum, float64(0))
	require.Equal(t, float64(0), matcherMetricValue(t, metrics, "fight_landlord_match_queue_current", nil))
}

func matcherMetricValue(t *testing.T, metrics *observability.Metrics, name string, labels map[string]string) float64 {
	t.Helper()
	metric := matcherMetric(t, metrics, name, labels)
	if metric.Gauge != nil {
		return metric.GetGauge().GetValue()
	}
	if metric.Counter != nil {
		return metric.GetCounter().GetValue()
	}
	t.Fatalf("metric %s is not a gauge or counter", name)
	return 0
}

func matcherHistogramValue(t *testing.T, metrics *observability.Metrics, name string, labels map[string]string) (uint64, float64) {
	t.Helper()
	histogram := matcherMetric(t, metrics, name, labels).GetHistogram()
	require.NotNil(t, histogram, "metric %s is not a histogram", name)
	return histogram.GetSampleCount(), histogram.GetSampleSum()
}

func matcherMetric(t *testing.T, metrics *observability.Metrics, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if matcherMetricLabelsEqual(metric, labels) {
				return metric
			}
		}
	}
	t.Fatalf("metric %s with labels %v was not gathered", name, labels)
	return nil
}

func matcherMetricLabelsEqual(metric *dto.Metric, labels map[string]string) bool {
	if len(metric.GetLabel()) != len(labels) {
		return false
	}
	for _, pair := range metric.GetLabel() {
		if labels[pair.GetName()] != pair.GetValue() {
			return false
		}
	}
	return true
}
