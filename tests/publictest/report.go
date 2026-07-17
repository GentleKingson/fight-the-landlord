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
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
}

type enforcedThresholds struct {
	MinCompleteGameSuccessRate float64 `json:"min_complete_game_success_rate"`
	MinReconnectSuccessRate    float64 `json:"min_reconnect_success_rate"`
	MaxDuplicateSettlements    int     `json:"max_duplicate_settlements"`
	MaxRedisErrors             int64   `json:"max_redis_errors"`
	MaxRemainingConnections    int64   `json:"max_remaining_connections"`
	MaxRemainingRooms          int64   `json:"max_remaining_rooms"`
	MaxFinalGoroutineGrowth    int     `json:"max_final_goroutine_growth"`
	AllowLinearMemoryGrowth    bool    `json:"allow_linear_memory_growth"`
}

var publicTestThresholds = enforcedThresholds{
	MinCompleteGameSuccessRate: 0.99,
	MinReconnectSuccessRate:    0.99,
	MaxDuplicateSettlements:    0,
	MaxRedisErrors:             0,
	MaxRemainingConnections:    0,
	MaxRemainingRooms:          0,
	MaxFinalGoroutineGrowth:    10,
	AllowLinearMemoryGrowth:    false,
}

type reportConfiguration struct {
	Preset         string  `json:"preset"`
	URL            string  `json:"url"`
	MetricsURL     string  `json:"metrics_url"`
	Duration       string  `json:"duration"`
	Players        int     `json:"players"`
	DisconnectRate float64 `json:"disconnect_rate"`
	DouZero        bool    `json:"douzero"`
	Seed           int64   `json:"seed"`
}

type publicTestReport struct {
	SchemaVersion int                 `json:"schema_version"`
	Status        string              `json:"status"`
	StartedAt     time.Time           `json:"started_at"`
	FinishedAt    time.Time           `json:"finished_at"`
	ElapsedMS     int64               `json:"elapsed_ms"`
	Configuration reportConfiguration `json:"configuration"`

	ConnectionsAttempted    int     `json:"connections_attempted"`
	ConnectionsSuccessful   int     `json:"connections_successful"`
	GamesStarted            int     `json:"games_started"`
	CompletedGames          int     `json:"completed_games"`
	FailedGames             int     `json:"failed_games"`
	CleanCompletedGames     int     `json:"clean_completed_games"`
	CompleteGameSuccessRate float64 `json:"complete_game_success_rate"`

	ReconnectsAttempted  int            `json:"reconnects_attempted"`
	ReconnectsSuccessful int            `json:"reconnects_successful"`
	ReconnectSuccessRate float64        `json:"reconnect_success_rate"`
	Latency              latencySummary `json:"latency"`

	InitialRSSBytes   *uint64 `json:"initial_rss_bytes"`
	PeakRSSBytes      *uint64 `json:"peak_rss_bytes"`
	FinalRSSBytes     *uint64 `json:"final_rss_bytes"`
	InitialGoroutines *int    `json:"initial_goroutines"`
	PeakGoroutines    *int    `json:"peak_goroutines"`
	FinalGoroutines   *int    `json:"final_goroutines"`

	RedisErrors            *int64   `json:"redis_errors"`
	InitialConnections     *int64   `json:"initial_active_connections"`
	RemainingConnections   *int64   `json:"remaining_active_connections"`
	InitialRooms           *int64   `json:"initial_active_rooms"`
	RemainingRooms         *int64   `json:"remaining_active_rooms"`
	ProtocolRoomsRemaining int      `json:"room_list_remaining"`
	DuplicateSettlements   int      `json:"duplicate_settlements"`
	ExpectedPlayerGames    int      `json:"expected_player_total_games"`
	ObservedPlayerGames    int      `json:"observed_player_total_games"`
	TotalGamesReconciled   bool     `json:"total_games_reconciled"`
	LeaderboardType        string   `json:"leaderboard_type"`
	LeaderboardEntries     int      `json:"leaderboard_entries"`
	LeaderboardVerified    bool     `json:"leaderboard_verified"`
	MetricGamesStarted     *int64   `json:"metric_games_started"`
	MetricGamesFinished    *int64   `json:"metric_games_finished"`
	MemoryTrendAssessed    bool     `json:"memory_trend_assessed"`
	LinearMemoryGrowth     *bool    `json:"linear_memory_growth"`
	MemorySlopeBytesMinute *float64 `json:"memory_slope_bytes_per_minute"`
	MemoryRegressionR2     *float64 `json:"memory_regression_r2"`
	TelemetrySamples       int      `json:"telemetry_samples"`
	TelemetryErrors        int      `json:"telemetry_errors"`

	Thresholds        enforcedThresholds `json:"thresholds"`
	ThresholdFailures []string           `json:"threshold_failures"`
	Errors            []string           `json:"errors"`
	Warnings          []string           `json:"warnings"`
}

