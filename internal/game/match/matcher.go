package match

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// SessionRegistrationFunc registers a committed game session. It returns false
// when the exact room was removed or replaced before registration completed.
type SessionRegistrationFunc func(roomCode string, gs *session.GameSession) bool

const queueTimeout = 30 * time.Second

// QueueState is the authoritative lifecycle of one matchmaking entry.
type QueueState uint8

const (
	QueueStateQueued QueueState = iota
	QueueStateInflight
	QueueStateCommitted
	QueueStateRolledBack
)

// QueueEntry binds a player identity to one physical-client generation and
// one server-owned deadline.
type QueueEntry struct {
	PlayerID         string
	ClientGeneration uint64
	JoinedAt         time.Time
	Deadline         time.Time
	State            QueueState
	Cancel           context.CancelFunc

	ctx      context.Context
	client   types.ClientInterface
	attempt  *matchAttempt
	ownedBot bool
	// deliveryMu orders the initial/replacement MatchQueued publication with
	// every terminal MatchCancelled delivery for this exact generation.
	deliveryMu sync.Mutex
}

// RoomAssembly is an unpublished room transaction used by the matcher.
type RoomAssembly interface {
	Room() *room.Room
	Join(context.Context, types.ClientInterface) error
	Commit(context.Context) error
	Rollback() error
}

// roomLifecycleAssembly reports whether a successful Commit transferred Bot
// ownership to the RoomManager removal lifecycle. Custom fault-injection
// assemblies that publish managed rooms can opt into the same contract.
type roomLifecycleAssembly interface {
	BotsOwnedByRoomLifecycle() bool
}

// BeginRoomFunc starts an unpublished room transaction.
type BeginRoomFunc func(context.Context, types.ClientInterface) (RoomAssembly, error)

// BotFactory creates a matcher-owned bot that must be closed on rollback.
type BotFactory func(bot.DecisionEngine) types.ClientInterface

type matchAttempt struct {
	id          uint64
	ctx         context.Context
	cancel      context.CancelFunc
	start       chan struct{}
	startOnce   sync.Once
	entries     []*QueueEntry
	practice    bool
	reason      string
	cancelledBy string
	room        *room.Room // immutable identity once room assembly begins
	// publishMu serializes each post-commit side effect with RoomRemoved. It is
	// never nested inside Matcher.mu and may cover bounded client enqueueing.
	publishMu sync.Mutex
}

// Matcher coordinates queued, inflight, committed, and rolled-back matches.
type Matcher struct {
	roomManager     *room.RoomManager
	redisStore      *storage.RedisStore
	leaderboard     *storage.LeaderboardManager
	gameConfig      config.GameConfig
	botEngine       bot.DecisionEngine
	botCfg          config.BotConfig
	registerSession SessionRegistrationFunc
	resolveActive   func(string) types.ClientInterface
	beginRoom       BeginRoomFunc
	botFactory      BotFactory
	queueTimeout    time.Duration
	botFillDelay    time.Duration

	ctx        context.Context
	cancel     context.CancelFunc
	closedDone chan struct{}

	queue    []*QueueEntry
	entries  map[string]*QueueEntry
	attempts map[uint64]*matchAttempt
	// publishedRooms keeps committed ownership alive through RoomJoined,
	// session registration, Start, persistence, and eventual room removal.
	publishedRooms map[*room.Room]*matchAttempt
	generation     uint64
	attemptID      uint64
	closed         bool

	botFillEpoch  uint64
	botFillCancel context.CancelFunc

	mu      sync.Mutex
	workers sync.WaitGroup
}

// MatcherDeps contains matcher dependencies and deterministic test seams.
type MatcherDeps struct {
	RoomManager         *room.RoomManager
	RedisStore          *storage.RedisStore
	Leaderboard         *storage.LeaderboardManager
	GameConfig          config.GameConfig
	BotEngine           bot.DecisionEngine
	BotConfig           config.BotConfig
	RegisterSession     SessionRegistrationFunc
	ResolveActiveClient func(string) types.ClientInterface
	BeginRoom           BeginRoomFunc
	BotFactory          BotFactory
	QueueTimeout        time.Duration
	BotFillDelay        time.Duration
	Context             context.Context
}

