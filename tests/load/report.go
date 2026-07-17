package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type latencySummary struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
}

type rateThresholdCheck struct {
	name string
	got  float64
	want float64
}

type reportConfig struct {
	URL                string `json:"url"`
	MetricsURL         string `json:"metrics_url,omitempty"`
	Connections        int    `json:"connections"`
	ConnectConcurrency int    `json:"connect_concurrency"`
	IdleDuration       string `json:"idle_duration"`
	Reconnects         int    `json:"reconnects"`
	RoomOperations     int    `json:"room_operations"`
	MatchOperations    int    `json:"match_operations"`
	MatchTimeouts      int    `json:"match_timeouts"`
}

type loadReport struct {
	SchemaVersion int          `json:"schema_version"`
	Status        string       `json:"status"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at"`
	DurationMS    int64        `json:"duration_ms"`
	Config        reportConfig `json:"config"`

	ConnectionsAttempted  int     `json:"connections_attempted"`
	ConnectionsSuccessful int     `json:"connections_successful"`
	ConnectionsRejected   int     `json:"connections_rejected"`
	ConnectionSuccessRate float64 `json:"connection_success_rate"`
	IdleChecksAttempted   int     `json:"idle_checks_attempted"`
	IdleChecksSuccessful  int     `json:"idle_checks_successful"`
	IdleSuccessRate       float64 `json:"idle_success_rate"`

	ReconnectsAttempted  int     `json:"reconnects_attempted"`
	ReconnectsSuccessful int     `json:"reconnects_successful"`
	ReconnectSuccessRate float64 `json:"reconnect_success_rate"`

	RoomScenariosAttempted    int     `json:"room_scenarios_attempted"`
	RoomScenariosSuccessful   int     `json:"room_scenarios_successful"`
	RoomSuccessRate           float64 `json:"room_success_rate"`
	RoomsCreated              int     `json:"rooms_created"`
	RoomsJoined               int     `json:"rooms_joined"`
	RoomsLeft                 int     `json:"rooms_left"`
	MatchOperationsAttempted  int     `json:"match_operations_attempted"`
	MatchOperationsSuccessful int     `json:"match_operations_successful"`
	MatchTimeoutsAttempted    int     `json:"match_timeouts_attempted"`
	MatchTimeoutsObserved     int     `json:"match_timeouts_observed"`
	MatchSuccessRate          float64 `json:"match_success_rate"`
	GamesStarted              int64   `json:"games_started"`
	GamesFinished             int64   `json:"games_finished"`

	LatencyMS           latencySummary `json:"latency_ms"`
	ConnectionLatencyMS latencySummary `json:"connection_latency_ms"`
	ReconnectLatencyMS  latencySummary `json:"reconnect_latency_ms"`
	RoomLatencyMS       latencySummary `json:"room_latency_ms"`
	MatchLatencyMS      latencySummary `json:"match_latency_ms"`
	IdlePingLatencyMS   latencySummary `json:"idle_ping_latency_ms"`

	PeakRSSBytes              *uint64 `json:"peak_rss_bytes"`
	PeakGoroutines            *int    `json:"peak_goroutines"`
	BaselineGoroutines        *int    `json:"baseline_goroutines"`
	FinalGoroutines           *int    `json:"final_goroutines"`
	RedisErrorCount           *int64  `json:"redis_error_count"`
	SlowClientDisconnectCount *int64  `json:"slow_client_disconnect_count"`
	ServerCrashCount          *int    `json:"server_crash_count"`
	BaselineConnections       *int64  `json:"baseline_connections"`
	FinalConnections          *int64  `json:"final_connections"`
	TelemetrySamples          int     `json:"telemetry_samples"`
	TelemetryErrors           int     `json:"telemetry_errors"`

	LoadGeneratorPeakHeapBytes   uint64 `json:"load_generator_peak_heap_bytes"`
	LoadGeneratorPeakGoroutines  int    `json:"load_generator_peak_goroutines"`
	LoadGeneratorFinalHeapBytes  uint64 `json:"load_generator_final_heap_bytes"`
	LoadGeneratorFinalGoroutines int    `json:"load_generator_final_goroutines"`

	Thresholds        thresholds `json:"thresholds"`
	ThresholdFailures []string   `json:"threshold_failures"`
	Errors            []string   `json:"errors"`
	Warnings          []string   `json:"warnings"`
	Limitations       []string   `json:"limitations"`
}

