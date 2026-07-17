package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	metricConnectionsCurrent = "fight_landlord_websocket_connections_current"
	metricRedisErrors        = "fight_landlord_redis_errors_total"
	metricSlowClients        = "fight_landlord_slow_client_disconnects_total"
	metricGamesStarted       = "fight_landlord_games_started_total"
	metricGamesFinished      = "fight_landlord_games_finished_total"
	metricProcessRSS         = "process_resident_memory_bytes"
	metricGoRoutines         = "go_goroutines"
)

type telemetrySnapshot struct {
	PeakRSSBytes              *uint64
	PeakGoroutines            *int
	BaselineGoroutines        *int
	FinalGoroutines           *int
	RedisErrorCount           *int64
	SlowClientDisconnectCount *int64
	GamesStarted              *int64
	GamesFinished             *int64
	BaselineConnections       *int64
	FinalConnections          *int64
	Samples                   int
	Errors                    int
	Warning                   string
}

type telemetryMonitor struct {
	url      string
	interval time.Duration
	client   *http.Client
	stop     chan struct{}
	done     chan struct{}

	mu       sync.Mutex
	started  bool
	baseline map[string]float64
	latest   map[string]float64
	peakRSS  float64
	peakGo   float64
	samples  int
	errors   int
	warning  string
}

func startTelemetryMonitor(metricsURL string, interval time.Duration) *telemetryMonitor {
	monitor := &telemetryMonitor{
		url:      metricsURL,
		interval: interval,
		client:   &http.Client{Timeout: 2 * time.Second},
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if metricsURL == "" {
		close(monitor.done)
		return monitor
	}
	monitor.collect(context.Background())
	go monitor.run()
	return monitor
}

func (m *telemetryMonitor) run() {
	defer close(m.done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.collect(context.Background())
		case <-m.stop:
			return
		}
	}
}

func (m *telemetryMonitor) finish() telemetrySnapshot {
	if m.url == "" {
		return telemetrySnapshot{Warning: "server telemetry disabled; server-only fields are null"}
	}
	close(m.stop)
	<-m.done
	m.collect(context.Background())

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		warning := m.warning
		if warning == "" {
			warning = "Prometheus endpoint returned no usable samples"
		}
		return telemetrySnapshot{Errors: m.errors, Warning: warning}
	}

	peakRSS := uint64(maxFloat(m.peakRSS, 0))
	peakGo := int(maxFloat(m.peakGo, 0))
	baselineGoroutines := int(m.baseline[metricGoRoutines])
	finalGoroutines := int(m.latest[metricGoRoutines])
	redisErrors := nonNegativeDelta(m.latest[metricRedisErrors], m.baseline[metricRedisErrors])
	slowClients := nonNegativeDelta(m.latest[metricSlowClients], m.baseline[metricSlowClients])
	gamesStarted := nonNegativeDelta(m.latest[metricGamesStarted], m.baseline[metricGamesStarted])
	gamesFinished := nonNegativeDelta(m.latest[metricGamesFinished], m.baseline[metricGamesFinished])
	baselineConnections := int64(m.baseline[metricConnectionsCurrent])
	finalConnections := int64(m.latest[metricConnectionsCurrent])
	return telemetrySnapshot{
		PeakRSSBytes:              &peakRSS,
		PeakGoroutines:            &peakGo,
		BaselineGoroutines:        &baselineGoroutines,
		FinalGoroutines:           &finalGoroutines,
		RedisErrorCount:           &redisErrors,
		SlowClientDisconnectCount: &slowClients,
		GamesStarted:              &gamesStarted,
		GamesFinished:             &gamesFinished,
		BaselineConnections:       &baselineConnections,
		FinalConnections:          &finalConnections,
		Samples:                   m.samples,
		Errors:                    m.errors,
		Warning:                   m.warning,
	}
}

func (m *telemetryMonitor) collect(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url, http.NoBody)
	if err != nil {
		m.recordError(err)
		return
	}
	response, err := m.client.Do(request)
	if err != nil {
		m.recordError(err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		m.recordError(fmt.Errorf("metrics endpoint returned HTTP %d", response.StatusCode))
		return
	}
	values, err := parsePrometheus(response.Body)
	if err != nil {
		m.recordError(err)
		return
	}
	if _, ok := values[metricProcessRSS]; !ok {
		m.recordError(fmt.Errorf("metrics endpoint omitted %s", metricProcessRSS))
		return
	}
	if _, ok := values[metricGoRoutines]; !ok {
		m.recordError(fmt.Errorf("metrics endpoint omitted %s", metricGoRoutines))
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		m.started = true
		m.baseline = cloneMetrics(values)
	}
	m.latest = cloneMetrics(values)
	m.peakRSS = maxFloat(m.peakRSS, values[metricProcessRSS])
	m.peakGo = maxFloat(m.peakGo, values[metricGoRoutines])
	m.samples++
}

func (m *telemetryMonitor) recordError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors++
	if m.warning == "" {
		m.warning = err.Error()
	}
}

func parsePrometheus(reader io.Reader) (map[string]float64, error) {
	values := make(map[string]float64)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// The optional third field is a timestamp; the metric value is always
		// the second field in the Prometheus text exposition format.
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		name := fields[0]
		if brace := strings.IndexByte(name, '{'); brace >= 0 {
			name = name[:brace]
		}
		values[name] += value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func cloneMetrics(source map[string]float64) map[string]float64 {
	clone := make(map[string]float64, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func nonNegativeDelta(latest, baseline float64) int64 {
	delta := int64(latest - baseline)
	if delta < 0 {
		return 0
	}
	return delta
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

type generatorSnapshot struct {
	PeakHeapBytes   uint64
	PeakGoroutines  int
	FinalHeapBytes  uint64
	FinalGoroutines int
}

type generatorMonitor struct {
	stop  chan struct{}
	done  chan struct{}
	mu    sync.Mutex
	peak  uint64
	goMax int
}

func startGeneratorMonitor() *generatorMonitor {
	monitor := &generatorMonitor{stop: make(chan struct{}), done: make(chan struct{})}
	go monitor.run()
	return monitor
}

func (m *generatorMonitor) run() {
	defer close(m.done)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.sample()
		select {
		case <-ticker.C:
		case <-m.stop:
			return
		}
	}
}

func (m *generatorMonitor) sample() {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	goroutines := runtime.NumGoroutine()
	m.mu.Lock()
	if memory.HeapAlloc > m.peak {
		m.peak = memory.HeapAlloc
	}
	if goroutines > m.goMax {
		m.goMax = goroutines
	}
	m.mu.Unlock()
}

func (m *generatorMonitor) finish() generatorSnapshot {
	close(m.stop)
	<-m.done
	m.sample()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	m.mu.Lock()
	defer m.mu.Unlock()
	return generatorSnapshot{
		PeakHeapBytes:   m.peak,
		PeakGoroutines:  m.goMax,
		FinalHeapBytes:  memory.HeapAlloc,
		FinalGoroutines: runtime.NumGoroutine(),
	}
}