// NewMatcher creates a matcher whose timers and workers are rooted in one
// cancellable context.
func NewMatcher(deps MatcherDeps) *Matcher {
	root := deps.Context
	if root == nil {
		root = context.Background()
	}
	ctx, cancel := context.WithCancel(root)
	timeout := deps.QueueTimeout
	if timeout <= 0 {
		timeout = queueTimeout
	}
	fillDelay := deps.BotFillDelay
	if fillDelay <= 0 {
		fillDelay = time.Duration(deps.BotConfig.BotFillTimeout) * time.Second
	}
	factory := deps.BotFactory
	if factory == nil {
		factory = func(engine bot.DecisionEngine) types.ClientInterface {
			return bot.NewBotClient(engine)
		}
	}
	m := &Matcher{
		roomManager:     deps.RoomManager,
		redisStore:      deps.RedisStore,
		leaderboard:     deps.Leaderboard,
		gameConfig:      deps.GameConfig,
		botEngine:       deps.BotEngine,
		botCfg:          deps.BotConfig,
		registerSession: deps.RegisterSession,
		resolveActive:   deps.ResolveActiveClient,
		beginRoom:       deps.BeginRoom,
		botFactory:      factory,
		queueTimeout:    timeout,
		botFillDelay:    fillDelay,
		ctx:             ctx,
		cancel:          cancel,
		closedDone:      make(chan struct{}),
		queue:           make([]*QueueEntry, 0),
		entries:         make(map[string]*QueueEntry),
		attempts:        make(map[uint64]*matchAttempt),
		publishedRooms:  make(map[*room.Room]*matchAttempt),
	}
	if m.beginRoom == nil && m.roomManager != nil {
		m.beginRoom = m.beginManagedRoom
	}
	return m
}

type managedRoomAssembly struct {
	tx                *room.MatchRoomTransaction
	lifecycleOwnsBots bool
}

func (m *Matcher) beginManagedRoom(ctx context.Context, first types.ClientInterface) (RoomAssembly, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := m.roomManager.BeginMatchRoom(first)
	if err != nil {
		return nil, err
	}
	return &managedRoomAssembly{tx: tx}, nil
}

func (assembly *managedRoomAssembly) Room() *room.Room { return assembly.tx.Room() }

func (assembly *managedRoomAssembly) Join(ctx context.Context, client types.ClientInterface) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return assembly.tx.Join(client)
}

func (assembly *managedRoomAssembly) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := assembly.tx.Commit()
	if err == nil {
		assembly.lifecycleOwnsBots = true
	}
	return err
}

func (assembly *managedRoomAssembly) BotsOwnedByRoomLifecycle() bool {
	return assembly.lifecycleOwnsBots
}

func (assembly *managedRoomAssembly) Rollback() error {
	assembly.tx.Rollback()
	return nil
}

// AddToQueue adds one physical client generation to the authoritative queue.
func (m *Matcher) AddToQueue(client types.ClientInterface) bool {
	if client == nil || client.GetRoom() != "" {
		return false
	}
	playerID := client.GetID()
	joinedAt := time.Now()
	deadline := joinedAt.Add(m.queueTimeout)

	m.mu.Lock()
	if m.closed || m.entries[playerID] != nil {
		m.mu.Unlock()
		return false
	}
	entry := m.newEntryLocked(client, joinedAt, deadline, false)
	m.queue = append(m.queue, entry)
	attempt := m.freezeQueuedAttemptLocked(false)
	if attempt == nil {
		m.scheduleBotFillLocked()
	}
	queueLength := len(m.queue)
	m.mu.Unlock()

	log.Printf("🔍 玩家 %s 加入匹配队列，当前队列长度: %d", client.GetName(), queueLength)
	current, delivered := m.publishQueuedEntry(entry, client, false, "match queued")
	if current && !delivered {
		m.cancelEntry(client, "delivery_failed", "")
	}
	if attempt != nil {
		m.launchAttempt(attempt)
	}
	return true
}

func (m *Matcher) newEntryLocked(client types.ClientInterface, joinedAt, deadline time.Time, ownedBot bool) *QueueEntry {
	entryCtx, cancel := context.WithDeadline(m.ctx, deadline)
	m.generation++
	entry := &QueueEntry{
		PlayerID:         client.GetID(),
		ClientGeneration: m.generation,
		JoinedAt:         joinedAt,
		Deadline:         deadline,
		State:            QueueStateQueued,
		Cancel:           cancel,
		ctx:              entryCtx,
		client:           client,
		ownedBot:         ownedBot,
	}
	m.entries[entry.PlayerID] = entry
	m.workers.Add(1)
	go m.watchEntry(entry)
	return entry
}

func (m *Matcher) watchEntry(entry *QueueEntry) {
	defer m.workers.Done()
	<-entry.ctx.Done()
	if errors.Is(entry.ctx.Err(), context.DeadlineExceeded) {
		m.expireEntry(entry)
	}
}