func buildReport(
	cfg config,
	state *runState,
	rooms []*gameRoom,
	reconciliation reconciliationResult,
	telemetry telemetrySnapshot,
	startedAt, finishedAt time.Time,
) publicTestReport {
	state.mu.Lock()
	errorsCopy := slices.Clone(state.errors)
	latencies := slices.Clone(state.latencies)
	connectionsAttempted := state.connectionsAttempted
	connectionsSuccessful := state.connectionsSuccessful
	reconnectsAttempted := state.reconnectsAttempted
	reconnectsSuccessful := state.reconnectsSuccessful
	duplicates := state.duplicateSettlements
	state.mu.Unlock()

	gamesStarted, completedGames, failedGames := 0, 0, 0
	for _, room := range rooms {
		started, completed, failed, _, _ := room.counts()
		gamesStarted += started
		completedGames += completed
		failedGames += failed
	}
	cleanCompleted := gamesStarted - failedGames
	if cleanCompleted < 0 {
		cleanCompleted = 0
	}
	gameSuccessRate := ratio(cleanCompleted, gamesStarted)
	reconnectSuccessRate := ratio(reconnectsSuccessful, reconnectsAttempted)

	report := publicTestReport{
		SchemaVersion: 1,
		StartedAt:     startedAt.UTC(),
		FinishedAt:    finishedAt.UTC(),
		ElapsedMS:     finishedAt.Sub(startedAt).Milliseconds(),
		Configuration: reportConfiguration{
			Preset: cfg.Preset, URL: cfg.URL, MetricsURL: cfg.MetricsURL,
			Duration: cfg.Duration.String(), Players: cfg.Players,
			DisconnectRate: cfg.DisconnectRate, DouZero: cfg.DouZero, Seed: cfg.Seed,
		},
		ConnectionsAttempted:    connectionsAttempted,
		ConnectionsSuccessful:   connectionsSuccessful,
		GamesStarted:            gamesStarted,
		CompletedGames:          completedGames,
		FailedGames:             failedGames,
		CleanCompletedGames:     cleanCompleted,
		CompleteGameSuccessRate: gameSuccessRate,
		ReconnectsAttempted:     reconnectsAttempted,
		ReconnectsSuccessful:    reconnectsSuccessful,
		ReconnectSuccessRate:    reconnectSuccessRate,
		Latency:                 summarizeLatency(latencies),
		InitialRSSBytes:         telemetry.InitialRSSBytes,
		PeakRSSBytes:            telemetry.PeakRSSBytes,
		FinalRSSBytes:           telemetry.FinalRSSBytes,
		InitialGoroutines:       telemetry.InitialGoroutines,
		PeakGoroutines:          telemetry.PeakGoroutines,
		FinalGoroutines:         telemetry.FinalGoroutines,
		RedisErrors:             telemetry.RedisErrors,
		InitialConnections:      telemetry.InitialConnections,
		RemainingConnections:    telemetry.FinalConnections,
		InitialRooms:            telemetry.InitialRooms,
		RemainingRooms:          telemetry.FinalRooms,
		ProtocolRoomsRemaining:  reconciliation.ProtocolRoomsRemaining,
		DuplicateSettlements:    duplicates,
		ExpectedPlayerGames:     reconciliation.ExpectedPlayerGames,
		ObservedPlayerGames:     reconciliation.ObservedPlayerGames,
		TotalGamesReconciled:    reconciliation.TotalGamesReconciled,
		LeaderboardType:         reconciliation.LeaderboardType,
		LeaderboardEntries:      reconciliation.LeaderboardEntries,
		LeaderboardVerified:     reconciliation.LeaderboardVerified,
		MetricGamesStarted:      telemetry.MetricGamesStarted,
		MetricGamesFinished:     telemetry.MetricGamesFinished,
		MemoryTrendAssessed:     telemetry.MemoryTrendAssessed,
		LinearMemoryGrowth:      telemetry.LinearMemoryGrowth,
		MemorySlopeBytesMinute:  telemetry.MemorySlopeBytesMin,
		MemoryRegressionR2:      telemetry.MemoryRegressionR2,
		TelemetrySamples:        telemetry.Samples,
		TelemetryErrors:         telemetry.CollectionErrors,
		Thresholds:              publicTestThresholds,
		Errors:                  errorsCopy,
	}
	if telemetry.Warning != "" {
		report.Warnings = append(report.Warnings, telemetry.Warning)
	}
	report.evaluateThresholds()
	return report
}

