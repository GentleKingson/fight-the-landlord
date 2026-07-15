package match

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

func TestCommitTracksPublishedRoomAndRemovalCleansExactAssociations(t *testing.T) {
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	matcher := NewMatcher(MatcherDeps{RoomManager: roomManager})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })
	oldRoom := room.NewMockRoom("reused", nil)
	replacement := room.NewMockRoom("reused", nil)
	roomManager.AddRoomForTest(oldRoom)

	attemptCtx, attemptCancel := context.WithCancel(context.Background())
	attempt := &matchAttempt{id: 1, ctx: attemptCtx, cancel: attemptCancel, room: oldRoom}
	for index, playerID := range []string{"p1", "p2", "p3"} {
		entryCtx, entryCancel := context.WithCancel(context.Background())
		entry := &QueueEntry{
			PlayerID: playerID,
			State:    QueueStateInflight,
			Cancel:   entryCancel,
			ctx:      entryCtx,
			attempt:  attempt,
		}
		attempt.entries = append(attempt.entries, entry)
		matcher.entries[playerID] = entry
		_ = index
	}
	matcher.attempts[attempt.id] = attempt

	require.True(t, matcher.commitAttempt(attempt, oldRoom))
	matcher.mu.Lock()
	matcher.publishedRooms[replacement] = attempt
	require.Contains(t, matcher.publishedRooms, oldRoom)
	matcher.mu.Unlock()

	matcher.RoomRemoved(oldRoom)
	matcher.mu.Lock()
	require.NotContains(t, matcher.publishedRooms, oldRoom)
	require.Contains(t, matcher.publishedRooms, replacement)
	matcher.mu.Unlock()
}

func TestCommitRejectsRoomThatLostExactManagerOwnership(t *testing.T) {
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	matcher := NewMatcher(MatcherDeps{RoomManager: roomManager})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })
	staleRoom := room.NewMockRoom("commit-reused", nil)
	replacement := room.NewMockRoom(staleRoom.Code, nil)
	roomManager.AddRoomForTest(staleRoom)
	roomManager.AddRoomForTest(replacement)

	attemptCtx, attemptCancel := context.WithCancel(context.Background())
	attempt := &matchAttempt{id: 9, ctx: attemptCtx, cancel: attemptCancel, room: staleRoom}
	for _, playerID := range []string{"p1", "p2", "p3"} {
		entryCtx, entryCancel := context.WithCancel(context.Background())
		entry := &QueueEntry{
			PlayerID: playerID,
			State:    QueueStateInflight,
			Cancel:   entryCancel,
			ctx:      entryCtx,
			attempt:  attempt,
		}
		attempt.entries = append(attempt.entries, entry)
		matcher.entries[playerID] = entry
	}
	matcher.attempts[attempt.id] = attempt

	require.False(t, matcher.commitAttempt(attempt, staleRoom))
	matcher.mu.Lock()
	require.NotContains(t, matcher.publishedRooms, staleRoom)
	matcher.mu.Unlock()
	matcher.RoomRemoved(staleRoom)
}

func TestRemovedRoomWhileRoomJoinedBlocksStopsRegistrationStartAndLaterDelivery(t *testing.T) {
	first := &roomJoinedBarrierClient{
		matcherClient: newMatcherClient("removed-publish-1", false),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	peers := newMatcherClients("removed-publish-peer", 2)
	registry := &activeClientRegistry{clients: make(map[string]types.ClientInterface)}
	registry.Set(first)
	registry.Set(peers[0])
	registry.Set(peers[1])
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	var registrations atomic.Int32
	matcher := NewMatcher(MatcherDeps{
		RoomManager:         roomManager,
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		GameConfig:          config.GameConfig{TurnTimeout: 3600, BidTimeout: 3600},
		RegisterSession: func(string, *session.GameSession) bool {
			registrations.Add(1)
			return true
		},
		BotFillDelay: time.Hour,
	})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(first.release) })
		require.NoError(t, matcher.Close())
	})
	roomManager.SetOnRoomRemoved(func(removal room.RoomRemoval) { matcher.RoomRemoved(removal.Room) })

	require.True(t, matcher.AddToQueue(first))
	require.True(t, matcher.AddToQueue(peers[0]))
	require.True(t, matcher.AddToQueue(peers[1]))
	waitSignal(t, first.entered, "blocked RoomJoined delivery")
	gameRoom := roomManager.GetRoom(first.GetRoom())
	require.NotNil(t, gameRoom)

	removed := make(chan bool, 1)
	go func() { removed <- roomManager.RemoveRoom(gameRoom, room.RoomRemovalRollback) }()
	stateRead := make(chan struct{})
	go func() {
		_ = gameRoom.State()
		close(stateRead)
	}()
	waitSignal(t, stateRead, "room state read while removal waits for publication")
	select {
	case <-removed:
		t.Fatal("room removal completed before the active RoomJoined enqueue quiesced")
	default:
	}
	releaseOnce.Do(func() { close(first.release) })
	require.True(t, waitValue(t, removed, "room removal after delivery quiesced"))
	require.NoError(t, matcher.Close())

	require.Nil(t, roomManager.GetRoom(gameRoom.Code))
	require.LessOrEqual(t, registrations.Load(), int32(1))
	for _, peer := range peers {
		peer.mu.Lock()
		messages := append([]*protocol.Message(nil), peer.messages...)
		peer.mu.Unlock()
		joinedAt, startedAt := -1, -1
		for index, message := range messages {
			if message.Type == protocol.MsgRoomJoined {
				joinedAt = index
			}
			if message.Type == protocol.MsgGameStart {
				startedAt = index
			}
		}
		if startedAt >= 0 {
			require.GreaterOrEqual(t, joinedAt, 0)
			require.Greater(t, startedAt, joinedAt)
		}
	}
}