func (m *Matcher) expireEntry(entry *QueueEntry) {
	m.mu.Lock()
	current := m.entries[entry.PlayerID]
	if current != entry {
		m.mu.Unlock()
		return
	}
	if entry.State != QueueStateQueued {
		m.cancelEntryLocked(entry, "timeout", "")
		m.mu.Unlock()
		return
	}
	client := entry.client
	m.removeQueuedEntryLocked(entry)
	entry.State = QueueStateRolledBack
	entry.Cancel()
	m.scheduleBotFillLocked()
	m.mu.Unlock()

	// Keep the exact generation reserved until its terminal notification has
	// been enqueued. Otherwise a frozen client can requeue between deletion and
	// delivery, then receive this stale cancellation after its new MatchQueued.
	if m.isCurrent(entry) {
		entry.deliveryMu.Lock()
		m.notifyCancelled(entry.PlayerID, client, "timeout")
		entry.deliveryMu.Unlock()
	}
	m.mu.Lock()
	if m.entries[entry.PlayerID] == entry {
		delete(m.entries, entry.PlayerID)
	}
	if !m.closed {
		m.scheduleBotFillLocked()
	}
	m.mu.Unlock()
}

// RemoveFromQueue cancels both queued and not-yet-committed inflight work.
func (m *Matcher) RemoveFromQueue(client types.ClientInterface) bool {
	if client == nil {
		return false
	}
	return m.cancelEntry(client, "cancelled", client.GetID())
}

// PlayerDisconnected cancels work owned by the disconnected physical handle.
func (m *Matcher) PlayerDisconnected(client types.ClientInterface) bool {
	if client == nil {
		return false
	}
	return m.cancelEntry(client, "disconnected", client.GetID())
}

func (m *Matcher) cancelEntry(client types.ClientInterface, reason, cancelledBy string) bool {
	if client == nil {
		return false
	}
	m.mu.Lock()
	entry := m.entries[client.GetID()]
	if entry == nil || entry.client != client {
		m.mu.Unlock()
		return false
	}
	accepted := m.cancelEntryLocked(entry, reason, cancelledBy)
	m.mu.Unlock()
	return accepted
}

func (m *Matcher) cancelEntryLocked(entry *QueueEntry, reason, cancelledBy string) bool {
	switch entry.State {
	case QueueStateQueued:
		m.removeQueuedEntryLocked(entry)
		entry.State = QueueStateRolledBack
		entry.Cancel()
		delete(m.entries, entry.PlayerID)
		m.scheduleBotFillLocked()
		return true
	case QueueStateInflight:
		m.cancelAttemptLocked(entry.attempt, reason, cancelledBy)
		return true
	default:
		return false
	}
}

func (m *Matcher) removeQueuedEntryLocked(target *QueueEntry) {
	for index, entry := range m.queue {
		if entry == target {
			m.queue = append(m.queue[:index], m.queue[index+1:]...)
			break
		}
	}
	if len(m.queue) == 0 {
		m.cancelBotFillLocked()
	}
}

func (m *Matcher) cancelAttemptLocked(attempt *matchAttempt, reason, cancelledBy string) {
	if attempt == nil {
		return
	}
	if attempt.reason == "" {
		attempt.reason = reason
		attempt.cancelledBy = cancelledBy
	}
	attempt.cancel()
	for _, entry := range attempt.entries {
		if entry.State == QueueStateInflight {
			entry.State = QueueStateRolledBack
		}
		entry.Cancel()
	}
}

// ReplaceClient migrates queued work to a replacement physical generation. An
// inflight replacement rolls back because staged room handles are already fixed.
func (m *Matcher) ReplaceClient(previous, replacement types.ClientInterface) bool {
	if previous == nil || replacement == nil || previous.GetID() != replacement.GetID() {
		return false
	}
	m.mu.Lock()
	entry := m.entries[previous.GetID()]
	if entry == nil || entry.client != previous {
		m.mu.Unlock()
		return false
	}
	if entry.State == QueueStateInflight {
		m.cancelAttemptLocked(entry.attempt, "connection_replaced", previous.GetID())
		m.mu.Unlock()
		return true
	}
	if entry.State != QueueStateQueued {
		m.mu.Unlock()
		return false
	}
	m.mu.Unlock()

	entry.deliveryMu.Lock()
	defer entry.deliveryMu.Unlock()
	m.mu.Lock()
	if m.closed || m.entries[previous.GetID()] != entry || entry.client != previous {
		m.mu.Unlock()
		return false
	}
	if entry.State == QueueStateInflight {
		m.cancelAttemptLocked(entry.attempt, "connection_replaced", previous.GetID())
		m.mu.Unlock()
		return true
	}
	if entry.State != QueueStateQueued {
		m.mu.Unlock()
		return false
	}
	m.generation++
	entry.ClientGeneration = m.generation
	entry.client = replacement
	deadline := entry.Deadline
	m.mu.Unlock()
	delivered := m.sendIfUnbound(entry.PlayerID, replacement, codec.MustNewMessage(protocol.MsgMatchQueued, protocol.MatchQueuedPayload{
		DeadlineMS: deadline.UnixMilli(),
		Practice:   false,
	}), "replacement match queued")
	if !delivered {
		m.cancelEntry(replacement, "delivery_failed", "")
	}
	return true
}

