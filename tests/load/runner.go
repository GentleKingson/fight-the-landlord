package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const maxReportedErrors = 100

type runState struct {
	mu                  sync.Mutex
	errors              []string
	connectionLatencies []float64
	reconnectLatencies  []float64
	roomLatencies       []float64
	matchLatencies      []float64
	idleLatencies       []float64
}

func (s *runState) recordError(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errors) < maxReportedErrors {
		s.errors = append(s.errors, fmt.Sprintf(format, args...))
	}
}

func (s *runState) recordLatency(target *[]float64, duration time.Duration) {
	s.mu.Lock()
	*target = append(*target, float64(duration)/float64(time.Millisecond))
	s.mu.Unlock()
}

func runLoad(ctx context.Context, cfg config) loadReport {
	started := time.Now()
	state := &runState{}
	telemetry := startTelemetryMonitor(cfg.MetricsURL, cfg.MetricsInterval)
	generator := startGeneratorMonitor()
	report := newLoadReport(cfg, started)

	clients := connectClients(ctx, cfg, state)
	report.ConnectionsSuccessful = len(clients)
	report.ConnectionsRejected = report.ConnectionsAttempted - report.ConnectionsSuccessful
	report.ConnectionSuccessRate = successRate(report.ConnectionsSuccessful, report.ConnectionsAttempted)

	report.ReconnectsAttempted, report.ReconnectsSuccessful = reconnectClients(ctx, cfg, clients, state)
	report.ReconnectSuccessRate = successRate(report.ReconnectsSuccessful, report.ReconnectsAttempted)

	roomResults := runRoomScenarios(ctx, cfg, clients, state)
	report.RoomScenariosAttempted = roomResults.attempted
	report.RoomScenariosSuccessful = roomResults.successful
	report.RoomSuccessRate = successRate(roomResults.successful, roomResults.attempted)
	report.RoomsCreated = roomResults.created
	report.RoomsJoined = roomResults.joined
	report.RoomsLeft = roomResults.left

	matchResults := runMatchScenarios(ctx, cfg, clients, state)
	report.MatchOperationsAttempted = matchResults.operationsAttempted
	report.MatchOperationsSuccessful = matchResults.operationsSuccessful
	report.MatchTimeoutsAttempted = matchResults.timeoutsAttempted
	report.MatchTimeoutsObserved = matchResults.timeoutsObserved
	report.MatchSuccessRate = successRate(
		matchResults.operationsSuccessful+matchResults.timeoutsObserved,
		matchResults.operationsAttempted+matchResults.timeoutsAttempted,
	)

	if len(clients) > 0 {
		select {
		case <-time.After(cfg.Duration):
		case <-ctx.Done():
		}
	}
	report.IdleChecksAttempted, report.IdleChecksSuccessful = pingClients(ctx, cfg, clients, state)
	report.IdleSuccessRate = successRate(report.IdleChecksSuccessful, report.IdleChecksAttempted)

	for _, client := range clients {
		client.close()
	}
	if cfg.Cooldown > 0 {
		select {
		case <-time.After(cfg.Cooldown):
		case <-ctx.Done():
		}
	}

	applyTelemetry(&report, telemetry.finish(), generator.finish())
	applyRunState(&report, state)
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = report.FinishedAt.Sub(report.StartedAt).Milliseconds()
	report.evaluateThresholds()
	if ctx.Err() != nil {
		report.Status = "canceled"
		report.ThresholdFailures = append(report.ThresholdFailures, "load test context was canceled")
	}
	return report
}

func newLoadReport(cfg config, started time.Time) loadReport {
	return loadReport{
		SchemaVersion: 1,
		Status:        "running",
		StartedAt:     started.UTC(),
		Config: reportConfig{
			URL:                cfg.URL,
			MetricsURL:         cfg.MetricsURL,
			Connections:        cfg.Connections,
			ConnectConcurrency: cfg.ConnectConcurrency,
			IdleDuration:       cfg.Duration.String(),
			Reconnects:         cfg.Reconnects,
			RoomOperations:     cfg.RoomOperations,
			MatchOperations:    cfg.MatchOperations,
			MatchTimeouts:      cfg.MatchTimeouts,
		},
		ConnectionsAttempted: cfg.Connections,
		Thresholds:           cfg.Thresholds,
		Limitations: []string{
			"The load generator does not inject Redis pause/restart or measure Redis failover recovery.",
			"DouZero timeout, invalid-response, and fallback behavior are not exercised by this client.",
			"No complete games are played; games_started and games_finished come from optional server telemetry.",
			"Slow-reader buffer exhaustion, SIGTERM/restart recovery, and multi-instance ownership require the separate chaos harness.",
			"server_crash_count is null because a remote client cannot distinguish a process restart from a network outage; the workflow separately checks server liveness.",
		},
	}
}