func TestCloseAndExternalRemovalQuiesceBlockedRoomJoinedBeforeRoomLeft(t *testing.T) {
	first := &roomJoinedBarrierClient{
		matcherClient: newMatcherClient("shutdown-publish-1", false),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	peers := newMatcherClients("shutdown-publish-peer", 2)
	registry := &activeClientRegistry{clients: make(map[string]types.ClientInterface)}
	registry.Set(first)
	registry.Set(peers[0])
	registry.Set(peers[1])
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	var registrations atomic.Int32
	matcher := NewMatcher(MatcherDeps{
		RoomManager:         roomManager,
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		GameConfig:          config.GameConfig{TurnTimeout: 3600, BidTimeout: 3600},
		RegisterSession: func(string, *session.GameSession) bool {
			registrations.Add(1)
			return false
		},
		BotFillDelay: time.Hour,
	})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(first.release) })
		require.NoError(t, matcher.Close())
	})
	roomManager.SetOnRoomRemoved(func(removal room.RoomRemoval) {
		matcher.RoomRemoved(removal.Room)
		require.NoError(t, first.SendMessage(&protocol.Message{Type: protocol.MsgRoomLeft}))
	})

	require.True(t, matcher.AddToQueue(first))
	require.True(t, matcher.AddToQueue(peers[0]))
	require.True(t, matcher.AddToQueue(peers[1]))
	waitSignal(t, first.entered, "blocked RoomJoined delivery")
	gameRoom := roomManager.GetRoom(first.GetRoom())
	require.NotNil(t, gameRoom)

	closed := make(chan error, 1)
	go func() { closed <- matcher.Close() }()
	require.Eventually(t, func() bool {
		matcher.mu.Lock()
		defer matcher.mu.Unlock()
		return matcher.closed
	}, time.Second, time.Millisecond)

	removed := make(chan bool, 1)
	go func() { removed <- roomManager.RemoveRoom(gameRoom, room.RoomRemovalRollback) }()
	stateRead := make(chan struct{})
	go func() {
		_ = gameRoom.State()
		close(stateRead)
	}()
	waitSignal(t, stateRead, "room state read while shutdown waits for publication")
	require.False(t, first.hasMessage(protocol.MsgRoomLeft), "terminal event passed a blocked publish action")
	select {
	case err := <-closed:
		require.NoError(t, err)
		t.Fatal("matcher Close completed before the publish gate quiesced")
	default:
	}
	select {
	case <-removed:
		t.Fatal("external removal callback completed before the publish gate quiesced")
	default:
	}

	releaseOnce.Do(func() { close(first.release) })
	_ = waitValue(t, removed, "external room removal")
	require.NoError(t, waitValue(t, closed, "matcher Close"))
	require.Nil(t, roomManager.GetRoom(gameRoom.Code))
	require.Zero(t, registrations.Load())

	first.mu.Lock()
	messages := append([]*protocol.Message(nil), first.messages...)
	first.mu.Unlock()
	joinedAt, leftAt := -1, -1
	for index, message := range messages {
		switch message.Type {
		case protocol.MsgRoomJoined:
			joinedAt = index
		case protocol.MsgRoomLeft:
			leftAt = index
		}
	}
	require.GreaterOrEqual(t, joinedAt, 0)
	require.Greater(t, leftAt, joinedAt)
	for _, message := range messages[leftAt+1:] {
		require.NotEqual(t, protocol.MsgRoomJoined, message.Type)
		require.NotEqual(t, protocol.MsgPlayerReady, message.Type)
		require.NotEqual(t, protocol.MsgGameStart, message.Type)
	}
}