func (m *Matcher) freezeQueuedAttemptLocked(practice bool) *matchAttempt {
	if len(m.queue) < 3 || m.closed {
		return nil
	}
	entries := append([]*QueueEntry(nil), m.queue[:3]...)
	m.queue = m.queue[3:]
	return m.newAttemptLocked(entries, practice)
}

func (m *Matcher) newAttemptLocked(entries []*QueueEntry, practice bool) *matchAttempt {
	m.cancelBotFillLocked()
	m.attemptID++
	ctx, cancel := context.WithCancel(m.ctx)
	attempt := &matchAttempt{
		id:       m.attemptID,
		ctx:      ctx,
		cancel:   cancel,
		start:    make(chan struct{}),
		entries:  entries,
		practice: practice,
	}
	for _, entry := range entries {
		entry.State = QueueStateInflight
		entry.attempt = attempt
	}
	m.attempts[attempt.id] = attempt
	m.workers.Add(1)
	go m.attemptWorker(attempt)
	return attempt
}

func (m *Matcher) launchAttempt(attempt *matchAttempt) {
	attempt.startOnce.Do(func() { close(attempt.start) })
}

func (m *Matcher) attemptWorker(attempt *matchAttempt) {
	defer m.workers.Done()
	select {
	case <-attempt.start:
	case <-attempt.ctx.Done():
	}
	m.runAttempt(attempt)
}

func (m *Matcher) runAttempt(attempt *matchAttempt) {
	if err := m.validateAttempt(attempt); err != nil {
		m.failAttempt(attempt, nil, false, "participant_unavailable", err)
		return
	}
	if m.beginRoom == nil {
		m.failAttempt(attempt, nil, false, "assembly_failed", errors.New("match room assembler unavailable"))
		return
	}

	clients := attemptClients(attempt)
	assembly, err := m.beginRoom(attempt.ctx, clients[0])
	if err != nil {
		m.failAttempt(attempt, nil, false, "assembly_failed", err)
		return
	}
	gameRoom := assembly.Room()
	if gameRoom == nil {
		m.failAttempt(attempt, assembly, false, "assembly_failed", errors.New("room transaction began without a room identity"))
		return
	}
	if !m.bindAttemptRoom(attempt, gameRoom) {
		m.failAttempt(attempt, assembly, false, "cancelled", attempt.ctx.Err())
		return
	}
	if err := m.validateAttempt(attempt); err != nil {
		m.failAttempt(attempt, assembly, false, "participant_unavailable", err)
		return
	}
	for _, client := range clients[1:] {
		if err := assembly.Join(attempt.ctx, client); err != nil {
			m.failAttempt(attempt, assembly, false, "assembly_failed", err)
			return
		}
		if err := m.validateAttempt(attempt); err != nil {
			m.failAttempt(attempt, assembly, false, "participant_unavailable", err)
			return
		}
	}
	if err := assembly.Commit(attempt.ctx); err != nil {
		m.failAttempt(attempt, assembly, false, "assembly_failed", err)
		return
	}
	// A successful managed Commit transfers Bot ownership to RoomManager. Any
	// removal after this point closes them through the exact room lifecycle.
	managedAssembly, managed := assembly.(roomLifecycleAssembly)
	roomLifecycleOwnsBots := managed && managedAssembly.BotsOwnedByRoomLifecycle()
	if err := m.validateAttempt(attempt); err != nil {
		m.failAttempt(attempt, assembly, roomLifecycleOwnsBots, "participant_unavailable", err)
		return
	}
	if assembly.Room() != gameRoom {
		m.failAttempt(attempt, assembly, roomLifecycleOwnsBots, "assembly_failed", errors.New("room transaction changed identity during commit"))
		return
	}
	startingPlayers, err := gameRoom.ReadyAllAndStart(clients)
	if err != nil {
		m.failAttempt(attempt, assembly, roomLifecycleOwnsBots, "assembly_failed", err)
		return
	}
	if !m.commitAttempt(attempt, gameRoom) {
		m.failAttempt(attempt, assembly, roomLifecycleOwnsBots, "cancelled", attempt.ctx.Err())
		return
	}

	m.publishCommittedMatch(attempt, gameRoom, clients, startingPlayers)
}