func applyTelemetry(report *loadReport, serverTelemetry telemetrySnapshot, generatorTelemetry generatorSnapshot) {
	report.PeakRSSBytes = serverTelemetry.PeakRSSBytes
	report.PeakGoroutines = serverTelemetry.PeakGoroutines
	report.BaselineGoroutines = serverTelemetry.BaselineGoroutines
	report.FinalGoroutines = serverTelemetry.FinalGoroutines
	report.RedisErrorCount = serverTelemetry.RedisErrorCount
	report.SlowClientDisconnectCount = serverTelemetry.SlowClientDisconnectCount
	report.BaselineConnections = serverTelemetry.BaselineConnections
	report.FinalConnections = serverTelemetry.FinalConnections
	report.TelemetrySamples = serverTelemetry.Samples
	report.TelemetryErrors = serverTelemetry.Errors
	if serverTelemetry.GamesStarted != nil {
		report.GamesStarted = *serverTelemetry.GamesStarted
	}
	if serverTelemetry.GamesFinished != nil {
		report.GamesFinished = *serverTelemetry.GamesFinished
	}
	if serverTelemetry.Warning != "" {
		report.Warnings = append(report.Warnings, serverTelemetry.Warning)
	}
	report.LoadGeneratorPeakHeapBytes = generatorTelemetry.PeakHeapBytes
	report.LoadGeneratorPeakGoroutines = generatorTelemetry.PeakGoroutines
	report.LoadGeneratorFinalHeapBytes = generatorTelemetry.FinalHeapBytes
	report.LoadGeneratorFinalGoroutines = generatorTelemetry.FinalGoroutines
}

func applyRunState(report *loadReport, state *runState) {
	state.mu.Lock()
	report.Errors = append(report.Errors, state.errors...)
	report.ConnectionLatencyMS = summarizeLatency(state.connectionLatencies)
	report.ReconnectLatencyMS = summarizeLatency(state.reconnectLatencies)
	report.RoomLatencyMS = summarizeLatency(state.roomLatencies)
	report.MatchLatencyMS = summarizeLatency(state.matchLatencies)
	report.IdlePingLatencyMS = summarizeLatency(state.idleLatencies)
	combined := make([]float64, 0, len(state.connectionLatencies)+len(state.reconnectLatencies)+len(state.roomLatencies)+len(state.matchLatencies)+len(state.idleLatencies))
	combined = append(combined, state.connectionLatencies...)
	combined = append(combined, state.reconnectLatencies...)
	combined = append(combined, state.roomLatencies...)
	combined = append(combined, state.matchLatencies...)
	combined = append(combined, state.idleLatencies...)
	state.mu.Unlock()
	report.LatencyMS = summarizeLatency(combined)
}

type matchScenarioResults struct {
	operationsAttempted  int
	operationsSuccessful int
	timeoutsAttempted    int
	timeoutsObserved     int
}