func TestPublishedRollbackTransfersBotCloseOwnershipExactlyOnce(t *testing.T) {
	human := newMatcherClient("rollback-human", false)
	bots := []*countingLifecycleBot{
		newCountingLifecycleBot("rollback-bot-1"),
		newCountingLifecycleBot("rollback-bot-2"),
	}
	registry := newActiveClientRegistry(human)
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	committed := make(chan struct{})
	releaseCommit := make(chan struct{})
	var factoryIndex atomic.Int32
	matcher := NewMatcher(MatcherDeps{
		RoomManager:         roomManager,
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(_ context.Context, first types.ClientInterface) (RoomAssembly, error) {
			tx, err := roomManager.BeginMatchRoom(first)
			if err != nil {
				return nil, err
			}
			return &commitReturnBarrierAssembly{tx: tx, committed: committed, release: releaseCommit}, nil
		},
		BotFactory: func(bot.DecisionEngine) types.ClientInterface {
			return bots[int(factoryIndex.Add(1))-1]
		},
		BotEngine:    bot.NewHeuristicEngine(),
		BotFillDelay: time.Hour,
	})
	roomManager.SetOnRoomRemoved(func(removal room.RoomRemoval) { matcher.RoomRemoved(removal.Room) })
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	require.True(t, matcher.PracticeMatch(human))
	waitSignal(t, committed, "published room before Commit returns")
	gameRoom := roomManager.GetRoom(human.GetRoom())
	require.NotNil(t, gameRoom)
	require.True(t, roomManager.RemoveRoom(gameRoom, room.RoomRemovalRollback))
	for _, botClient := range bots {
		require.EqualValues(t, 1, botClient.closeCount.Load())
	}
	close(releaseCommit)
	human.waitForMessage(t, protocol.MsgMatchCancelled)
	require.NoError(t, matcher.Close())
	for _, botClient := range bots {
		require.EqualValues(t, 1, botClient.closeCount.Load(), "published rollback must not close a RoomManager-owned bot twice")
	}
}

func TestMatcherCloseRetiresCommittedPracticeRoomAndBots(t *testing.T) {
	human := newMatcherClient("shutdown-human", false)
	bots := []*countingLifecycleBot{
		newCountingLifecycleBot("shutdown-bot-1"),
		newCountingLifecycleBot("shutdown-bot-2"),
	}
	registry := newActiveClientRegistry(human)
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	registered := make(chan *session.GameSession, 1)
	var factoryIndex atomic.Int32
	matcher := NewMatcher(MatcherDeps{
		RoomManager:         roomManager,
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		GameConfig:          config.GameConfig{TurnTimeout: 3600, BidTimeout: 3600},
		RegisterSession: func(_ string, game *session.GameSession) bool {
			registered <- game
			return true
		},
		BotFactory: func(bot.DecisionEngine) types.ClientInterface {
			return bots[int(factoryIndex.Add(1))-1]
		},
		BotEngine:    bot.NewHeuristicEngine(),
		BotFillDelay: time.Hour,
	})
	roomManager.SetOnRoomRemoved(func(removal room.RoomRemoval) { matcher.RoomRemoved(removal.Room) })

	require.True(t, matcher.PracticeMatch(human))
	game := waitValue(t, registered, "committed practice session")
	t.Cleanup(game.StopAllTimers)
	human.waitForMessage(t, protocol.MsgGameStart)
	roomCode := human.GetRoom()
	require.NotEmpty(t, roomCode)
	require.NoError(t, matcher.Close())

	require.Nil(t, roomManager.GetRoom(roomCode))
	for _, botClient := range bots {
		require.EqualValues(t, 1, botClient.closeCount.Load())
	}
	require.NoError(t, matcher.Close())
	for _, botClient := range bots {
		require.EqualValues(t, 1, botClient.closeCount.Load())
	}
}