func (m *Matcher) bindAttemptRoom(attempt *matchAttempt, gameRoom *room.Room) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gameRoom == nil || m.closed || attempt.ctx.Err() != nil || m.attempts[attempt.id] != attempt {
		return false
	}
	if attempt.room != nil && attempt.room != gameRoom {
		return false
	}
	attempt.room = gameRoom
	return true
}

func attemptClients(attempt *matchAttempt) []types.ClientInterface {
	clients := make([]types.ClientInterface, len(attempt.entries))
	for index, entry := range attempt.entries {
		clients[index] = entry.client
	}
	return clients
}

func (m *Matcher) validateAttempt(attempt *matchAttempt) error {
	if err := attempt.ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed || m.attempts[attempt.id] != attempt {
		m.mu.Unlock()
		return context.Canceled
	}
	entries := append([]*QueueEntry(nil), attempt.entries...)
	for _, entry := range entries {
		if entry.State != QueueStateInflight || m.entries[entry.PlayerID] != entry {
			m.mu.Unlock()
			return context.Canceled
		}
	}
	m.mu.Unlock()

	if m.resolveActive != nil {
		for _, entry := range entries {
			if entry.client.IsBot() {
				continue
			}
			if m.resolveActive(entry.PlayerID) != entry.client {
				return fmt.Errorf("player %s physical generation is no longer active", entry.PlayerID)
			}
		}
	}
	return attempt.ctx.Err()
}

func (m *Matcher) commitAttempt(attempt *matchAttempt, gameRoom *room.Room) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gameRoom == nil || m.closed || attempt.ctx.Err() != nil || m.attempts[attempt.id] != attempt || attempt.room != gameRoom {
		return false
	}
	// Lock order is Matcher.mu -> RoomManager.mu. RoomManager invokes removal
	// callbacks only after releasing its locks, so exact ownership validation
	// and association form one side-effect-free commit boundary.
	if m.roomManager != nil && m.roomManager.GetRoom(gameRoom.Code) != gameRoom {
		return false
	}
	if current := m.publishedRooms[gameRoom]; current != nil && current != attempt {
		return false
	}
	for _, entry := range attempt.entries {
		if entry.State != QueueStateInflight || m.entries[entry.PlayerID] != entry {
			return false
		}
	}
	for _, entry := range attempt.entries {
		entry.State = QueueStateCommitted
		entry.Cancel()
		delete(m.entries, entry.PlayerID)
	}
	m.publishedRooms[gameRoom] = attempt
	delete(m.attempts, attempt.id)
	attempt.cancel()
	return true
}

// RoomRemoved unlinks only the exact room identity. Inflight entries remain
// reserved until their worker has rolled back and delivered cancellation, so a
// stale cancellation cannot race a newly queued generation of the same client.
func (m *Matcher) RoomRemoved(gameRoom *room.Room) {
	if gameRoom == nil {
		return
	}
	m.mu.Lock()
	publishedAttempt := m.publishedRooms[gameRoom]
	delete(m.publishedRooms, gameRoom)
	for _, attempt := range m.attempts {
		if attempt.room != gameRoom {
			continue
		}
		m.cancelAttemptLocked(attempt, "room_removed", "")
	}
	m.mu.Unlock()
	if publishedAttempt != nil {
		publishedAttempt.publishMu.Lock()
		publishedAttempt.publishMu.Unlock()
	}
}

func (m *Matcher) failAttempt(attempt *matchAttempt, assembly RoomAssembly, roomLifecycleOwnsBots bool, fallbackReason string, cause error) {
	m.mu.Lock()
	if attempt.reason == "" {
		attempt.reason = fallbackReason
	}
	reason := attempt.reason
	cancelledBy := attempt.cancelledBy
	for _, entry := range attempt.entries {
		if entry.State != QueueStateCommitted {
			entry.State = QueueStateRolledBack
		}
		entry.Cancel()
	}
	attempt.cancel()
	m.mu.Unlock()

	if cause != nil && !errors.Is(cause, context.Canceled) {
		log.Printf("匹配事务回滚 (%s): %v", reason, cause)
	}
	if assembly != nil {
		if err := assembly.Rollback(); err != nil {
			log.Printf("回滚匹配房间失败: %v", err)
		}
	}

	entries := append([]*QueueEntry(nil), attempt.entries...)
	for _, entry := range entries {
		if entry.ownedBot {
			if !roomLifecycleOwnsBots {
				entry.client.Close()
			}
			continue
		}
		if entry.PlayerID == cancelledBy && (reason == "cancelled" || reason == "disconnected" || reason == "connection_replaced") {
			continue
		}
		if m.isCurrent(entry) {
			entry.deliveryMu.Lock()
			m.notifyCancelled(entry.PlayerID, entry.client, reason)
			entry.deliveryMu.Unlock()
		}
	}

	// Keep exact entries authoritative through rollback and delivery. Only now
	// may AddToQueue publish a new generation for any participant.
	m.mu.Lock()
	delete(m.attempts, attempt.id)
	for _, entry := range entries {
		if m.entries[entry.PlayerID] == entry {
			delete(m.entries, entry.PlayerID)
		}
	}
	if !m.closed {
		m.scheduleBotFillLocked()
	}
	m.mu.Unlock()
}