func (r *publicTestReport) evaluateThresholds() {
	r.evaluateGameThresholds()
	r.evaluateReconnectThresholds()
	r.evaluateSettlementAndRedisThresholds()
	r.evaluateCleanupThresholds()
	r.evaluateMemoryThresholds()
	r.evaluateReconciliationThresholds()
	if len(r.ThresholdFailures) == 0 {
		r.Status = "passed"
	} else {
		r.Status = "failed"
	}
}

func (r *publicTestReport) evaluateGameThresholds() {
	if r.GamesStarted == 0 {
		r.ThresholdFailures = append(r.ThresholdFailures, "no complete-game scenarios started")
	} else if r.CompleteGameSuccessRate < r.Thresholds.MinCompleteGameSuccessRate {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf(
			"complete-game success rate %.4f is below %.4f",
			r.CompleteGameSuccessRate,
			r.Thresholds.MinCompleteGameSuccessRate,
		))
	}
}

func (r *publicTestReport) evaluateReconnectThresholds() {
	if r.Configuration.DisconnectRate > 0 && r.ReconnectsAttempted == 0 {
		r.ThresholdFailures = append(r.ThresholdFailures, "disconnect-rate was non-zero but no reconnect was attempted")
	} else if r.ReconnectsAttempted > 0 && r.ReconnectSuccessRate < r.Thresholds.MinReconnectSuccessRate {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf(
			"reconnect success rate %.4f is below %.4f",
			r.ReconnectSuccessRate,
			r.Thresholds.MinReconnectSuccessRate,
		))
	}
}

func (r *publicTestReport) evaluateSettlementAndRedisThresholds() {
	if r.DuplicateSettlements > r.Thresholds.MaxDuplicateSettlements {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("duplicate settlements %d exceeds %d", r.DuplicateSettlements, r.Thresholds.MaxDuplicateSettlements))
	}
	if r.RedisErrors == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "Redis error telemetry is unavailable")
	} else if *r.RedisErrors > r.Thresholds.MaxRedisErrors {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("Redis error delta %d exceeds %d", *r.RedisErrors, r.Thresholds.MaxRedisErrors))
	}
}

func (r *publicTestReport) evaluateCleanupThresholds() {
	if r.RemainingConnections == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "final connection telemetry is unavailable")
	} else if *r.RemainingConnections > r.Thresholds.MaxRemainingConnections {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("remaining connections %d exceeds %d", *r.RemainingConnections, r.Thresholds.MaxRemainingConnections))
	}
	if r.RemainingRooms == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "final room telemetry is unavailable")
	} else if *r.RemainingRooms > r.Thresholds.MaxRemainingRooms {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("remaining rooms %d exceeds %d", *r.RemainingRooms, r.Thresholds.MaxRemainingRooms))
	}
	if r.ProtocolRoomsRemaining != 0 {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("room-list cleanup returned %d rooms", r.ProtocolRoomsRemaining))
	}
	if r.InitialGoroutines == nil || r.FinalGoroutines == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "goroutine telemetry is unavailable")
	} else if growth := *r.FinalGoroutines - *r.InitialGoroutines; growth > r.Thresholds.MaxFinalGoroutineGrowth {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("final goroutine growth %d exceeds %d", growth, r.Thresholds.MaxFinalGoroutineGrowth))
	}
}

func (r *publicTestReport) evaluateMemoryThresholds() {
	duration, durationErr := time.ParseDuration(r.Configuration.Duration)
	switch {
	case durationErr == nil && duration >= 10*time.Minute && !r.MemoryTrendAssessed:
		r.ThresholdFailures = append(r.ThresholdFailures, "memory trend could not be assessed over a five-minute tail")
	case r.LinearMemoryGrowth == nil:
		r.ThresholdFailures = append(r.ThresholdFailures, "memory trend telemetry is unavailable")
	case r.MemoryTrendAssessed && *r.LinearMemoryGrowth && !r.Thresholds.AllowLinearMemoryGrowth:
		r.ThresholdFailures = append(r.ThresholdFailures, "RSS samples show sustained linear memory growth")
	}
}

func (r *publicTestReport) evaluateReconciliationThresholds() {
	if !r.TotalGamesReconciled {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("player total-games mismatch: observed %d, expected %d", r.ObservedPlayerGames, r.ExpectedPlayerGames))
	}
	if !r.LeaderboardVerified {
		r.ThresholdFailures = append(r.ThresholdFailures, "total leaderboard did not match player stats")
	}
	if r.MetricGamesStarted == nil || r.MetricGamesFinished == nil {
		r.ThresholdFailures = append(r.ThresholdFailures, "game lifecycle telemetry is unavailable")
	} else if *r.MetricGamesStarted != int64(r.GamesStarted) || *r.MetricGamesFinished != int64(r.CompletedGames) {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf(
			"game metrics mismatch: started %d/%d, finished %d/%d",
			*r.MetricGamesStarted, r.GamesStarted, *r.MetricGamesFinished, r.CompletedGames,
		))
	}
	if r.TelemetryErrors > 0 {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("Prometheus collection errors: %d", r.TelemetryErrors))
	}
	if len(r.Errors) > 0 {
		r.ThresholdFailures = append(r.ThresholdFailures, fmt.Sprintf("harness errors: %d", len(r.Errors)))
	}
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