func TestRoomRemovalCancelsButReservesInflightEntriesForWorkerCleanup(t *testing.T) {
	matcher := NewMatcher(MatcherDeps{})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })
	gameRoom := room.NewMockRoom("inflight", nil)

	attemptCtx, attemptCancel := context.WithCancel(context.Background())
	attempt := &matchAttempt{id: 7, ctx: attemptCtx, cancel: attemptCancel, room: gameRoom}
	for _, playerID := range []string{"p1", "p2", "p3"} {
		entryCtx, entryCancel := context.WithCancel(context.Background())
		entry := &QueueEntry{
			PlayerID: playerID,
			State:    QueueStateInflight,
			Cancel:   entryCancel,
			ctx:      entryCtx,
			attempt:  attempt,
		}
		attempt.entries = append(attempt.entries, entry)
		matcher.entries[playerID] = entry
	}
	matcher.attempts[attempt.id] = attempt

	matcher.RoomRemoved(gameRoom)
	require.ErrorIs(t, attemptCtx.Err(), context.Canceled)
	matcher.mu.Lock()
	require.Contains(t, matcher.attempts, attempt.id)
	for _, entry := range attempt.entries {
		require.Equal(t, QueueStateRolledBack, entry.State)
		require.Same(t, entry, matcher.entries[entry.PlayerID])
	}
	matcher.mu.Unlock()
}

func TestRoomRemovalReservesGenerationUntilStaleCancellationDeliveryCompletes(t *testing.T) {
	first := &matchCancelledBarrierClient{
		matcherClient: newMatcherClient("reserved-generation-1", false),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	peers := newMatcherClients("reserved-generation-peer", 2)
	registry := &activeClientRegistry{clients: map[string]types.ClientInterface{
		first.GetID():    first,
		peers[0].GetID(): peers[0],
		peers[1].GetID(): peers[1],
	}}
	assemblyReady := make(chan *scriptedRoomAssembly, 1)
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(_ context.Context, owner types.ClientInterface) (RoomAssembly, error) {
			assembly := newScriptedRoomAssembly(owner)
			assembly.blockJoinAt = 1
			assemblyReady <- assembly
			return assembly, nil
		},
		BotFillDelay: time.Hour,
	})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(first.release) })
		require.NoError(t, matcher.Close())
	})

	require.True(t, matcher.AddToQueue(first))
	require.True(t, matcher.AddToQueue(peers[0]))
	require.True(t, matcher.AddToQueue(peers[1]))
	assembly := waitValue(t, assemblyReady, "room assembly")
	require.Equal(t, 1, waitValue(t, assembly.joinStarted, "blocked Join"))
	matcher.RoomRemoved(assembly.Room())
	waitSignal(t, first.entered, "stale MatchCancelled delivery")

	require.False(t, matcher.AddToQueue(first), "old generation must remain reserved until its cancellation finishes")
	releaseOnce.Do(func() { close(first.release) })
	matcher.workers.Wait()
	require.True(t, matcher.AddToQueue(first), "the player may requeue after rollback and cancellation complete")
	require.True(t, matcher.RemoveFromQueue(first))
}

func TestQueueTimeoutReservesGenerationUntilCancellationDeliveryCompletes(t *testing.T) {
	client := &matchCancelledBarrierClient{
		matcherClient: newMatcherClient("timeout-reserved-generation", false),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	registry := &activeClientRegistry{clients: map[string]types.ClientInterface{client.GetID(): client}}
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        10 * time.Millisecond,
		ResolveActiveClient: registry.Resolve,
		BotFillDelay:        time.Hour,
	})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(client.release) })
		require.NoError(t, matcher.Close())
	})

	require.True(t, matcher.AddToQueue(client))
	waitSignal(t, client.entered, "blocked timeout cancellation")
	require.False(t, matcher.AddToQueue(client), "timed-out generation must remain reserved through cancellation delivery")

	releaseOnce.Do(func() { close(client.release) })
	require.Eventually(t, func() bool {
		matcher.mu.Lock()
		defer matcher.mu.Unlock()
		return matcher.entries[client.GetID()] == nil
	}, time.Second, time.Millisecond)
	require.True(t, matcher.AddToQueue(client))
	require.True(t, matcher.RemoveFromQueue(client))
}