func (m *Matcher) isCurrent(entry *QueueEntry) bool {
	return m.resolveActive == nil || entry.client.IsBot() || m.resolveActive(entry.PlayerID) == entry.client
}

func (m *Matcher) publishedRoomCurrent(attempt *matchAttempt, gameRoom *room.Room) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.publishedRooms[gameRoom] != attempt {
		return false
	}
	return m.roomManager == nil || m.roomManager.GetRoom(gameRoom.Code) == gameRoom
}

func (m *Matcher) withPublishedRoom(attempt *matchAttempt, gameRoom *room.Room, action func()) bool {
	attempt.publishMu.Lock()
	defer attempt.publishMu.Unlock()
	if !m.publishedRoomCurrent(attempt, gameRoom) {
		return false
	}
	action()
	return true
}

func (m *Matcher) publishCommittedMatch(attempt *matchAttempt, gameRoom *room.Room, clients []types.ClientInterface, startingPlayers []room.PlayerSnapshot) {
	for index, client := range clients {
		playerID := attempt.entries[index].PlayerID
		delivered := false
		if !m.withPublishedRoom(attempt, gameRoom, func() {
			if m.roomManager != nil {
				var err error
				delivered, err = m.roomManager.SendRoomJoinedSnapshotIfCurrent(gameRoom, playerID, client)
				if err != nil {
					log.Printf("发送 room joined 给玩家 %s 失败: %v", playerID, err)
				}
				return
			}
			playerInfo, ok := gameRoom.GetPlayerInfo(playerID)
			if !ok {
				return
			}
			delivered = m.sendCurrentMember(gameRoom, playerID, client, codec.MustNewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
				RoomCode: gameRoom.Code,
				Player:   playerInfo,
				Players:  gameRoom.GetAllPlayersInfo(),
			}), "room joined")
		}) || !delivered {
			if m.roomManager != nil {
				m.roomManager.RemoveRoom(gameRoom, room.RoomRemovalRollback)
			}
			return
		}
	}
	for _, player := range startingPlayers {
		published := false
		if !m.withPublishedRoom(attempt, gameRoom, func() {
			message := codec.MustNewMessage(protocol.MsgPlayerReady, protocol.PlayerReadyPayload{
				PlayerID: player.ID,
				Ready:    true,
			})
			if m.roomManager != nil {
				published = m.roomManager.BroadcastIfCurrentRoom(gameRoom, message)
				return
			}
			gameRoom.Broadcast(message)
			published = true
		}) || !published {
			return
		}
	}

	// RoomJoined delivery can overlap a physical disconnect after the matcher
	// has committed. Re-snapshot membership so the new session does not revive
	// a client handle that the room has already detached.
	var sessionPlayers []room.PlayerSnapshot
	if !m.withPublishedRoom(attempt, gameRoom, func() {
		sessionPlayers = gameRoom.SnapshotPlayers()
	}) {
		return
	}
	gs := session.NewGameSessionWithPlayers(gameRoom, sessionPlayers, m.leaderboard, m.gameConfig)
	gs.SetRoomManager(m.roomManager)
	if m.registerSession != nil {
		registered := false
		if !m.withPublishedRoom(attempt, gameRoom, func() {
			registered = m.registerSession(gameRoom.Code, gs)
		}) || !registered {
			gs.Retire()
			return
		}
	}
	if !m.withPublishedRoom(attempt, gameRoom, func() {
		for _, client := range clients {
			if botClient, ok := client.(*bot.BotClient); ok {
				botClient.SetSession(gs)
			}
		}
		gs.Start()
	}) {
		gs.Retire()
		return
	}

	if m.roomManager != nil {
		if !m.withPublishedRoom(attempt, gameRoom, func() {
			m.roomManager.PersistRoom(gameRoom)
		}) {
			gs.Retire()
			return
		}
	}
	log.Printf("🎮 匹配成功！房间 %s，玩家: %s, %s, %s",
		gameRoom.Code, clients[0].GetName(), clients[1].GetName(), clients[2].GetName())
}