func summarizeLatency(values []float64) latencySummary {
	if len(values) == 0 {
		return latencySummary{}
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	return latencySummary{
		Count: len(ordered),
		P50:   percentile(ordered, 0.50),
		P95:   percentile(ordered, 0.95),
		P99:   percentile(ordered, 0.99),
		Max:   ordered[len(ordered)-1],
	}
}

func percentile(ordered []float64, quantile float64) float64 {
	if len(ordered) == 0 {
		return 0
	}
	index := int(math.Ceil(quantile*float64(len(ordered)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}

func successRate(successful, attempted int) float64 {
	if attempted == 0 {
		return 1
	}
	return float64(successful) / float64(attempted)
}

func (r *loadReport) evaluateThresholds() {
	for _, check := range r.rateThresholdChecks() {
		if check.got < check.want {
			r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("%s %.4f is below %.4f", check.name, check.got, check.want))
		}
	}
	r.evaluateLatencyThreshold()
	r.evaluateServerRSS()
	r.evaluateServerGoroutines()
	r.evaluateFinalGoroutineDelta()
	r.evaluateRedisErrors()
	r.evaluateFinalConnectionDelta()

	if len(r.ThresholdFailures) == 0 {
		r.Status = "passed"
	} else {
		r.Status = "failed"
	}
}

func (r *loadReport) rateThresholdChecks() []rateThresholdCheck {
	checks := []rateThresholdCheck{
		{"connection success rate", r.ConnectionSuccessRate, r.Thresholds.MinConnectionSuccessRate},
		{"idle success rate", r.IdleSuccessRate, r.Thresholds.MinIdleSuccessRate},
	}
	if r.ReconnectsAttempted > 0 {
		checks = append(checks, rateThresholdCheck{"reconnect success rate", r.ReconnectSuccessRate, r.Thresholds.MinReconnectSuccessRate})
	}
	if r.RoomScenariosAttempted > 0 {
		checks = append(checks, rateThresholdCheck{"room success rate", r.RoomSuccessRate, r.Thresholds.MinRoomSuccessRate})
	}
	matchAttempted := r.MatchOperationsAttempted + r.MatchTimeoutsAttempted
	if matchAttempted > 0 {
		checks = append(checks, rateThresholdCheck{"match success rate", r.MatchSuccessRate, r.Thresholds.MinMatchSuccessRate})
	}
	return checks
}

func (r *loadReport) evaluateLatencyThreshold() {
	if r.Thresholds.MaxP99LatencyMS <= 0 {
		return
	}
	if r.LatencyMS.Count == 0 {
		r.ThresholdFailures = append(r.ThresholdFailures, "p99 latency threshold configured but no latency samples were recorded")
	} else if r.LatencyMS.P99 > r.Thresholds.MaxP99LatencyMS {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("p99 latency %.2fms exceeds %.2fms", r.LatencyMS.P99, r.Thresholds.MaxP99LatencyMS))
	}
}

func (r *loadReport) evaluateServerRSS() {
	if r.Thresholds.MaxServerRSSBytes <= 0 {
		return
	}
	if r.PeakRSSBytes == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "server RSS threshold configured but telemetry is unavailable")
	} else if *r.PeakRSSBytes > r.Thresholds.MaxServerRSSBytes {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("server RSS %d exceeds %d bytes", *r.PeakRSSBytes, r.Thresholds.MaxServerRSSBytes))
	}
}

func (r *loadReport) evaluateServerGoroutines() {
	if r.Thresholds.MaxServerGoroutines <= 0 {
		return
	}
	if r.PeakGoroutines == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "server goroutine threshold configured but telemetry is unavailable")
	} else if *r.PeakGoroutines > r.Thresholds.MaxServerGoroutines {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("server goroutines %d exceeds %d", *r.PeakGoroutines, r.Thresholds.MaxServerGoroutines))
	}
}

func (r *loadReport) evaluateFinalGoroutineDelta() {
	if r.Thresholds.MaxFinalGoroutinesDelta < 0 {
		return
	}
	if r.BaselineGoroutines == nil || r.FinalGoroutines == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "final goroutine threshold configured but telemetry is unavailable")
		return
	}
	delta := *r.FinalGoroutines - *r.BaselineGoroutines
	if delta > r.Thresholds.MaxFinalGoroutinesDelta {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("final goroutine delta %d exceeds %d", delta, r.Thresholds.MaxFinalGoroutinesDelta))
	}
}

func (r *loadReport) evaluateRedisErrors() {
	if r.Thresholds.MaxRedisErrors < 0 {
		return
	}
	if r.RedisErrorCount == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "Redis error threshold configured but telemetry is unavailable")
	} else if *r.RedisErrorCount > r.Thresholds.MaxRedisErrors {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("Redis error delta %d exceeds %d", *r.RedisErrorCount, r.Thresholds.MaxRedisErrors))
	}
}

func (r *loadReport) evaluateFinalConnectionDelta() {
	if r.Thresholds.MaxFinalConnectionsDelta < 0 {
		return
	}
	if r.BaselineConnections == nil || r.FinalConnections == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "connection cleanup threshold configured but telemetry is unavailable")
		return
	}
	delta := *r.FinalConnections - *r.BaselineConnections
	if delta > r.Thresholds.MaxFinalConnectionsDelta {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("final connection delta %d exceeds %d", delta, r.Thresholds.MaxFinalConnectionsDelta))
	}
}

