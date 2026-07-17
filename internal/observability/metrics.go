package observability

import (
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricsNamespace = "fight_landlord"

// Metrics owns a private Prometheus registry. Keeping registration local to a
// server instance prevents duplicate-registration panics in tests and in
// processes that construct more than one Server.
type Metrics struct {
	enabled  bool
	registry *prometheus.Registry

	connectionsCurrent    prometheus.Gauge
	connectionsTotal      prometheus.Counter
	connectionsRejected   prometheus.Counter
	slowClientDisconnects prometheus.Counter
	reconnectAttempts     prometheus.Counter
	reconnectSuccesses    prometheus.Counter
	reconnectFailures     *prometheus.CounterVec
	roomsCurrent          prometheus.Gauge
	roomCleanups          prometheus.Counter
	gamesCurrent          prometheus.Gauge
	gamesStarted          prometheus.Counter
	gamesFinished         prometheus.Counter
	gameDuration          prometheus.Histogram
	matchQueueCurrent     prometheus.Gauge
	matchWait             prometheus.Histogram
	matchCancelled        *prometheus.CounterVec
	matchRollbacks        *prometheus.CounterVec
	commands              *prometheus.CounterVec
	commandLatency        *prometheus.HistogramVec
	protocolErrors        *prometheus.CounterVec
	idempotencyHits       prometheus.Counter
	idempotencyConflicts  prometheus.Counter
	redisLatency          *prometheus.HistogramVec
	redisErrors           *prometheus.CounterVec
	readiness             prometheus.Gauge
	botDuration           *prometheus.HistogramVec
	botTimeouts           *prometheus.CounterVec
	botFallbacks          *prometheus.CounterVec
}

// NewMetrics constructs an isolated metric registry. Disabled metrics are
// represented by the same nil-safe object so call sites do not need branches.
func NewMetrics(enabled bool) *Metrics {
	m := &Metrics{enabled: enabled, registry: prometheus.NewRegistry()}
	if !enabled {
		return m
	}

	m.connectionsCurrent = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "websocket_connections_current", Help: "Current upgraded WebSocket connections."})
	m.connectionsTotal = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "websocket_connections_total", Help: "Total accepted WebSocket connections."})
	m.connectionsRejected = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "websocket_rejected_total", Help: "Total rejected WebSocket connection attempts."})
	m.slowClientDisconnects = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "slow_client_disconnects_total", Help: "Total clients disconnected because their outbound buffer was full."})
	m.reconnectAttempts = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "reconnect_attempts_total", Help: "Total reconnect commands received."})
	m.reconnectSuccesses = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "reconnect_success_total", Help: "Total reconnect commands completed successfully."})
	m.reconnectFailures = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "reconnect_failure_total", Help: "Reconnect failures by bounded reason."}, []string{"reason"})
	m.roomsCurrent = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "rooms_current", Help: "Current published rooms."})
	m.roomCleanups = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "room_cleanup_total", Help: "Total published rooms removed by lifecycle cleanup."})
	m.gamesCurrent = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "games_current", Help: "Current active game sessions."})
	m.gamesStarted = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "games_started_total", Help: "Total game sessions started."})
	m.gamesFinished = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "games_finished_total", Help: "Total game sessions finished."})
	m.gameDuration = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricsNamespace, Name: "game_duration_seconds", Help: "Game session duration in seconds.", Buckets: prometheus.ExponentialBuckets(5, 2, 10)})
	m.matchQueueCurrent = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "match_queue_current", Help: "Current players queued or reserved for matching."})
	m.matchWait = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricsNamespace, Name: "match_wait_seconds", Help: "Time a player waited before a match committed.", Buckets: prometheus.ExponentialBuckets(0.1, 2, 12)})
	m.matchCancelled = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "match_cancel" + "led_total", Help: "Match queue cancellations by bounded reason."}, []string{"reason"})
	m.matchRollbacks = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "match_transaction_rollback_total", Help: "Match transaction rollbacks by bounded stage."}, []string{"stage"})
	m.commands = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "commands_total", Help: "Client commands by type and bounded result."}, []string{"type", "result"})
	m.commandLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricsNamespace, Name: "command_latency_seconds", Help: "Client command latency by bounded type.", Buckets: prometheus.DefBuckets}, []string{"type"})
	m.protocolErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "protocol_errors_total", Help: "Protocol errors by bounded reason."}, []string{"reason"})
	m.idempotencyHits = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "idempotency_cache_hits_total", Help: "Commands served from an idempotency cache entry."})
	m.idempotencyConflicts = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "idempotency_conflicts_total", Help: "Request IDs reused with a different command fingerprint."})
	m.redisLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricsNamespace, Name: "redis_operation_seconds", Help: "Redis operation latency by bounded operation.", Buckets: prometheus.DefBuckets}, []string{"operation"})
	m.redisErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "redis_errors_total", Help: "Redis errors by bounded operation."}, []string{"operation"})
	m.readiness = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "readiness_status", Help: "Current readiness state (1 ready, 0 not ready)."})
	m.botDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricsNamespace, Name: "bot_decision_seconds", Help: "Bot decision duration by engine.", Buckets: prometheus.DefBuckets}, []string{"engine"})
	m.botTimeouts = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "bot_timeouts_total", Help: "Bot decision timeouts by engine."}, []string{"engine"})
	m.botFallbacks = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "bot_fallback_total", Help: "Bot engine fallbacks between bounded engines."}, []string{"from", "to"})

	m.registry.MustRegister(
		m.connectionsCurrent, m.connectionsTotal, m.connectionsRejected, m.slowClientDisconnects,
		m.reconnectAttempts, m.reconnectSuccesses, m.reconnectFailures,
		m.roomsCurrent, m.roomCleanups, m.gamesCurrent, m.gamesStarted, m.gamesFinished, m.gameDuration,
		m.matchQueueCurrent, m.matchWait, m.matchCancelled, m.matchRollbacks,
		m.commands, m.commandLatency, m.protocolErrors, m.idempotencyHits, m.idempotencyConflicts,
		m.redisLatency, m.redisErrors, m.readiness, m.botDuration, m.botTimeouts, m.botFallbacks,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	// Vector metric families do not appear in exposition until at least one
	// bounded child is created. Pre-create only the fixed fallback labels so
	// every documented metric is discoverable before the first event.
	m.reconnectFailures.WithLabelValues("other")
	m.matchCancelled.WithLabelValues("other")
	m.matchRollbacks.WithLabelValues("other")
	m.commands.WithLabelValues("other", "other")
	m.commandLatency.WithLabelValues("other")
	m.protocolErrors.WithLabelValues("other")
	m.redisLatency.WithLabelValues("other")
	m.redisErrors.WithLabelValues("other")
	m.botDuration.WithLabelValues("other")
	m.botTimeouts.WithLabelValues("other")
	m.botFallbacks.WithLabelValues("other", "other")
	return m
}

