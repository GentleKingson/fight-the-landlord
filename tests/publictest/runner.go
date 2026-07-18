package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

const maxReportedErrors = 100

const publicTestConnectDelay = 125 * time.Millisecond

type runState struct {
	cfg config

	mu                    sync.Mutex
	errors                []string
	latencies             []float64
	connectionsAttempted  int
	connectionsSuccessful int
	reconnectsAttempted   int
	reconnectsSuccessful  int
	duplicateSettlements  int
	deadline              time.Time
	stopRematches         bool

	randomMu sync.Mutex
	random   *rand.Rand
}

func newRunState(cfg config) *runState {
	return &runState{cfg: cfg, random: rand.New(rand.NewSource(cfg.Seed))} //nolint:gosec // deterministic test scheduling only.
}

func (s *runState) recordError(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errors) < maxReportedErrors {
		s.errors = append(s.errors, fmt.Sprintf(format, args...))
	}
}

func (s *runState) recordLatency(duration time.Duration) {
	if duration <= 0 {
		return
	}
	s.mu.Lock()
	s.latencies = append(s.latencies, float64(duration)/float64(time.Millisecond))
	s.mu.Unlock()
}

func (s *runState) recordReconnect(duration time.Duration, err error) {
	s.mu.Lock()
	s.reconnectsAttempted++
	if err == nil {
		s.reconnectsSuccessful++
	}
	if duration > 0 {
		s.latencies = append(s.latencies, float64(duration)/float64(time.Millisecond))
	}
	s.mu.Unlock()
}

func (s *runState) recordDuplicateSettlement() {
	s.mu.Lock()
	s.duplicateSettlements++
	s.mu.Unlock()
}

func (s *runState) addDuplicateSettlementRecords(count int) {
	if count <= 0 {
		return
	}
	s.mu.Lock()
	s.duplicateSettlements += count
	s.mu.Unlock()
}

func (s *runState) shouldDisconnect() bool {
	if s.cfg.DisconnectRate <= 0 {
		return false
	}
	s.randomMu.Lock()
	value := s.random.Float64()
	s.randomMu.Unlock()
	return value < s.cfg.DisconnectRate
}

func (s *runState) beginWorkload(now time.Time) {
	s.mu.Lock()
	s.deadline = now.Add(s.cfg.Duration)
	s.stopRematches = false
	s.mu.Unlock()
}

func (s *runState) workloadDeadline() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadline
}

func (s *runState) rematchesAllowed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.stopRematches && !s.deadline.IsZero() && time.Now().Before(s.deadline)
}

func (s *runState) stopNewRematches() {
	s.mu.Lock()
	s.stopRematches = true
	s.mu.Unlock()
}

type gameRoom struct {
	run     *runState
	clients []*gameClient
	code    string

	mu           sync.Mutex
	started      map[string]struct{}
	completed    map[string]struct{}
	failed       map[string]struct{}
	rematch      map[string]bool
	pendingReady map[string]int
}

func newGameRoom(run *runState) *gameRoom {
	return &gameRoom{
		run:          run,
		started:      make(map[string]struct{}),
		completed:    make(map[string]struct{}),
		failed:       make(map[string]struct{}),
		rematch:      make(map[string]bool),
		pendingReady: make(map[string]int),
	}
}

func (r *gameRoom) startGame(gameID string) {
	if gameID == "" {
		return
	}
	r.mu.Lock()
	r.started[gameID] = struct{}{}
	r.mu.Unlock()
}

func (r *gameRoom) finishGame(gameID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.completed[gameID]; !exists {
		r.completed[gameID] = struct{}{}
	}
	decision, exists := r.rematch[gameID]
	if !exists {
		decision = r.run.rematchesAllowed()
		r.rematch[gameID] = decision
		if decision {
			r.pendingReady[gameID] = len(r.clients)
		}
	}
	return decision
}

func (r *gameRoom) readyDone(gameID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	remaining := r.pendingReady[gameID]
	if remaining <= 1 {
		delete(r.pendingReady, gameID)
		return
	}
	r.pendingReady[gameID] = remaining - 1
}

func (r *gameRoom) failGame(gameID string) {
	if gameID == "" {
		return
	}
	r.mu.Lock()
	r.failed[gameID] = struct{}{}
	r.mu.Unlock()
}

func (r *gameRoom) counts() (started, completed, failed, active, pending int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	started = len(r.started)
	completed = len(r.completed)
	for gameID := range r.started {
		_, done := r.completed[gameID]
		_, bad := r.failed[gameID]
		if !done || bad {
			failed++
		}
		if !done {
			active++
		}
	}
	for _, count := range r.pendingReady {
		pending += count
	}
	return started, completed, failed, active, pending
}

