package match

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

const matcherTestDeadline = 3 * time.Second

var errInjectedMatchAssembly = errors.New("injected match room assembly failure")

// Keep the public state-machine contract visible in tests. In particular, a
// QueueEntry must not retain a stale client pointer as its identity.
func TestQueueEntryCarriesAuthoritativeState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entry := QueueEntry{
		PlayerID:         "player-1",
		ClientGeneration: 7,
		JoinedAt:         time.Unix(100, 0),
		Deadline:         time.Unix(130, 0),
		State:            QueueStateQueued,
		Cancel:           cancel,
	}

	require.Equal(t, "player-1", entry.PlayerID)
	require.EqualValues(t, 7, entry.ClientGeneration)
	require.True(t, entry.Deadline.After(entry.JoinedAt))
	require.Equal(t, QueueStateQueued, entry.State)
	require.NotEqual(t, QueueStateQueued, QueueStateInflight)
	require.NotEqual(t, QueueStateInflight, QueueStateCommitted)
	require.NotEqual(t, QueueStateCommitted, QueueStateRolledBack)

	entry.Cancel()
	select {
	case <-ctx.Done():
	case <-time.After(matcherTestDeadline):
		t.Fatal("QueueEntry.Cancel did not cancel its authoritative context")
	}
}

func TestMatcherServerSideTimeoutExpiresFrozenClient(t *testing.T) {
	client := newMatcherClient("timeout-player", false)
	registry := newActiveClientRegistry(client)
	startedAt := time.Now()
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        25 * time.Millisecond,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(context.Context, types.ClientInterface) (RoomAssembly, error) {
			t.Fatal("one queued player must not begin a room transaction")
			return nil, errInjectedMatchAssembly
		},
		BotFillDelay: time.Hour,
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	require.True(t, matcher.AddToQueue(client))
	queued := client.waitForMessage(t, protocol.MsgMatchQueued)
	queuedPayload, err := codec.ParsePayload[protocol.MatchQueuedPayload](queued)
	require.NoError(t, err)
	require.GreaterOrEqual(t, queuedPayload.DeadlineMS, startedAt.UnixMilli())
	require.LessOrEqual(t, queuedPayload.DeadlineMS, startedAt.Add(time.Second).UnixMilli())

	// The client performs no cancel action and never advances a client-side
	// clock. The server timer must still retire the queue entry.
	cancelled := client.waitForMessage(t, protocol.MsgMatchCancelled)
	cancelPayload, err := codec.ParsePayload[protocol.MatchCancelledPayload](cancelled)
	require.NoError(t, err)
	require.NotEmpty(t, cancelPayload.Reason)
	require.Zero(t, matcher.GetQueueLength())
	require.False(t, matcher.RemoveFromQueue(client))
	require.True(t, matcher.AddToQueue(client), "expired entries must not remain inflight")
	require.True(t, matcher.RemoveFromQueue(client))
}