func (m *Metrics) Enabled() bool { return m != nil && m.enabled }

func (m *Metrics) Handler() http.Handler {
	if !m.Enabled() || m.registry == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

func (m *Metrics) Gatherer() prometheus.Gatherer {
	if m == nil {
		return prometheus.NewRegistry()
	}
	return m.registry
}

func (m *Metrics) SetConnectionsCurrent(value int) {
	if m.Enabled() {
		m.connectionsCurrent.Set(float64(max(value, 0)))
	}
}
func (m *Metrics) ConnectionAccepted() {
	if m.Enabled() {
		m.connectionsTotal.Inc()
	}
}
func (m *Metrics) ConnectionRejected() {
	if m.Enabled() {
		m.connectionsRejected.Inc()
	}
}
func (m *Metrics) SlowClientDisconnected() {
	if m.Enabled() {
		m.slowClientDisconnects.Inc()
	}
}

func (m *Metrics) ReconnectAttempt() {
	if m.Enabled() {
		m.reconnectAttempts.Inc()
	}
}
func (m *Metrics) ReconnectSuccess() {
	if m.Enabled() {
		m.reconnectSuccesses.Inc()
	}
}
func (m *Metrics) ReconnectFailure(reason string) {
	if m.Enabled() {
		m.reconnectFailures.WithLabelValues(bounded(reason, reconnectReasons)).Inc()
	}
}

func (m *Metrics) SetRoomsCurrent(value int) {
	if m.Enabled() {
		m.roomsCurrent.Set(float64(max(value, 0)))
	}
}
func (m *Metrics) RoomCleaned() {
	if m.Enabled() {
		m.roomCleanups.Inc()
	}
}
func (m *Metrics) GameStarted() {
	if m.Enabled() {
		m.gamesStarted.Inc()
		m.gamesCurrent.Inc()
	}
}
func (m *Metrics) GameFinished(duration time.Duration) {
	if !m.Enabled() {
		return
	}
	m.gamesFinished.Inc()
	m.gamesCurrent.Dec()
	m.gameDuration.Observe(nonNegativeSeconds(duration))
}
func (m *Metrics) GameAborted() {
	if m.Enabled() {
		m.gamesCurrent.Dec()
	}
}

func (m *Metrics) SetMatchQueueCurrent(value int) {
	if m.Enabled() {
		m.matchQueueCurrent.Set(float64(max(value, 0)))
	}
}
func (m *Metrics) ObserveMatchWait(duration time.Duration) {
	if m.Enabled() {
		m.matchWait.Observe(nonNegativeSeconds(duration))
	}
}
func (m *Metrics) MatchCancelled(reason string) {
	if m.Enabled() {
		m.matchCancelled.WithLabelValues(bounded(reason, matchReasons)).Inc()
	}
}
func (m *Metrics) MatchRollback(stage string) {
	if m.Enabled() {
		m.matchRollbacks.WithLabelValues(bounded(stage, rollbackStages)).Inc()
	}
}

func (m *Metrics) ObserveCommand(commandType, result string, duration time.Duration) {
	if !m.Enabled() {
		return
	}
	commandType = bounded(commandType, commandTypes)
	result = bounded(result, commandResults)
	m.commands.WithLabelValues(commandType, result).Inc()
	m.commandLatency.WithLabelValues(commandType).Observe(nonNegativeSeconds(duration))
}
func (m *Metrics) ProtocolError(reason string) {
	if m.Enabled() {
		m.protocolErrors.WithLabelValues(bounded(reason, protocolErrorReasons)).Inc()
	}
}
func (m *Metrics) IdempotencyHit() {
	if m.Enabled() {
		m.idempotencyHits.Inc()
	}
}
func (m *Metrics) IdempotencyConflict() {
	if m.Enabled() {
		m.idempotencyConflicts.Inc()
	}
}

func (m *Metrics) ObserveRedis(operation string, duration time.Duration, failed bool) {
	if !m.Enabled() {
		return
	}
	operation = bounded(strings.ToLower(operation), redisOperations)
	m.redisLatency.WithLabelValues(operation).Observe(nonNegativeSeconds(duration))
	if failed {
		m.redisErrors.WithLabelValues(operation).Inc()
	}
}
func (m *Metrics) SetReady(ready bool) {
	if !m.Enabled() {
		return
	}
	if ready {
		m.readiness.Set(1)
	} else {
		m.readiness.Set(0)
	}
}

func (m *Metrics) ObserveBot(engine string, duration time.Duration, timedOut bool) {
	if !m.Enabled() {
		return
	}
	engine = bounded(engine, botEngines)
	m.botDuration.WithLabelValues(engine).Observe(nonNegativeSeconds(duration))
	if timedOut {
		m.botTimeouts.WithLabelValues(engine).Inc()
	}
}
func (m *Metrics) BotTimeout(engine string) {
	if m.Enabled() {
		m.botTimeouts.WithLabelValues(bounded(engine, botEngines)).Inc()
	}
}
func (m *Metrics) BotFallback(from, to string) {
	if !m.Enabled() {
		return
	}
	m.botFallbacks.WithLabelValues(bounded(from, botEngines), bounded(to, botEngines)).Inc()
}

func nonNegativeSeconds(duration time.Duration) float64 {
	if duration < 0 {
		return 0
	}
	return duration.Seconds()
}

func bounded(value string, allowed map[string]struct{}) string {
	if _, ok := allowed[value]; ok {
		return value
	}
	return "other"
}

func set(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values)+1)
	for _, value := range values {
		result[value] = struct{}{}
	}
	result["other"] = struct{}{}
	return result
}

var (
	commandTypes         = set("reconnect", "ping", "create_room", "join_room", "leave_room", "quick_match", "practice_match", "cancel_match", "ready", "cancel_ready", "bid", "play_cards", "pass", "get_stats", "get_leaderboard", "get_room_list", "get_online_count", "get_maintenance_status", "chat")
	commandResults       = set("ok", "error", "invalid", "rate_limited", "unavailable", "cache_hit", "conflict")
	protocolErrorReasons = set("non_binary_frame", "decode", "invalid_command", "invalid_request_id", "handshake")
	reconnectReasons     = set("decode", "already_bound", "invalid", "expired", "rebind", "delivery", "snapshot_skipped", "ticket", "superseded", "authority_race")
	matchReasons         = set("cancel"+"led", "timeout", "disconnected", "connection_replaced", "delivery_failed", "shutdown", "room_removed", "participant_unavailable", "assembly_failed")
	rollbackStages       = set("validate", "begin", "bind", "join", "commit", "start", "publish", "persist", "cancel")
	redisOperations      = set("dial", "ping", "get", "set", "del", "hgetall", "hset", "expire", "zadd", "zscore", "zrank", "zrevrank", "zrange", "zrevrange", "pipeline")
	botEngines           = set("heuristic", "douzero")
)