func runMatchScenarios(ctx context.Context, cfg config, clients []*loadClient, state *runState) matchScenarioResults {
	results := matchScenarioResults{}
	active := activeClients(clients)
	remaining := cfg.MatchOperations
	clientOffset := 0
	for remaining > 0 && len(active) > 0 {
		batchSize := min(remaining, min(2, len(active)))
		batch := make([]*loadClient, batchSize)
		for index := range batchSize {
			batch[index] = active[(clientOffset+index)%len(active)]
		}
		clientOffset = (clientOffset + batchSize) % len(active)
		results.operationsAttempted += batchSize

		queued := runConcurrentMatchCommands(ctx, cfg, batch, true, state)
		if len(queued) == 0 {
			remaining -= batchSize
			continue
		}
		canceled := runConcurrentMatchCommands(ctx, cfg, queued, false, state)
		results.operationsSuccessful += len(canceled)
		remaining -= batchSize
	}
	for results.timeoutsAttempted < cfg.MatchTimeouts && len(active) > 0 {
		client := active[(clientOffset+results.timeoutsAttempted)%len(active)]
		results.timeoutsAttempted++
		operationContext, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
		latency, err := client.queueMatch(operationContext)
		cancel()
		state.recordLatency(&state.matchLatencies, latency)
		if err != nil {
			state.recordError("match timeout[%d] enqueue: %v", results.timeoutsAttempted-1, err)
			continue
		}
		timeoutContext, timeoutCancel := context.WithTimeout(ctx, cfg.MatchTimeoutWait)
		latency, err = client.awaitMatchTimeout(timeoutContext)
		timeoutCancel()
		state.recordLatency(&state.matchLatencies, latency)
		if err != nil {
			state.recordError("match timeout[%d] await: %v", results.timeoutsAttempted-1, err)
			// Best effort cleanup makes a failed timeout observation safe to rerun.
			cleanupContext, cleanupCancel := context.WithTimeout(ctx, cfg.OperationTimeout)
			_, _ = client.cancelMatch(cleanupContext)
			cleanupCancel()
			continue
		}
		results.timeoutsObserved++
	}
	return results
}

func runConcurrentMatchCommands(ctx context.Context, cfg config, clients []*loadClient, enqueue bool, state *runState) []*loadClient {
	type result struct {
		client  *loadClient
		latency time.Duration
		err     error
	}
	results := make(chan result, len(clients))
	var group sync.WaitGroup
	for _, client := range clients {
		group.Add(1)
		go func(loadClient *loadClient) {
			defer group.Done()
			operationContext, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
			defer cancel()
			var latency time.Duration
			var err error
			if enqueue {
				latency, err = loadClient.queueMatch(operationContext)
			} else {
				latency, err = loadClient.cancelMatch(operationContext)
			}
			results <- result{client: loadClient, latency: latency, err: err}
		}(client)
	}
	group.Wait()
	close(results)

	successful := make([]*loadClient, 0, len(clients))
	for result := range results {
		state.recordLatency(&state.matchLatencies, result.latency)
		if result.err != nil {
			action := "enqueue"
			if !enqueue {
				action = "cancel"
			}
			state.recordError("match %s[%d]: %v", action, result.client.index, result.err)
			continue
		}
		successful = append(successful, result.client)
	}
	return successful
}

type connectResult struct {
	client  *loadClient
	latency time.Duration
	err     error
	index   int
}