func TestMatcherAssemblyFailuresRollbackBeforeRoomJoined(t *testing.T) {
	testCases := []struct {
		name         string
		beginFails   bool
		joinFailsAt  int
		commitFails  bool
		wantRollback int32
	}{
		{name: "begin room", beginFails: true},
		{name: "join second player", joinFailsAt: 1, wantRollback: 1},
		{name: "join third player", joinFailsAt: 2, wantRollback: 1},
		{name: "commit room", commitFails: true, wantRollback: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			clients := newMatcherClients("failure", 3)
			registry := newActiveClientRegistry(clients...)
			beginCalled := make(chan struct{}, 1)
			var assembly *scriptedRoomAssembly
			var registered atomic.Int32
			matcher := NewMatcher(MatcherDeps{
				QueueTimeout:        time.Hour,
				ResolveActiveClient: registry.Resolve,
				BeginRoom: func(_ context.Context, first types.ClientInterface) (RoomAssembly, error) {
					beginCalled <- struct{}{}
					if testCase.beginFails {
						return nil, errInjectedMatchAssembly
					}
					assembly = newScriptedRoomAssembly(first)
					assembly.failJoinAt = testCase.joinFailsAt
					assembly.failCommit = testCase.commitFails
					return assembly, nil
				},
				RegisterSession: func(string, *session.GameSession) {
					registered.Add(1)
				},
				BotFillDelay: time.Hour,
			})
			t.Cleanup(func() { require.NoError(t, matcher.Close()) })

			addThreeToMatcher(t, matcher, clients)
			waitSignal(t, beginCalled, "BeginRoom")
			for _, client := range clients {
				client.waitForMessage(t, protocol.MsgMatchCancelled)
			}
			if assembly != nil {
				waitSignal(t, assembly.rollbackDone, "RoomAssembly.Rollback")
				require.Equal(t, testCase.wantRollback, assembly.rollbackCalls.Load())
			}

			for _, client := range clients {
				require.False(t, client.hasMessage(protocol.MsgRoomJoined))
				require.Empty(t, client.GetRoom())
			}
			require.Zero(t, registered.Load(), "a rolled-back match must not register a GameSession")

			// Whether a recoverable infrastructure failure requeues a player or
			// terminates the attempt, no stale inflight entry may reject a new one.
			for _, client := range clients {
				matcher.RemoveFromQueue(client)
			}
			for _, client := range clients {
				require.True(t, matcher.AddToQueue(client), "player %s remained inflight", client.GetID())
				require.True(t, matcher.RemoveFromQueue(client))
			}
		})
	}
}

func TestMatcherRemoveFromQueueCancelsInflightTransaction(t *testing.T) {
	clients := newMatcherClients("cancel", 3)
	registry := newActiveClientRegistry(clients...)
	assemblyReady := make(chan *scriptedRoomAssembly, 1)
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(_ context.Context, first types.ClientInterface) (RoomAssembly, error) {
			assembly := newScriptedRoomAssembly(first)
			assembly.blockJoinAt = 1
			assemblyReady <- assembly
			return assembly, nil
		},
		BotFillDelay: time.Hour,
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	addThreeToMatcher(t, matcher, clients)
	assembly := waitValue(t, assemblyReady, "room assembly")
	require.Equal(t, 1, waitValue(t, assembly.joinStarted, "second-player Join"))
	require.True(t, matcher.RemoveFromQueue(clients[0]), "CancelMatch must cancel inflight work")
	waitSignal(t, assembly.rollbackDone, "RoomAssembly.Rollback")
	require.EqualValues(t, 1, assembly.rollbackCalls.Load())
	for _, client := range clients {
		require.False(t, client.hasMessage(protocol.MsgRoomJoined))
		require.Empty(t, client.GetRoom())
	}

	for _, client := range clients[1:] {
		matcher.RemoveFromQueue(client)
	}
	require.True(t, matcher.AddToQueue(clients[0]), "cancel must clear the initiator's inflight state")
	require.True(t, matcher.RemoveFromQueue(clients[0]))
}

func TestMatcherPlayerDisconnectedRemovesQueuedEntry(t *testing.T) {
	client := newMatcherClient("queued-disconnect", false)
	registry := newActiveClientRegistry(client)
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(context.Context, types.ClientInterface) (RoomAssembly, error) {
			t.Fatal("a disconnected single player must not begin room assembly")
			return nil, errInjectedMatchAssembly
		},
		BotFillDelay: time.Hour,
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	require.True(t, matcher.AddToQueue(client))
	client.waitForMessage(t, protocol.MsgMatchQueued)
	registry.Remove(client.GetID())
	matcher.PlayerDisconnected(client)
	require.Zero(t, matcher.GetQueueLength())
	require.False(t, matcher.RemoveFromQueue(client))

	registry.Set(client)
	require.True(t, matcher.AddToQueue(client), "disconnect left a stale queue state")
	require.True(t, matcher.RemoveFromQueue(client))
}