func ratio(successful, attempted int) float64 {
	if attempted == 0 {
		return 0
	}
	return float64(successful) / float64(attempted)
}

func writeReports(report publicTestReport, jsonPath, markdownPath string) error {
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	jsonData = append(jsonData, '\n')
	if err := writeReportFile(jsonPath, jsonData); err != nil {
		return err
	}
	return writeReportFile(markdownPath, []byte(renderMarkdown(report)))
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

func renderMarkdown(report publicTestReport) string {
	var out strings.Builder
	out.WriteString("# Public Test Complete-Game Smoke\n\n")
	fmt.Fprintf(&out, "- Status: **%s**\n", report.Status)
	fmt.Fprintf(&out, "- Preset: `%s` (`%s`)\n", report.Configuration.Preset, report.Configuration.Duration)
	fmt.Fprintf(&out, "- Players: `%d`\n", report.Configuration.Players)
	fmt.Fprintf(&out, "- Target: `%s`\n", report.Configuration.URL)
	fmt.Fprintf(&out, "- Started: `%s`\n\n", report.StartedAt.Format(time.RFC3339))

	out.WriteString("## Results\n\n")
	out.WriteString("| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(&out, "| Connections | %d / %d |\n", report.ConnectionsSuccessful, report.ConnectionsAttempted)
	fmt.Fprintf(&out, "| Clean completed games | %d / %d (%.2f%%) |\n", report.CleanCompletedGames, report.GamesStarted, report.CompleteGameSuccessRate*100)
	fmt.Fprintf(&out, "| Terminal game completions | %d |\n", report.CompletedGames)
	fmt.Fprintf(&out, "| Failed games | %d |\n", report.FailedGames)
	fmt.Fprintf(&out, "| Reconnects | %d / %d (%.2f%%) |\n", report.ReconnectsSuccessful, report.ReconnectsAttempted, report.ReconnectSuccessRate*100)
	fmt.Fprintf(&out, "| Latency p50 / p95 / p99 | %.2f / %.2f / %.2f ms |\n", report.Latency.P50, report.Latency.P95, report.Latency.P99)
	fmt.Fprintf(&out, "| RSS initial / peak / final | %s / %s / %s |\n", optionalUint(report.InitialRSSBytes), optionalUint(report.PeakRSSBytes), optionalUint(report.FinalRSSBytes))
	fmt.Fprintf(&out, "| Goroutines initial / peak / final | %s / %s / %s |\n", optionalInt(report.InitialGoroutines), optionalInt(report.PeakGoroutines), optionalInt(report.FinalGoroutines))
	fmt.Fprintf(&out, "| Redis errors | %s |\n", optionalInt64(report.RedisErrors))
	fmt.Fprintf(&out, "| Remaining connections / rooms | %s / %s |\n", optionalInt64(report.RemainingConnections), optionalInt64(report.RemainingRooms))
	fmt.Fprintf(&out, "| Duplicate settlements | %d |\n", report.DuplicateSettlements)
	fmt.Fprintf(&out, "| Total games reconciled | %t (%d / %d player games) |\n", report.TotalGamesReconciled, report.ObservedPlayerGames, report.ExpectedPlayerGames)
	fmt.Fprintf(&out, "| Leaderboard verified | %t (`%s`, %d entries) |\n", report.LeaderboardVerified, report.LeaderboardType, report.LeaderboardEntries)
	fmt.Fprintf(&out, "| Sustained linear memory growth | %s |\n", optionalBool(report.LinearMemoryGrowth))
	fmt.Fprintf(&out, "| Memory trend assessed | %t |\n", report.MemoryTrendAssessed)

	writeMarkdownList(&out, "Threshold Failures", report.ThresholdFailures)
	writeMarkdownList(&out, "Errors", report.Errors)
	writeMarkdownList(&out, "Warnings", report.Warnings)
	return out.String()
}

func writeMarkdownList(out *strings.Builder, heading string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(out, "\n## %s\n\n", heading)
	for _, value := range values {
		fmt.Fprintf(out, "- %s\n", strings.ReplaceAll(value, "\n", " "))
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

func optionalBool(value *bool) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%t", *value)
}