func writeReports(report loadReport, jsonPath, markdownPath string) error {
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	jsonData = append(jsonData, '\n')
	if err := writeReportFile(jsonPath, jsonData); err != nil {
		return err
	}
	if err := writeReportFile(markdownPath, []byte(renderMarkdown(report))); err != nil {
		return err
	}
	return nil
}

func writeReportFile(path string, data []byte) error {
	cleanPath := filepath.Clean(path)
	directory := filepath.Dir(cleanPath)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.WriteFile(filepath.Base(cleanPath), data, 0o600)
}

func renderMarkdown(report loadReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Load Test Report\n\n")
	fmt.Fprintf(&out, "- Status: **%s**\n", report.Status)
	fmt.Fprintf(&out, "- Target: `%s`\n", report.Config.URL)
	fmt.Fprintf(&out, "- Started: `%s`\n", report.StartedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&out, "- Duration: `%d ms`\n\n", report.DurationMS)

	out.WriteString("## Results\n\n")
	out.WriteString("| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(&out, "| Connections | %d / %d (%.2f%%) |\n", report.ConnectionsSuccessful, report.ConnectionsAttempted, report.ConnectionSuccessRate*100)
	fmt.Fprintf(&out, "| Idle checks | %d / %d (%.2f%%) |\n", report.IdleChecksSuccessful, report.IdleChecksAttempted, report.IdleSuccessRate*100)
	fmt.Fprintf(&out, "| Reconnects | %d / %d (%.2f%%) |\n", report.ReconnectsSuccessful, report.ReconnectsAttempted, report.ReconnectSuccessRate*100)
	fmt.Fprintf(&out, "| Room scenarios | %d / %d (%.2f%%) |\n", report.RoomScenariosSuccessful, report.RoomScenariosAttempted, report.RoomSuccessRate*100)
	fmt.Fprintf(&out, "| Rooms created / joined / left | %d / %d / %d |\n", report.RoomsCreated, report.RoomsJoined, report.RoomsLeft)
	fmt.Fprintf(&out, "| Match enqueue/cancel | %d / %d |\n", report.MatchOperationsSuccessful, report.MatchOperationsAttempted)
	fmt.Fprintf(&out, "| Match timeouts observed | %d / %d |\n", report.MatchTimeoutsObserved, report.MatchTimeoutsAttempted)
	fmt.Fprintf(&out, "| Games started / finished | %d / %d |\n", report.GamesStarted, report.GamesFinished)
	fmt.Fprintf(&out, "| Peak server RSS | %s |\n", optionalUint(report.PeakRSSBytes))
	fmt.Fprintf(&out, "| Peak server goroutines | %s |\n", optionalInt(report.PeakGoroutines))
	fmt.Fprintf(&out, "| Server goroutines baseline / final | %s / %s |\n", optionalInt(report.BaselineGoroutines), optionalInt(report.FinalGoroutines))
	fmt.Fprintf(&out, "| Redis error delta | %s |\n", optionalInt64(report.RedisErrorCount))
	fmt.Fprintf(&out, "| Slow-client disconnect delta | %s |\n\n", optionalInt64(report.SlowClientDisconnectCount))

	out.WriteString("## Latency (ms)\n\n")
	out.WriteString("| Operation | Samples | p50 | p95 | p99 | max |\n| --- | ---: | ---: | ---: | ---: | ---: |\n")
	writeLatencyRow(&out, "All", report.LatencyMS)
	writeLatencyRow(&out, "Connect", report.ConnectionLatencyMS)
	writeLatencyRow(&out, "Reconnect", report.ReconnectLatencyMS)
	writeLatencyRow(&out, "Room commands", report.RoomLatencyMS)
	writeLatencyRow(&out, "Match commands/timeouts", report.MatchLatencyMS)
	writeLatencyRow(&out, "Post-idle ping", report.IdlePingLatencyMS)

	writeMarkdownList(&out, "Threshold failures", report.ThresholdFailures)
	writeMarkdownList(&out, "Errors", report.Errors)
	writeMarkdownList(&out, "Warnings", report.Warnings)
	writeMarkdownList(&out, "Limitations", report.Limitations)
	return out.String()
}

func writeLatencyRow(out *strings.Builder, name string, summary latencySummary) {
	fmt.Fprintf(out, "| %s | %d | %.2f | %.2f | %.2f | %.2f |\n", name, summary.Count, summary.P50, summary.P95, summary.P99, summary.Max)
}

func writeMarkdownList(out *strings.Builder, heading string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(out, "\n## %s\n\n", heading)
	for _, value := range values {
		value = strings.ReplaceAll(value, "\n", " ")
		fmt.Fprintf(out, "- %s\n", value)
	}
}

func optionalUint(value *uint64) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d bytes", *value)
}

func optionalInt(value *int) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d", *value)
}

func optionalInt64(value *int64) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d", *value)
}