func TestMatcherDisconnectCancelsInflightAtTransactionBoundaries(t *testing.T) {
	testCases := []struct {
		name        string
		blockJoinAt int
		blockCommit bool
		victim      int
	}{
		{name: "while second player joins", blockJoinAt: 1, victim: 0},
		{name: "after second before third joins", blockJoinAt: 2, victim: 1},
		{name: "after all joins before commit", blockCommit: true, victim: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			clients := newMatcherClients("disconnect", 3)
			registry := newActiveClientRegistry(clients...)
			assemblyReady := make(chan *scriptedRoomAssembly, 1)
			matcher := NewMatcher(MatcherDeps{
				QueueTimeout:        time.Hour,
				ResolveActiveClient: registry.Resolve,
				BeginRoom: func(_ context.Context, first types.ClientInterface) (RoomAssembly, error) {
					assembly := newScriptedRoomAssembly(first)
					assembly.blockJoinAt = testCase.blockJoinAt
					assembly.blockCommit = testCase.blockCommit
					assemblyReady <- assembly
					return assembly, nil
				},
				BotFillDelay: time.Hour,
			})
			t.Cleanup(func() { require.NoError(t, matcher.Close()) })

			addThreeToMatcher(t, matcher, clients)
			assembly := waitValue(t, assemblyReady, "room assembly")
			if testCase.blockCommit {
				waitSignal(t, assembly.commitStarted, "Commit")
			} else {
				waitForJoinCall(t, assembly.joinStarted, testCase.blockJoinAt)
			}
			for _, client := range clients {
				require.False(t, client.hasMessage(protocol.MsgRoomJoined), "RoomJoined was sent before commit")
			}

			victim := clients[testCase.victim]
			registry.Remove(victim.GetID())
			matcher.PlayerDisconnected(victim)
			waitSignal(t, assembly.rollbackDone, "RoomAssembly.Rollback")
			require.EqualValues(t, 1, assembly.rollbackCalls.Load())
			for _, client := range clients {
				require.False(t, client.hasMessage(protocol.MsgRoomJoined))
				require.Empty(t, client.GetRoom())
			}

			for _, client := range clients {
				matcher.RemoveFromQueue(client)
			}
			registry.Set(victim)
			require.True(t, matcher.AddToQueue(victim), "disconnect must clear inflight membership")
			require.True(t, matcher.RemoveFromQueue(victim))
		})
	}
}

func TestMatcherReplaceClientUsesLatestGeneration(t *testing.T) {
	oldClient := newMatcherClient("reconnecting", false)
	newClient := newMatcherClient("reconnecting", false)
	peers := newMatcherClients("reconnect-peer", 2)
	registry := newActiveClientRegistry(oldClient, peers[0], peers[1])
	beginOwner := make(chan types.ClientInterface, 1)
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(_ context.Context, first types.ClientInterface) (RoomAssembly, error) {
			beginOwner <- first
			return nil, errInjectedMatchAssembly
		},
		BotFillDelay: time.Hour,
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	require.True(t, matcher.AddToQueue(oldClient))
	registry.Set(newClient)
	matcher.ReplaceClient(oldClient, newClient)
	require.True(t, matcher.AddToQueue(peers[0]))
	require.True(t, matcher.AddToQueue(peers[1]))

	require.Same(t, newClient, waitValue(t, beginOwner, "BeginRoom owner"))
	newClient.waitForMessage(t, protocol.MsgMatchCancelled)
	require.False(t, oldClient.hasMessage(protocol.MsgRoomJoined))
	require.False(t, oldClient.hasMessage(protocol.MsgMatchCancelled), "stale generation received transaction delivery")
}