func (r *gameRoom) completedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.completed)
}

type reconciliationResult struct {
	Stats                  map[string]protocol.StatsResultPayload
	ExpectedPlayerGames    int
	ObservedPlayerGames    int
	TotalGamesReconciled   bool
	LeaderboardVerified    bool
	LeaderboardType        string
	LeaderboardEntries     int
	ProtocolRoomsRemaining int
}

func runPublicTest(ctx context.Context, cfg config) publicTestReport {
	startedAt := time.Now()
	state := newRunState(cfg)
	telemetry := startTelemetryMonitor(cfg.MetricsURL, cfg.MetricsInterval)
	clients := make([]*gameClient, 0, cfg.Players)
	rooms := make([]*gameRoom, 0, cfg.Players/3)
	baselineStats := make(map[string]protocol.StatsResultPayload, cfg.Players)
	reconciliation := reconciliationResult{}

	setupOK := connectAllPlayers(ctx, cfg, state, &clients)
	if setupOK {
		setupOK = createAllRooms(ctx, state, clients, &rooms)
	}
	if setupOK {
		setupOK = captureBaselineStats(ctx, state, clients, baselineStats)
	}
	if setupOK {
		state.beginWorkload(time.Now())
		setupOK = readyAllPlayers(ctx, state, clients)
	}
	if setupOK {
		waitForDuration(ctx, state)
		state.stopNewRematches()
		waitForGamesToQuiesce(ctx, state, rooms)
		reconciliation = reconcileResults(ctx, state, clients, baselineStats)
	}

	leaveAllRooms(ctx, state, clients)
	if len(clients) > 0 {
		roomList, latency, err := clients[0].roomList(ctx)
		state.recordLatency(latency)
		if err != nil {
			state.recordError("final room list query failed: %v", err)
			reconciliation.ProtocolRoomsRemaining = -1
		} else {
			reconciliation.ProtocolRoomsRemaining = len(roomList.Rooms)
		}
	}
	for _, client := range clients {
		client.close()
	}
	if cfg.Cooldown > 0 {
		timer := time.NewTimer(cfg.Cooldown)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	telemetryResult := telemetry.finish()
	return buildReport(cfg, state, rooms, reconciliation, telemetryResult, startedAt, time.Now())
}

func connectAllPlayers(ctx context.Context, cfg config, state *runState, clients *[]*gameClient) bool {
	for index := range cfg.Players {
		state.mu.Lock()
		state.connectionsAttempted++
		state.mu.Unlock()
		client, latency, err := connectGameClient(ctx, cfg, state, index)
		state.recordLatency(latency)
		if err != nil {
			state.recordError("connect client %d: %v", index, err)
			return false
		}
		state.mu.Lock()
		state.connectionsSuccessful++
		state.mu.Unlock()
		*clients = append(*clients, client)
		if index+1 < cfg.Players {
			timer := time.NewTimer(publicTestConnectDelay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				state.recordError("connection pacing canceled: %v", ctx.Err())
				return false
			}
		}
	}
	return true
}

func createAllRooms(ctx context.Context, state *runState, clients []*gameClient, rooms *[]*gameRoom) bool {
	for start := 0; start < len(clients); start += 3 {
		room := newGameRoom(state)
		room.clients = clients[start : start+3]
		for _, client := range room.clients {
			client.room = room
		}
		code, latency, err := room.clients[0].createRoom(ctx)
		state.recordLatency(latency)
		if err != nil {
			state.recordError("room %d create failed: %v", len(*rooms), err)
			return false
		}
		room.code = code
		for _, client := range room.clients[1:] {
			latency, err := client.joinRoom(ctx, code)
			state.recordLatency(latency)
			if err != nil {
				state.recordError("join room %s failed: %v", code, err)
				return false
			}
		}
		*rooms = append(*rooms, room)
	}
	return true
}

func captureBaselineStats(
	ctx context.Context,
	state *runState,
	clients []*gameClient,
	baseline map[string]protocol.StatsResultPayload,
) bool {
	for _, client := range clients {
		stats, latency, err := client.stats(ctx)
		state.recordLatency(latency)
		if err != nil {
			state.recordError("baseline stats for client %d failed: %v", client.index, err)
			return false
		}
		baseline[client.id()] = stats
	}
	return true
}

func readyAllPlayers(ctx context.Context, state *runState, clients []*gameClient) bool {
	for _, client := range clients {
		latency, err := client.ready(ctx)
		state.recordLatency(latency)
		if err != nil {
			state.recordError("initial ready for client %d failed: %v", client.index, err)
			return false
		}
	}
	return true
}

func waitForDuration(ctx context.Context, state *runState) {
	duration := time.Until(state.workloadDeadline())
	if duration <= 0 {
		return
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		state.recordError("workload canceled before duration elapsed: %v", ctx.Err())
	}
}

func waitForGamesToQuiesce(ctx context.Context, state *runState, rooms []*gameRoom) {
	grace := max(30*time.Second, 3*state.cfg.OperationTimeout)
	timeout := time.NewTimer(grace)
	defer timeout.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	stable := 0
	for {
		active, pending := 0, 0
		for _, room := range rooms {
			_, _, _, roomActive, roomPending := room.counts()
			active += roomActive
			pending += roomPending
		}
		if active == 0 && pending == 0 {
			stable++
			if stable >= 3 {
				return
			}
		} else {
			stable = 0
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			state.recordError("games did not quiesce within %s (%d active, %d ready commands pending)", grace, active, pending)
			for _, room := range rooms {
				room.mu.Lock()
				for gameID := range room.started {
					if _, complete := room.completed[gameID]; !complete {
						room.failed[gameID] = struct{}{}
					}
				}
				room.mu.Unlock()
			}
			return
		case <-ctx.Done():
			state.recordError("game completion wait canceled: %v", ctx.Err())
			return
		}
	}
}

func reconcileResults(
	ctx context.Context,
	state *runState,
	clients []*gameClient,
	baseline map[string]protocol.StatsResultPayload,
) reconciliationResult {
	result := reconciliationResult{Stats: make(map[string]protocol.StatsResultPayload, len(clients))}
	deadline := time.Now().Add(state.cfg.OperationTimeout)
	for {
		result.Stats = make(map[string]protocol.StatsResultPayload, len(clients))
		result.ExpectedPlayerGames = 0
		result.ObservedPlayerGames = 0
		allExpected := true
		for _, client := range clients {
			stats, latency, err := client.stats(ctx)
			state.recordLatency(latency)
			if err != nil {
				state.recordError("final stats for client %d failed: %v", client.index, err)
				allExpected = false
				continue
			}
			result.Stats[client.id()] = stats
			expected := 0
			if client.room != nil {
				expected = client.room.completedCount()
			}
			observed := stats.TotalGames - baseline[client.id()].TotalGames
			result.ExpectedPlayerGames += expected
			result.ObservedPlayerGames += observed
			if observed != expected {
				allExpected = false
			}
		}
		result.TotalGamesReconciled = allExpected && len(result.Stats) == len(clients)
		if result.TotalGamesReconciled || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if excess := result.ObservedPlayerGames - result.ExpectedPlayerGames; excess > 0 {
		state.addDuplicateSettlementRecords(excess)
	}

	if len(clients) == 0 {
		return result
	}
	leaderboard, latency, err := clients[0].leaderboard(ctx)
	state.recordLatency(latency)
	if err != nil {
		state.recordError("leaderboard query failed: %v", err)
		return result
	}
	result.LeaderboardType = leaderboard.Type
	result.LeaderboardEntries = len(leaderboard.Entries)
	result.LeaderboardVerified = verifyLeaderboard(leaderboard, result.Stats, clients)
	return result
}

func verifyLeaderboard(
	leaderboard protocol.LeaderboardResultPayload,
	stats map[string]protocol.StatsResultPayload,
	clients []*gameClient,
) bool {
	if leaderboard.Type != "total" {
		return false
	}
	entries := make(map[string]protocol.LeaderboardEntry, len(leaderboard.Entries))
	for _, entry := range leaderboard.Entries {
		entries[entry.PlayerID] = entry
	}
	for _, client := range clients {
		playerStats, statsOK := stats[client.id()]
		entry, entryOK := entries[client.id()]
		if !statsOK || !entryOK || entry.Score != playerStats.Score || entry.Wins != playerStats.Wins {
			return false
		}
	}
	return true
}

func leaveAllRooms(ctx context.Context, state *runState, clients []*gameClient) {
	for _, client := range clients {
		if client.room == nil || client.room.code == "" {
			continue
		}
		latency, err := client.leaveRoom(ctx)
		state.recordLatency(latency)
		if err != nil {
			state.recordError("client %d leave room failed: %v", client.index, err)
		}
	}
}