// PracticeMatch starts one human plus two matcher-owned bots transactionally.
func (m *Matcher) PracticeMatch(client types.ClientInterface) bool {
	if client == nil || client.GetRoom() != "" {
		return false
	}
	engine := m.botEngine
	if engine == nil {
		engine = bot.NewHeuristicEngine()
	}
	bots := []types.ClientInterface{m.botFactory(engine), m.botFactory(engine)}
	for _, botClient := range bots {
		if botClient == nil {
			for _, created := range bots {
				if created != nil {
					created.Close()
				}
			}
			return false
		}
	}

	joinedAt := time.Now()
	deadline := joinedAt.Add(m.queueTimeout)
	m.mu.Lock()
	if m.closed || m.entries[client.GetID()] != nil {
		m.mu.Unlock()
		for _, botClient := range bots {
			botClient.Close()
		}
		return false
	}
	for _, botClient := range bots {
		if m.entries[botClient.GetID()] != nil {
			m.mu.Unlock()
			for _, created := range bots {
				created.Close()
			}
			return false
		}
	}
	if bots[0].GetID() == bots[1].GetID() || bots[0].GetID() == client.GetID() || bots[1].GetID() == client.GetID() {
		m.mu.Unlock()
		for _, created := range bots {
			created.Close()
		}
		return false
	}
	entries := []*QueueEntry{m.newEntryLocked(client, joinedAt, deadline, false)}
	for _, botClient := range bots {
		entries = append(entries, m.newEntryLocked(botClient, joinedAt, deadline, true))
	}
	attempt := m.newAttemptLocked(entries, true)
	m.mu.Unlock()

	current, delivered := m.publishQueuedEntry(entries[0], client, true, "practice match queued")
	if current && !delivered {
		m.cancelEntry(client, "delivery_failed", "")
	}
	m.launchAttempt(attempt)
	return true
}

func (m *Matcher) scheduleBotFillLocked() {
	if m.closed || !m.botCfg.Enabled || len(m.queue) == 0 || len(m.queue) >= 3 || m.botFillCancel != nil {
		return
	}
	m.botFillEpoch++
	epoch := m.botFillEpoch
	ctx, cancel := context.WithCancel(m.ctx)
	m.botFillCancel = cancel
	delay := m.botFillDelay
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m.fillWithBots(epoch)
		}
	}()
}

func (m *Matcher) cancelBotFillLocked() {
	m.botFillEpoch++
	if m.botFillCancel != nil {
		m.botFillCancel()
		m.botFillCancel = nil
	}
}

func (m *Matcher) fillWithBots(epoch uint64) {
	m.mu.Lock()
	if m.closed || epoch != m.botFillEpoch || m.botFillCancel == nil || len(m.queue) == 0 || len(m.queue) >= 3 {
		m.mu.Unlock()
		return
	}
	m.botFillCancel = nil
	expected := append([]*QueueEntry(nil), m.queue...)
	needed := 3 - len(expected)
	m.mu.Unlock()

	engine := m.botEngine
	if engine == nil {
		engine = bot.NewHeuristicEngine()
	}
	created := make([]types.ClientInterface, 0, needed)
	for range needed {
		botClient := m.botFactory(engine)
		if botClient == nil {
			break
		}
		created = append(created, botClient)
	}
	if len(created) != needed {
		for _, client := range created {
			client.Close()
		}
		return
	}

	m.mu.Lock()
	valid := !m.closed && epoch == m.botFillEpoch && sameEntries(m.queue, expected)
	seenIDs := make(map[string]struct{}, len(created))
	for _, client := range created {
		_, duplicate := seenIDs[client.GetID()]
		if duplicate || m.entries[client.GetID()] != nil {
			valid = false
			break
		}
		seenIDs[client.GetID()] = struct{}{}
	}
	if !valid {
		m.mu.Unlock()
		for _, client := range created {
			client.Close()
		}
		return
	}
	now := time.Now()
	deadline := now.Add(m.queueTimeout)
	for _, client := range created {
		m.queue = append(m.queue, m.newEntryLocked(client, now, deadline, true))
	}
	attempt := m.freezeQueuedAttemptLocked(false)
	m.mu.Unlock()
	if attempt != nil {
		m.launchAttempt(attempt)
	}
}

func sameEntries(current, expected []*QueueEntry) bool {
	if len(current) != len(expected) {
		return false
	}
	for index := range current {
		if current[index] != expected[index] {
			return false
		}
	}
	return true
}

func (m *Matcher) notifyCancelled(playerID string, client types.ClientInterface, reason string) {
	m.sendIfUnbound(playerID, client, codec.MustNewMessage(protocol.MsgMatchCancelled, protocol.MatchCancelledPayload{
		Reason: reason,
	}), "match cancelled")
}