func TestMatcherBotFillLosesCleanlyToConcurrentHuman(t *testing.T) {
	humans := newMatcherClients("human", 3)
	registry := newActiveClientRegistry(humans...)
	botClient := newMatcherClient("unused-bot", true)
	factoryStarted := make(chan struct{})
	factoryRelease := make(chan struct{})
	assemblyReady := make(chan *scriptedRoomAssembly, 1)
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(_ context.Context, first types.ClientInterface) (RoomAssembly, error) {
			assembly := newScriptedRoomAssembly(first)
			assembly.failCommit = true
			assemblyReady <- assembly
			return assembly, nil
		},
		BotFactory: func(bot.DecisionEngine) types.ClientInterface {
			close(factoryStarted)
			<-factoryRelease
			return botClient
		},
		BotFillDelay: 10 * time.Millisecond,
		BotEngine:    bot.NewHeuristicEngine(),
		BotConfig:    config.BotConfig{Enabled: true},
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	require.True(t, matcher.AddToQueue(humans[0]))
	require.True(t, matcher.AddToQueue(humans[1]))
	waitSignal(t, factoryStarted, "BotFactory")

	addResult := make(chan bool, 1)
	go func() { addResult <- matcher.AddToQueue(humans[2]) }()
	select {
	case accepted := <-addResult:
		require.True(t, accepted)
	case <-time.After(matcherTestDeadline):
		close(factoryRelease)
		<-addResult
		t.Fatal("BotFactory held the matcher lock and excluded a ready human")
	}
	close(factoryRelease)

	assembly := waitValue(t, assemblyReady, "human room assembly")
	waitSignal(t, assembly.rollbackDone, "RoomAssembly.Rollback")
	require.Equal(t, []string{humans[0].GetID(), humans[1].GetID(), humans[2].GetID()}, assembly.participantIDs())
	waitSignal(t, botClient.closed, "discarded bot Close")
	require.False(t, botClient.hasMessage(protocol.MsgRoomJoined))
}

func TestMatcherCloseCancelsInflightAndWaitsForRollback(t *testing.T) {
	clients := newMatcherClients("shutdown", 3)
	registry := newActiveClientRegistry(clients...)
	assemblyReady := make(chan *scriptedRoomAssembly, 1)
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(_ context.Context, first types.ClientInterface) (RoomAssembly, error) {
			assembly := newScriptedRoomAssembly(first)
			assembly.blockJoinAt = 1
			assemblyReady <- assembly
			return assembly, nil
		},
		BotFillDelay: time.Hour,
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	addThreeToMatcher(t, matcher, clients)
	assembly := waitValue(t, assemblyReady, "room assembly")
	require.Equal(t, 1, waitValue(t, assembly.joinStarted, "second-player Join"))

	closeResult := make(chan error, 1)
	go func() { closeResult <- matcher.Close() }()
	select {
	case err := <-closeResult:
		require.NoError(t, err)
	case <-time.After(matcherTestDeadline):
		t.Fatal("Matcher.Close did not cancel and wait for inflight room work")
	}
	require.EqualValues(t, 1, assembly.rollbackCalls.Load())
	select {
	case <-assembly.rollbackDone:
	default:
		t.Fatal("Matcher.Close returned before transaction rollback")
	}
	for _, client := range clients {
		require.Empty(t, client.GetRoom())
		require.False(t, client.hasMessage(protocol.MsgRoomJoined))
	}
	require.Zero(t, matcher.GetQueueLength())
	require.False(t, matcher.AddToQueue(newMatcherClient("after-close", false)))
}