func TestQueueTimeoutCannotPublishMatchQueuedAfterCancellation(t *testing.T) {
	client := &matchQueuedNameBarrierClient{
		matcherClient: newMatcherClient("late-queued-generation", false),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	registry := &activeClientRegistry{clients: map[string]types.ClientInterface{client.GetID(): client}}
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        10 * time.Millisecond,
		ResolveActiveClient: registry.Resolve,
		BotFillDelay:        time.Hour,
	})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(client.release) })
		require.NoError(t, matcher.Close())
	})

	added := make(chan bool, 1)
	go func() { added <- matcher.AddToQueue(client) }()
	waitSignal(t, client.entered, "post-publication queue log")
	client.waitForMessage(t, protocol.MsgMatchCancelled)
	releaseOnce.Do(func() { close(client.release) })
	require.True(t, waitValue(t, added, "AddToQueue completion"))
	require.False(t, client.hasMessage(protocol.MsgMatchQueued), "expired generation published MatchQueued after MatchCancelled")
}

func TestMatchCancellationSkipsClientThatBoundAnotherRoom(t *testing.T) {
	client := &conditionalMatchControlClient{
		matcherClient: newMatcherClient("cancel-after-room-bind", false),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	registry := &activeClientRegistry{clients: map[string]types.ClientInterface{client.GetID(): client}}
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        10 * time.Millisecond,
		ResolveActiveClient: registry.Resolve,
		BotFillDelay:        time.Hour,
	})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(client.release) })
		require.NoError(t, matcher.Close())
	})

	require.True(t, matcher.AddToQueue(client))
	client.waitForMessage(t, protocol.MsgMatchQueued)
	waitSignal(t, client.entered, "conditional MatchCancelled delivery")
	client.SetRoom("replacement-room")
	releaseOnce.Do(func() { close(client.release) })
	require.Eventually(t, func() bool {
		matcher.mu.Lock()
		defer matcher.mu.Unlock()
		return matcher.entries[client.GetID()] == nil
	}, time.Second, time.Millisecond)
	require.False(t, client.hasMessage(protocol.MsgMatchCancelled))
}

type countingLifecycleBot struct {
	*matcherClient
	closeCount atomic.Int32
}

type matchCancelledBarrierClient struct {
	*matcherClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type matchQueuedNameBarrierClient struct {
	*matcherClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (client *matchQueuedNameBarrierClient) GetName() string {
	client.once.Do(func() { close(client.entered) })
	<-client.release
	return client.matcherClient.GetName()
}

type conditionalMatchControlClient struct {
	*matcherClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (client *conditionalMatchControlClient) SendMessageIfRoom(expectedRoom string, message *protocol.Message) (bool, error) {
	if message.Type == protocol.MsgMatchCancelled {
		client.once.Do(func() { close(client.entered) })
		<-client.release
	}
	client.mu.Lock()
	if client.roomCode != expectedRoom {
		client.mu.Unlock()
		return false, nil
	}
	client.messages = append(client.messages, message)
	client.mu.Unlock()
	select {
	case client.messageC <- message:
	default:
	}
	return true, nil
}

func (client *matchCancelledBarrierClient) SendMessage(message *protocol.Message) error {
	err := client.matcherClient.SendMessage(message)
	if message.Type == protocol.MsgMatchCancelled {
		client.once.Do(func() { close(client.entered) })
		<-client.release
	}
	return err
}

func newCountingLifecycleBot(id string) *countingLifecycleBot {
	return &countingLifecycleBot{matcherClient: newMatcherClient(id, true)}
}

func (client *countingLifecycleBot) Close() {
	client.closeCount.Add(1)
	client.matcherClient.Close()
}

type commitReturnBarrierAssembly struct {
	tx        *room.MatchRoomTransaction
	committed chan struct{}
	release   chan struct{}
}

func (assembly *commitReturnBarrierAssembly) Room() *room.Room { return assembly.tx.Room() }

func (assembly *commitReturnBarrierAssembly) Join(_ context.Context, client types.ClientInterface) error {
	return assembly.tx.Join(client)
}

func (assembly *commitReturnBarrierAssembly) Commit(ctx context.Context) error {
	if _, err := assembly.tx.Commit(); err != nil {
		return err
	}
	close(assembly.committed)
	select {
	case <-assembly.release:
		return nil
	case <-ctx.Done():
		// The room was already published. Returning nil lets the matcher route
		// cleanup through its published-room ownership path.
		return nil
	}
}

func (assembly *commitReturnBarrierAssembly) Rollback() error {
	assembly.tx.Rollback()
	return nil
}

func (assembly *commitReturnBarrierAssembly) BotsOwnedByRoomLifecycle() bool { return true }