func (m *Matcher) publishQueuedEntry(entry *QueueEntry, client types.ClientInterface, practice bool, operation string) (bool, bool) {
	entry.deliveryMu.Lock()
	defer entry.deliveryMu.Unlock()

	m.mu.Lock()
	current := !m.closed && m.entries[entry.PlayerID] == entry && entry.client == client &&
		(entry.State == QueueStateQueued || entry.State == QueueStateInflight)
	deadline := entry.Deadline
	m.mu.Unlock()
	if !current {
		return false, false
	}
	return true, m.sendIfUnbound(entry.PlayerID, client, codec.MustNewMessage(protocol.MsgMatchQueued, protocol.MatchQueuedPayload{
		DeadlineMS: deadline.UnixMilli(),
		Practice:   practice,
	}), operation)
}

func (m *Matcher) sendIfUnbound(playerID string, client types.ClientInterface, message *protocol.Message, operation string) bool {
	if client == nil {
		return false
	}
	sent, err := types.SendMessageIfIdentity(client, playerID, "", message)
	if err != nil {
		log.Printf("发送 %s 给玩家 %s 失败: %v", operation, playerID, err)
		return false
	}
	return sent
}

func (m *Matcher) send(client types.ClientInterface, message *protocol.Message, operation string) bool {
	if client == nil {
		return false
	}
	if err := client.SendMessage(message); err != nil {
		log.Printf("发送 %s 给玩家 %s 失败: %v", operation, client.GetID(), err)
		return false
	}
	return true
}

func (m *Matcher) sendCurrentMember(gameRoom *room.Room, playerID string, client types.ClientInterface, message *protocol.Message, operation string) bool {
	if client == nil || gameRoom == nil {
		return false
	}
	if m.roomManager != nil {
		sent, err := m.roomManager.SendIfCurrentMember(gameRoom, playerID, client, message)
		if err != nil {
			log.Printf("发送 %s 给玩家 %s 失败: %v", operation, playerID, err)
			return false
		}
		return sent
	}
	sent, err := types.SendMessageIfIdentity(client, playerID, gameRoom.Code, message)
	if err != nil {
		log.Printf("发送 %s 给玩家 %s 失败: %v", operation, playerID, err)
		return false
	}
	return sent
}

// GetQueueLength returns only queued entries, excluding inflight work.
func (m *Matcher) GetQueueLength() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}

// Close cancels authoritative deadlines, bot fill, and inflight transactions,
// then waits until every matcher-owned worker has exited.
func (m *Matcher) Close() error {
	m.mu.Lock()
	if m.closed {
		done := m.closedDone
		m.mu.Unlock()
		<-done
		return nil
	}
	m.closed = true
	m.cancelBotFillLocked()
	m.cancel()
	queued := append([]*QueueEntry(nil), m.queue...)
	m.queue = nil
	for _, entry := range queued {
		entry.State = QueueStateRolledBack
		entry.Cancel()
		delete(m.entries, entry.PlayerID)
	}
	for _, attempt := range m.attempts {
		m.cancelAttemptLocked(attempt, "shutdown", "")
	}
	published := make(map[*room.Room]*matchAttempt, len(m.publishedRooms))
	for gameRoom, attempt := range m.publishedRooms {
		published[gameRoom] = attempt
	}
	m.mu.Unlock()

	for gameRoom, attempt := range published {
		attempt.publishMu.Lock()
		// Keep the association visible until the publish gate is quiescent. An
		// independent RoomManager removal can otherwise miss the attempt and
		// deliver RoomLeft while an earlier RoomJoined enqueue is still blocked.
		m.mu.Lock()
		if m.publishedRooms[gameRoom] == attempt {
			delete(m.publishedRooms, gameRoom)
		}
		m.mu.Unlock()
		if m.roomManager != nil {
			m.roomManager.RemoveRoom(gameRoom, room.RoomRemovalShutdown)
			attempt.publishMu.Unlock()
			continue
		}
		// Custom assemblers without a RoomManager retain matcher ownership.
		for _, entry := range attempt.entries {
			if entry.ownedBot {
				entry.client.Close()
			}
		}
		attempt.publishMu.Unlock()
	}
	for _, entry := range queued {
		if entry.ownedBot {
			entry.client.Close()
			continue
		}
		entry.deliveryMu.Lock()
		m.notifyCancelled(entry.PlayerID, entry.client, "shutdown")
		entry.deliveryMu.Unlock()
	}
	m.workers.Wait()
	close(m.closedDone)
	return nil
}