func TestMatcherInflightDeadlineRollsBackAllParticipants(t *testing.T) {
	clients := newMatcherClients("inflight-timeout", 3)
	registry := newActiveClientRegistry(clients...)
	assemblyReady := make(chan *scriptedRoomAssembly, 1)
	matcher := NewMatcher(MatcherDeps{
		QueueTimeout:        30 * time.Millisecond,
		ResolveActiveClient: registry.Resolve,
		BeginRoom: func(_ context.Context, first types.ClientInterface) (RoomAssembly, error) {
			assembly := newScriptedRoomAssembly(first)
			assembly.blockJoinAt = 1
			assemblyReady <- assembly
			return assembly, nil
		},
		BotFillDelay: time.Hour,
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	addThreeToMatcher(t, matcher, clients)
	assembly := waitValue(t, assemblyReady, "room assembly")
	require.Equal(t, 1, waitValue(t, assembly.joinStarted, "second-player Join"))
	for _, client := range clients {
		cancelled := client.waitForMessage(t, protocol.MsgMatchCancelled)
		payload, err := codec.ParsePayload[protocol.MatchCancelledPayload](cancelled)
		require.NoError(t, err)
		require.Equal(t, "timeout", payload.Reason)
	}
	waitSignal(t, assembly.rollbackDone, "deadline rollback")
	for _, client := range clients {
		require.Empty(t, client.GetRoom())
		require.False(t, client.hasMessage(protocol.MsgRoomJoined))
	}
}

func TestMatcherDefaultRoomTransactionCommitsCompleteRoster(t *testing.T) {
	clients := newMatcherClients("commit", 3)
	registry := newActiveClientRegistry(clients...)
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	registered := make(chan *session.GameSession, 1)
	matcher := NewMatcher(MatcherDeps{
		RoomManager:         roomManager,
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		GameConfig: config.GameConfig{
			TurnTimeout: 3600,
			BidTimeout:  3600,
		},
		RegisterSession: func(_ string, game *session.GameSession) { registered <- game },
		BotFillDelay:    time.Hour,
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	addThreeToMatcher(t, matcher, clients)
	var roomCode string
	for _, client := range clients {
		joined := client.waitForMessage(t, protocol.MsgRoomJoined)
		payload, err := codec.ParsePayload[protocol.RoomJoinedPayload](joined)
		require.NoError(t, err)
		if roomCode == "" {
			roomCode = payload.RoomCode
		}
		require.Equal(t, roomCode, payload.RoomCode)
		require.Len(t, payload.Players, 3)
		require.Equal(t, roomCode, client.GetRoom())
	}
	game := waitValue(t, registered, "registered game session")
	t.Cleanup(game.StopAllTimers)
	clients[0].waitForMessage(t, protocol.MsgGameStart)
	committed := roomManager.GetRoom(roomCode)
	require.NotNil(t, committed)
	require.Len(t, committed.SnapshotPlayers(), 3)
	require.Equal(t, room.RoomStateBidding, committed.State())
	for _, client := range clients {
		require.False(t, matcher.RemoveFromQueue(client), "committed matches cannot be cancelled")
	}
}

type roomJoinedBarrierClient struct {
	*matcherClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (client *roomJoinedBarrierClient) SendMessage(message *protocol.Message) error {
	err := client.matcherClient.SendMessage(message)
	if message.Type == protocol.MsgRoomJoined {
		client.once.Do(func() { close(client.entered) })
		<-client.release
	}
	return err
}

func TestMatcherPostCommitDisconnectIsCarriedIntoRegisteredSession(t *testing.T) {
	first := &roomJoinedBarrierClient{
		matcherClient: newMatcherClient("committed-1", false),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	peers := newMatcherClients("committed-peer", 2)
	registry := &activeClientRegistry{clients: make(map[string]types.ClientInterface)}
	registry.Set(first)
	registry.Set(peers[0])
	registry.Set(peers[1])
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	registered := make(chan *session.GameSession, 1)
	matcher := NewMatcher(MatcherDeps{
		RoomManager:         roomManager,
		QueueTimeout:        time.Hour,
		ResolveActiveClient: registry.Resolve,
		GameConfig:          config.GameConfig{TurnTimeout: 3600, BidTimeout: 3600},
		RegisterSession:     func(_ string, game *session.GameSession) { registered <- game },
		BotFillDelay:        time.Hour,
	})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	require.True(t, matcher.AddToQueue(first))
	require.True(t, matcher.AddToQueue(peers[0]))
	require.True(t, matcher.AddToQueue(peers[1]))
	waitSignal(t, first.entered, "RoomJoined delivery after logical commit")
	require.False(t, matcher.RemoveFromQueue(first))
	registry.Remove(first.GetID())
	require.False(t, matcher.PlayerDisconnected(first), "committed work no longer belongs to the queue")
	roomManager.NotifyPlayerOffline(first)
	close(first.release)
	game := waitValue(t, registered, "registered committed game")
	t.Cleanup(game.StopAllTimers)
	for _, client := range peers {
		client.waitForMessage(t, protocol.MsgRoomJoined)
	}
	require.NotEmpty(t, first.GetRoom())
	snapshot := game.BuildGameStateDTO(first.GetID(), nil)
	for _, player := range snapshot.Players {
		if player.ID == first.GetID() {
			require.False(t, player.Online)
			return
		}
	}
	t.Fatalf("registered session omitted committed player %s", first.GetID())
}

type matcherClient struct {
	id    string
	name  string
	isBot bool

	mu       sync.Mutex
	roomCode string
	messages []*protocol.Message
	messageC chan *protocol.Message
	closed   chan struct{}
	closeOne sync.Once
}

func newMatcherClient(id string, isBot bool) *matcherClient {
	return &matcherClient{
		id:       id,
		name:     id,
		isBot:    isBot,
		messageC: make(chan *protocol.Message, 64),
		closed:   make(chan struct{}),
	}
}

func newMatcherClients(prefix string, count int) []*matcherClient {
	clients := make([]*matcherClient, count)
	for index := range clients {
		clients[index] = newMatcherClient(fmt.Sprintf("%s-%d", prefix, index+1), false)
	}
	return clients
}

func (client *matcherClient) GetID() string   { return client.id }
func (client *matcherClient) GetName() string { return client.name }
func (client *matcherClient) IsBot() bool     { return client.isBot }

func (client *matcherClient) GetRoom() string {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.roomCode
}

func (client *matcherClient) SetRoom(code string) {
	client.mu.Lock()
	client.roomCode = code
	client.mu.Unlock()
}

func (client *matcherClient) SendMessage(message *protocol.Message) error {
	client.mu.Lock()
	client.messages = append(client.messages, message)
	client.mu.Unlock()
	select {
	case client.messageC <- message:
	default:
	}
	return nil
}

func (client *matcherClient) Close() {
	client.closeOne.Do(func() { close(client.closed) })
}

func (client *matcherClient) hasMessage(messageType protocol.MessageType) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	for _, message := range client.messages {
		if message.Type == messageType {
			return true
		}
	}
	return false
}

func (client *matcherClient) waitForMessage(t *testing.T, messageType protocol.MessageType) *protocol.Message {
	t.Helper()
	deadline := time.NewTimer(matcherTestDeadline)
	defer deadline.Stop()
	for {
		select {
		case message := <-client.messageC:
			if message.Type == messageType {
				return message
			}
		case <-deadline.C:
			t.Fatalf("client %s did not receive %s", client.id, messageType)
			return nil
		}
	}
}

type activeClientRegistry struct {
	mu      sync.RWMutex
	clients map[string]types.ClientInterface
}

func newActiveClientRegistry(clients ...*matcherClient) *activeClientRegistry {
	registry := &activeClientRegistry{clients: make(map[string]types.ClientInterface, len(clients))}
	for _, client := range clients {
		registry.clients[client.GetID()] = client
	}
	return registry
}

func (registry *activeClientRegistry) Resolve(playerID string) types.ClientInterface {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.clients[playerID]
}

func (registry *activeClientRegistry) Set(client types.ClientInterface) {
	registry.mu.Lock()
	registry.clients[client.GetID()] = client
	registry.mu.Unlock()
}

func (registry *activeClientRegistry) Remove(playerID string) {
	registry.mu.Lock()
	delete(registry.clients, playerID)
	registry.mu.Unlock()
}

type scriptedRoomAssembly struct {
	gameRoom *room.Room

	mu            sync.Mutex
	participants  []types.ClientInterface
	joinCalls     int
	failJoinAt    int
	failCommit    bool
	blockJoinAt   int
	blockCommit   bool
	joinStarted   chan int
	commitStarted chan struct{}
	commitOnce    sync.Once
	rollbackDone  chan struct{}
	rollbackOnce  sync.Once
	rollbackCalls atomic.Int32
}

func newScriptedRoomAssembly(first types.ClientInterface) *scriptedRoomAssembly {
	return &scriptedRoomAssembly{
		gameRoom:      room.NewMockRoom("transaction-room", first),
		participants:  []types.ClientInterface{first},
		joinStarted:   make(chan int, 2),
		commitStarted: make(chan struct{}),
		rollbackDone:  make(chan struct{}),
	}
}

func (assembly *scriptedRoomAssembly) Room() *room.Room { return assembly.gameRoom }

func (assembly *scriptedRoomAssembly) Join(ctx context.Context, client types.ClientInterface) error {
	assembly.mu.Lock()
	assembly.joinCalls++
	joinCall := assembly.joinCalls
	assembly.mu.Unlock()
	assembly.joinStarted <- joinCall

	if assembly.blockJoinAt == joinCall {
		<-ctx.Done()
		return ctx.Err()
	}
	if assembly.failJoinAt == joinCall {
		return errInjectedMatchAssembly
	}

	assembly.gameRoom.AddPlayerForTest(client, joinCall, false)
	assembly.mu.Lock()
	assembly.participants = append(assembly.participants, client)
	assembly.mu.Unlock()
	return nil
}

func (assembly *scriptedRoomAssembly) Commit(ctx context.Context) error {
	assembly.commitOnce.Do(func() { close(assembly.commitStarted) })
	if assembly.blockCommit {
		<-ctx.Done()
		return ctx.Err()
	}
	if assembly.failCommit {
		return errInjectedMatchAssembly
	}

	assembly.mu.Lock()
	participants := append([]types.ClientInterface(nil), assembly.participants...)
	assembly.mu.Unlock()
	for _, participant := range participants {
		participant.SetRoom(assembly.gameRoom.Code)
	}
	return nil
}

func (assembly *scriptedRoomAssembly) Rollback() error {
	assembly.rollbackCalls.Add(1)
	assembly.mu.Lock()
	participants := append([]types.ClientInterface(nil), assembly.participants...)
	assembly.mu.Unlock()
	for _, participant := range participants {
		if participant.GetRoom() == assembly.gameRoom.Code {
			participant.SetRoom("")
		}
	}
	assembly.rollbackOnce.Do(func() { close(assembly.rollbackDone) })
	return nil
}

func (assembly *scriptedRoomAssembly) participantIDs() []string {
	assembly.mu.Lock()
	defer assembly.mu.Unlock()
	ids := make([]string, len(assembly.participants))
	for index, participant := range assembly.participants {
		ids[index] = participant.GetID()
	}
	return ids
}

func addThreeToMatcher(t *testing.T, matcher *Matcher, clients []*matcherClient) {
	t.Helper()
	require.Len(t, clients, 3)
	for _, client := range clients {
		require.True(t, matcher.AddToQueue(client), "failed to queue %s", client.GetID())
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(matcherTestDeadline):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitValue[T any](t *testing.T, values <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(matcherTestDeadline):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func waitForJoinCall(t *testing.T, calls <-chan int, target int) {
	t.Helper()
	for {
		call := waitValue(t, calls, fmt.Sprintf("Join call %d", target))
		if call == target {
			return
		}
		require.Less(t, call, target, "Join calls arrived out of order")
	}
}
