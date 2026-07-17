package room

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/observability"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestRoomManagerMetricsTrackPublishedRoomsAndCleanup(t *testing.T) {
	metrics := observability.NewMetrics(true)
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	rm.SetMetrics(metrics)
	t.Cleanup(func() { require.NoError(t, rm.Close()) })

	host := testutil.NewSimpleClient("metrics-host", "Metrics host")
	gameRoom, err := rm.CreateRoom(host)
	require.NoError(t, err)
	require.Equal(t, float64(1), roomMetricValue(t, metrics, "fight_landlord_rooms_current"))

	require.True(t, rm.RemoveRoom(gameRoom, RoomRemovalRollback))
	require.Equal(t, float64(0), roomMetricValue(t, metrics, "fight_landlord_rooms_current"))
	require.Equal(t, float64(1), roomMetricValue(t, metrics, "fight_landlord_room_cleanup_total"))
}

func TestMatchRoomTransactionCommitUpdatesRoomMetric(t *testing.T) {
	metrics := observability.NewMetrics(true)
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	rm.SetMetrics(metrics)
	t.Cleanup(func() { require.NoError(t, rm.Close()) })

	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient("metrics-match-1", "Metrics match 1"),
		testutil.NewSimpleClient("metrics-match-2", "Metrics match 2"),
		testutil.NewSimpleClient("metrics-match-3", "Metrics match 3"),
	}
	tx, err := rm.BeginMatchRoom(clients[0])
	require.NoError(t, err)
	require.NoError(t, tx.Join(clients[1]))
	require.NoError(t, tx.Join(clients[2]))
	require.Equal(t, float64(0), roomMetricValue(t, metrics, "fight_landlord_rooms_current"), "pending rooms are not published")

	gameRoom, err := tx.Commit()
	require.NoError(t, err)
	require.Same(t, tx.Room(), gameRoom)
	require.Equal(t, float64(1), roomMetricValue(t, metrics, "fight_landlord_rooms_current"))

	tx.Rollback()
	require.Equal(t, float64(0), roomMetricValue(t, metrics, "fight_landlord_rooms_current"))
	require.Equal(t, float64(1), roomMetricValue(t, metrics, "fight_landlord_room_cleanup_total"))
}

func roomMetricValue(t *testing.T, metrics *observability.Metrics, name string) float64 {
	t.Helper()
	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if len(metric.GetLabel()) != 0 {
				continue
			}
			if metric.Gauge != nil {
				return metric.GetGauge().GetValue()
			}
			if metric.Counter != nil {
				return metric.GetCounter().GetValue()
			}
			t.Fatalf("metric %s is not a gauge or counter", name)
		}
	}
	t.Fatalf("metric %s without labels was not gathered", name)
	return 0
}
