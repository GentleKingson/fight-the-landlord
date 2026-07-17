package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	metricConnectionsCurrent = "fight_landlord_websocket_connections_current"
	metricRoomsCurrent       = "fight_landlord_rooms_current"
	metricRedisErrors        = "fight_landlord_redis_errors_total"
	metricGamesStarted       = "fight_landlord_games_started_total"
	metricGamesFinished      = "fight_landlord_games_finished_total"
	metricProcessRSS         = "process_resident_memory_bytes"
	metricGoroutines         = "go_goroutines"
)

type resourcePoint struct {
	at         time.Time
	rss        float64
	goroutines float64
}

type telemetrySnapshot struct {
	InitialRSSBytes     *uint64
	PeakRSSBytes        *uint64
	FinalRSSBytes       *uint64
	InitialGoroutines   *int
	PeakGoroutines      *int
	FinalGoroutines     *int
	RedisErrors         *int64
	InitialConnections  *int64
	FinalConnections    *int64
	InitialRooms        *int64
	FinalRooms          *int64
	MetricGamesStarted  *int64
	MetricGamesFinished *int64
	MemoryTrendAssessed bool
	LinearMemoryGrowth  *bool
	MemorySlopeBytesMin *float64
	MemoryRegressionR2  *float64
	Samples             int
	CollectionErrors    int
	Warning             string
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
	points   []resourcePoint
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
		return telemetrySnapshot{CollectionErrors: m.errors, Warning: warning}
	}

	initialRSS := uint64(maxFloat(m.baseline[metricProcessRSS], 0))
	peakRSS := uint64(maxFloat(m.peakRSS, 0))
	finalRSS := uint64(maxFloat(m.latest[metricProcessRSS], 0))
	initialGo := int(maxFloat(m.baseline[metricGoroutines], 0))
	peakGo := int(maxFloat(m.peakGo, 0))
	finalGo := int(maxFloat(m.latest[metricGoroutines], 0))
	redisErrors := nonNegativeDelta(m.latest[metricRedisErrors], m.baseline[metricRedisErrors])
	initialConnections := int64(m.baseline[metricConnectionsCurrent])
	finalConnections := int64(m.latest[metricConnectionsCurrent])
	initialRooms := int64(m.baseline[metricRoomsCurrent])
	finalRooms := int64(m.latest[metricRoomsCurrent])
	gamesStarted := nonNegativeDelta(m.latest[metricGamesStarted], m.baseline[metricGamesStarted])
	gamesFinished := nonNegativeDelta(m.latest[metricGamesFinished], m.baseline[metricGamesFinished])
	assessed, linear, slope, r2 := detectLinearMemoryGrowth(m.points)

	return telemetrySnapshot{
		InitialRSSBytes:     &initialRSS,
		PeakRSSBytes:        &peakRSS,
		FinalRSSBytes:       &finalRSS,
		InitialGoroutines:   &initialGo,
		PeakGoroutines:      &peakGo,
		FinalGoroutines:     &finalGo,
		RedisErrors:         &redisErrors,
		InitialConnections:  &initialConnections,
		FinalConnections:    &finalConnections,
		InitialRooms:        &initialRooms,
		FinalRooms:          &finalRooms,
		MetricGamesStarted:  &gamesStarted,
		MetricGamesFinished: &gamesFinished,
		MemoryTrendAssessed: assessed,
		LinearMemoryGrowth:  &linear,
		MemorySlopeBytesMin: &slope,
		MemoryRegressionR2:  &r2,
		Samples:             len(m.points),
		CollectionErrors:    m.errors,
		Warning:             m.warning,
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
	for _, required := range []string{
		metricProcessRSS,
		metricGoroutines,
		metricConnectionsCurrent,
		metricRoomsCurrent,
		metricRedisErrors,
		metricGamesStarted,
		metricGamesFinished,
	} {
		if _, ok := values[required]; !ok {
			m.recordError(fmt.Errorf("metrics endpoint omitted %s", required))
			return
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		m.started = true
		m.baseline = cloneMetrics(values)
	}
	m.latest = cloneMetrics(values)
	m.peakRSS = maxFloat(m.peakRSS, values[metricProcessRSS])
	m.peakGo = maxFloat(m.peakGo, values[metricGoroutines])
	m.points = append(m.points, resourcePoint{
		at:         time.Now(),
		rss:        values[metricProcessRSS],
		goroutines: values[metricGoroutines],
	})
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

func detectLinearMemoryGrowth(points []resourcePoint) (bool, bool, float64, float64) {
	// Ignore initial allocation churn and require enough tail samples to make a
	// linear trend meaningful. The absolute guards avoid flagging allocator
	// noise as a leak during the short CI override.
	if len(points) < 6 {
		return false, false, 0, 0
	}
	tail := points[len(points)/4:]
	if tail[len(tail)-1].at.Sub(tail[0].at) < 5*time.Minute {
		return false, false, 0, 0
	}
	origin := tail[0].at
	var sumX, sumY, sumXX, sumXY float64
	for _, point := range tail {
		x := point.at.Sub(origin).Minutes()
		sumX += x
		sumY += point.rss
		sumXX += x * x
		sumXY += x * point.rss
	}
	n := float64(len(tail))
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return false, false, 0, 0
	}
	slope := (n*sumXY - sumX*sumY) / denominator
	meanY := sumY / n
	intercept := (sumY - slope*sumX) / n
	var residual, total float64
	for _, point := range tail {
		x := point.at.Sub(origin).Minutes()
		predicted := intercept + slope*x
		residual += (point.rss - predicted) * (point.rss - predicted)
		total += (point.rss - meanY) * (point.rss - meanY)
	}
	r2 := 0.0
	if total > 0 {
		r2 = 1 - residual/total
	}
	growth := tail[len(tail)-1].rss - tail[0].rss
	linear := slope > 1024*1024 && r2 >= 0.80 && growth > 4*1024*1024
	return true, linear, slope, r2
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