func connectClients(ctx context.Context, cfg config, state *runState) []*loadClient {
	workers := cfg.ConnectConcurrency
	if workers > cfg.Connections {
		workers = cfg.Connections
	}
	jobs := make(chan int)
	results := make(chan connectResult, cfg.Connections)
	var workerGroup sync.WaitGroup
	for range workers {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for index := range jobs {
				operationContext, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
				client, latency, err := connectLoadClient(operationContext, cfg, index)
				cancel()
				results <- connectResult{client: client, latency: latency, err: err, index: index}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range cfg.Connections {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workerGroup.Wait()
		close(results)
	}()

	clients := make([]*loadClient, 0, cfg.Connections)
	for result := range results {
		state.recordLatency(&state.connectionLatencies, result.latency)
		if result.err != nil {
			state.recordError("connect[%d]: %v", result.index, result.err)
			continue
		}
		clients = append(clients, result.client)
	}
	return clients
}

func reconnectClients(ctx context.Context, cfg config, clients []*loadClient, state *runState) (attempted, successful int) {
	attempted = cfg.Reconnects
	if attempted > len(clients) {
		attempted = len(clients)
	}
	if attempted == 0 {
		return 0, 0
	}
	type result struct {
		index   int
		latency time.Duration
		err     error
	}
	results := make(chan result, attempted)
	var group sync.WaitGroup
	for index := range attempted {
		group.Add(1)
		go func(clientIndex int) {
			defer group.Done()
			operationContext, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
			defer cancel()
			latency, err := clients[clientIndex].reconnect(operationContext)
			results <- result{index: clientIndex, latency: latency, err: err}
		}(index)
	}
	group.Wait()
	close(results)

	for result := range results {
		state.recordLatency(&state.reconnectLatencies, result.latency)
		if result.err != nil {
			state.recordError("reconnect[%d]: %v", result.index, result.err)
			continue
		}
		successful++
	}
	return attempted, successful
}

type roomScenarioResults struct {
	attempted  int
	successful int
	created    int
	joined     int
	left       int
}

type roomJoinResult struct {
	joined     int
	left       int
	successful bool
}

func runRoomScenarios(ctx context.Context, cfg config, clients []*loadClient, state *runState) roomScenarioResults {
	results := roomScenarioResults{attempted: cfg.RoomOperations}
	for scenario := range cfg.RoomOperations {
		active := activeClients(clients)
		if len(active) == 0 {
			state.recordError("room[%d]: no active client is available", scenario)
			continue
		}
		owner := active[scenario%len(active)]
		roomCode, latency, err := createRoom(ctx, cfg, owner)
		state.recordLatency(&state.roomLatencies, latency)
		if err != nil {
			state.recordError("room[%d] create: %v", scenario, err)
			continue
		}
		results.created++
		scenarioOK := true

		if len(active) > 1 {
			joinResult := runRoomJoinerScenario(ctx, cfg, active, scenario, roomCode, state)
			results.joined += joinResult.joined
			results.left += joinResult.left
			scenarioOK = joinResult.successful
		}

		leaveLatency, leaveErr := leaveRoom(ctx, cfg, owner)
		state.recordLatency(&state.roomLatencies, leaveLatency)
		if leaveErr != nil {
			state.recordError("room[%d] owner leave: %v", scenario, leaveErr)
			scenarioOK = false
		} else {
			results.left++
		}
		if scenarioOK {
			results.successful++
		}
	}
	return results
}

func runRoomJoinerScenario(ctx context.Context, cfg config, active []*loadClient, scenario int, roomCode string, state *runState) roomJoinResult {
	joiner := active[(scenario+1)%len(active)]
	joinLatency, joinErr := joinRoom(ctx, cfg, joiner, roomCode)
	state.recordLatency(&state.roomLatencies, joinLatency)
	if joinErr != nil {
		state.recordError("room[%d] join: %v", scenario, joinErr)
		return roomJoinResult{}
	}

	result := roomJoinResult{joined: 1}
	leaveLatency, leaveErr := leaveRoom(ctx, cfg, joiner)
	state.recordLatency(&state.roomLatencies, leaveLatency)
	if leaveErr != nil {
		state.recordError("room[%d] joiner leave: %v", scenario, leaveErr)
		return result
	}
	result.left = 1
	result.successful = true
	return result
}

func createRoom(ctx context.Context, cfg config, client *loadClient) (string, time.Duration, error) {
	operationContext, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
	defer cancel()
	return client.createRoom(operationContext)
}

func joinRoom(ctx context.Context, cfg config, client *loadClient, roomCode string) (time.Duration, error) {
	operationContext, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
	defer cancel()
	return client.joinRoom(operationContext, roomCode)
}

func leaveRoom(ctx context.Context, cfg config, client *loadClient) (time.Duration, error) {
	operationContext, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
	defer cancel()
	return client.leaveRoom(operationContext)
}

func activeClients(clients []*loadClient) []*loadClient {
	active := make([]*loadClient, 0, len(clients))
	for _, client := range clients {
		if client != nil && client.physical != nil && client.physical.active() {
			active = append(active, client)
		}
	}
	return active
}

func pingClients(ctx context.Context, cfg config, clients []*loadClient, state *runState) (attempted, successful int) {
	if len(clients) == 0 {
		return 0, 0
	}
	attempted = len(clients)
	type result struct {
		index   int
		latency time.Duration
		err     error
	}
	results := make(chan result, len(clients))
	var group sync.WaitGroup
	for index, client := range clients {
		group.Add(1)
		go func(clientIndex int, loadClient *loadClient) {
			defer group.Done()
			operationContext, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
			defer cancel()
			latency, err := loadClient.ping(operationContext)
			results <- result{index: clientIndex, latency: latency, err: err}
		}(index, client)
	}
	group.Wait()
	close(results)

	for result := range results {
		state.recordLatency(&state.idleLatencies, result.latency)
		if result.err != nil {
			state.recordError("idle ping[%d]: %v", result.index, result.err)
			continue
		}
		successful++
	}
	return attempted, successful
}
