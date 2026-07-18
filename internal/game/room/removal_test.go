package room

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

type conditionalTimeoutClient struct {
	*concurrencyClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type roomEventBarrierClient struct {
	*concurrencyClient
	blockType protocol.MessageType
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
	messages  []protocol.MessageType
	messageMu sync.Mutex
}

func (client *roomEventBarrierClient) SendMessageIfRoom(expectedRoom string, message *protocol.Message) (bool, error) {
	client.mu.RLock()
	if client.roomCode != expectedRoom {
		client.mu.RUnlock()
		return false, nil
	}
	if message.Type == client.blockType {
		client.once.Do(func() { close(client.entered) })
		<-client.release
	}
	client.messageMu.Lock()
	client.messages = append(client.messages, message.Type)
	client.messageMu.Unlock()
	client.mu.RUnlock()
	return true, nil
}

func (client *roomEventBarrierClient) messageTypes() []protocol.MessageType {
	client.messageMu.Lock()
	defer client.messageMu.Unlock()
	return append([]protocol.MessageType(nil), client.messages...)
}

func (client *conditionalTimeoutClient) SendMessageIfRoom(expectedRoom string, _ *protocol.Message) (bool, error) {
	client.mu.RLock()
	defer client.mu.RUnlock()
	if client.roomCode != expectedRoom {
		return false, nil
	}
	client.once.Do(func() { close(client.entered) })
	<-client.release
	client.sends.Add(1)
	return true, nil
}

type removalBotClient struct {
	*concurrencyClient
	closeCount atomic.Int32
}

func waitRemovalValue[T any](t *testing.T, values <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func newRemovalBotClient(id string) *removalBotClient {
	return &removalBotClient{concurrencyClient: newConcurrencyClient(id)}
}

func (client *removalBotClient) IsBot() bool { return true }
func (client *removalBotClient) Close()      { client.closeCount.Add(1) }

type blockingRemovalBot struct {
	*concurrencyClient
	closeEntered chan struct{}
	releaseClose chan struct{}
	closeOnce    sync.Once
}

func newBlockingRemovalBot(id string) *blockingRemovalBot {
	return &blockingRemovalBot{
		concurrencyClient: newConcurrencyClient(id),
		closeEntered:      make(chan struct{}),
		releaseClose:      make(chan struct{}),
	}
}

func (client *blockingRemovalBot) IsBot() bool { return true }
func (client *blockingRemovalBot) Close() {
	client.closeOnce.Do(func() { close(client.closeEntered) })
	<-client.releaseClose
}

func TestRoomRemovalIsExactOnceAndOutsideOwnershipLocks(t *testing.T) {
	rm := newMatchTransactionManager()
	botClient := newRemovalBotClient("last-bot")
	gameRoom, err := rm.CreateRoom(botClient)
	require.NoError(t, err)

	var calls atomic.Int32
	rm.SetOnRoomRemoved(func(removed RoomRemoval) {
		calls.Add(1)
		require.Equal(t, gameRoom.Code, removed.Code)
		require.Same(t, gameRoom, removed.Room)
		require.Equal(t, RoomRemovalLeft, removed.Reason)
		require.Len(t, removed.Players, 1)

		require.True(t, rm.mu.TryLock(), "callback ran while RoomManager.mu was held")
		rm.mu.Unlock()
		require.True(t, gameRoom.mu.TryLock(), "callback ran while Room.mu was held")
		gameRoom.mu.Unlock()
	})

	require.True(t, rm.LeaveRoom(botClient))
	require.False(t, rm.LeaveRoom(botClient))
	require.EqualValues(t, 1, calls.Load())
	require.EqualValues(t, 1, botClient.closeCount.Load())
	require.Empty(t, botClient.GetRoom())
	require.Nil(t, rm.GetRoom(gameRoom.Code))
}

func TestLeaveRoomRetiresWaitingRoomWhenOnlyBotsRemain(t *testing.T) {
	rm := newMatchTransactionManager()
	human := newConcurrencyClient("leaving-human")
	firstBot := newRemovalBotClient("first-bot")
	secondBot := newRemovalBotClient("second-bot")
	gameRoom, err := rm.CreateRoom(human)
	require.NoError(t, err)
	_, err = rm.JoinRoom(firstBot, gameRoom.Code)
	require.NoError(t, err)
	_, err = rm.JoinRoom(secondBot, gameRoom.Code)
	require.NoError(t, err)

	var calls atomic.Int32
	var removal RoomRemoval
	rm.SetOnRoomRemoved(func(removed RoomRemoval) {
		calls.Add(1)
		removal = removed
	})

	require.True(t, rm.LeaveRoom(human))
	require.False(t, rm.LeaveRoom(human))
	require.False(t, rm.RemoveRoom(gameRoom, RoomRemovalRollback))
	require.EqualValues(t, 1, calls.Load())
	require.Same(t, gameRoom, removal.Room)
	require.Equal(t, RoomRemovalLeft, removal.Reason)
	require.Len(t, removal.Players, 3)
	require.Nil(t, rm.GetRoom(gameRoom.Code))
	for _, client := range []types.ClientInterface{human, firstBot, secondBot} {
		require.Empty(t, client.GetRoom())
	}
	require.EqualValues(t, 1, firstBot.closeCount.Load())
	require.EqualValues(t, 1, secondBot.closeCount.Load())
}

func TestLeaveRoomKeepsWaitingRoomWhenHumanRemains(t *testing.T) {
	rm := newMatchTransactionManager()
	leavingHuman := newConcurrencyClient("leaving-human")
	remainingHuman := newConcurrencyClient("remaining-human")
	botClient := newRemovalBotClient("remaining-bot")
	gameRoom, err := rm.CreateRoom(leavingHuman)
	require.NoError(t, err)
	_, err = rm.JoinRoom(remainingHuman, gameRoom.Code)
	require.NoError(t, err)
	_, err = rm.JoinRoom(botClient, gameRoom.Code)
	require.NoError(t, err)

	var calls atomic.Int32
	rm.SetOnRoomRemoved(func(RoomRemoval) { calls.Add(1) })

	require.True(t, rm.LeaveRoom(leavingHuman))
	require.Zero(t, calls.Load())
	require.Same(t, gameRoom, rm.GetRoom(gameRoom.Code))
	require.Empty(t, leavingHuman.GetRoom())
	require.Equal(t, gameRoom.Code, remainingHuman.GetRoom())
	require.Equal(t, gameRoom.Code, botClient.GetRoom())
	require.Len(t, gameRoom.SnapshotPlayers(), 2)
	require.Zero(t, botClient.closeCount.Load())
}

func TestTimeoutDispatchesLifecycleBeforeBotCloseAndHumanNotification(t *testing.T) {
	rm := newMatchTransactionManager()
	human := newConcurrencyClient("timeout-human")
	botClient := newBlockingRemovalBot("timeout-bot")
	gameRoom, err := rm.CreateRoom(human)
	require.NoError(t, err)
	_, err = rm.JoinRoom(botClient, gameRoom.Code)
	require.NoError(t, err)
	gameRoom.SetCreatedAtForTest(time.Now().Add(-2 * time.Hour))
	human.sends.Store(0)
	botClient.sends.Store(0)

	var lifecycleDispatched atomic.Bool
	rm.SetOnRoomRemoved(func(removed RoomRemoval) {
		require.Same(t, gameRoom, removed.Room)
		lifecycleDispatched.Store(true)
	})
	cleanupDone := make(chan struct{})
	go func() {
		rm.cleanup()
		close(cleanupDone)
	}()
	select {
	case <-botClient.closeEntered:
	case <-time.After(time.Second):
		close(botClient.releaseClose)
		t.Fatal("timed out waiting for Bot close")
	}
	callbackBeforeClose := lifecycleDispatched.Load()
	humanSendsWhileCloseBlocked := human.sends.Load()
	botSendsWhileCloseBlocked := botClient.sends.Load()
	close(botClient.releaseClose)
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for room cleanup")
	}

	require.True(t, callbackBeforeClose, "lifecycle callback must run before a potentially blocking Bot.Close")
	require.Zero(t, humanSendsWhileCloseBlocked, "timeout delivery must wait until lifecycle dispatch completes")
	require.Zero(t, botSendsWhileCloseBlocked)
	require.EqualValues(t, 1, human.sends.Load())
	require.Zero(t, botClient.sends.Load(), "closed bots must not receive timeout errors")
}

func TestTimeoutNotificationIsAtomicWithReplacementRoomBinding(t *testing.T) {
	rm := newMatchTransactionManager()
	client := &conditionalTimeoutClient{
		concurrencyClient: newConcurrencyClient("timeout-replacement"),
		entered:           make(chan struct{}),
		release:           make(chan struct{}),
	}
	gameRoom, err := rm.CreateRoom(client)
	require.NoError(t, err)
	gameRoom.SetCreatedAtForTest(time.Now().Add(-2 * time.Hour))

	cleanupDone := make(chan struct{})
	go func() {
		rm.cleanup()
		close(cleanupDone)
	}()
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		close(client.release)
		t.Fatal("timed out waiting for conditional timeout delivery")
	}

	bindDone := make(chan struct{})
	go func() {
		client.SetRoom("replacement-room")
		close(bindDone)
	}()
	select {
	case <-bindDone:
		close(client.release)
		t.Fatal("replacement binding passed the terminal delivery boundary")
	default:
	}

	close(client.release)
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cleanup")
	}
	select {
	case <-bindDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement binding")
	}
	require.EqualValues(t, 1, client.sends.Load())
	require.Equal(t, "replacement-room", client.GetRoom())
}

func TestReconnectEventQuiescesBeforeRoomRemovalTerminal(t *testing.T) {
	rm := newMatchTransactionManager()
	host := &roomEventBarrierClient{
		concurrencyClient: newConcurrencyClient("event-host"),
		blockType:         protocol.MsgPlayerOnline,
		entered:           make(chan struct{}),
		release:           make(chan struct{}),
	}
	peer := newConcurrencyClient("event-peer")
	gameRoom, err := rm.CreateRoom(host)
	require.NoError(t, err)
	_, err = rm.JoinRoom(peer, gameRoom.Code)
	require.NoError(t, err)
	rm.NotifyPlayerOffline(peer)
	host.messageMu.Lock()
	host.messages = nil
	host.messageMu.Unlock()

	var terminalSent bool
	var terminalErr error
	rm.SetOnRoomRemoved(func(removal RoomRemoval) {
		terminalSent, terminalErr = host.SendMessageIfRoom("", &protocol.Message{Type: protocol.MsgRoomLeft})
	})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(host.release) }) })
	replacement := newConcurrencyClient(peer.GetID())
	reconnected := make(chan error, 1)
	go func() { reconnected <- rm.ReconnectPlayer(peer.GetID(), gameRoom.Code, replacement) }()
	select {
	case <-host.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PlayerOnline delivery")
	}

	removed := make(chan bool, 1)
	go func() { removed <- rm.RemoveRoom(gameRoom, RoomRemovalRollback) }()
	select {
	case <-removed:
		t.Fatal("room removal passed a blocked current-room event")
	default:
	}
	releaseOnce.Do(func() { close(host.release) })
	require.NoError(t, waitRemovalValue(t, reconnected, "ReconnectPlayer completion"))
	require.True(t, waitRemovalValue(t, removed, "room removal completion"))
	require.NoError(t, terminalErr)
	require.True(t, terminalSent)
	require.Equal(t, []protocol.MessageType{protocol.MsgPlayerOnline, protocol.MsgRoomLeft}, host.messageTypes())
}

func TestSendIfCurrentRoomRejectsRemovedAndReusedIdentity(t *testing.T) {
	rm := newMatchTransactionManager()
	client := newConcurrencyClient("stale-room-result")
	oldRoom, err := rm.CreateRoom(client)
	require.NoError(t, err)
	require.True(t, rm.RemoveRoom(oldRoom, RoomRemovalRollback))

	replacement := NewMockRoom(oldRoom.Code, client)
	client.SetRoom(oldRoom.Code)
	rm.AddRoomForTest(replacement)
	sent, err := rm.SendIfCurrentRoom(oldRoom, client, &protocol.Message{Type: protocol.MsgRoomJoined})
	require.NoError(t, err)
	require.False(t, sent)
	require.Zero(t, client.sends.Load())
}

func TestConcurrentRemovalPathsDispatchExactlyOnce(t *testing.T) {
	rm := newMatchTransactionManager()
	botClient := newRemovalBotClient("concurrent-bot")
	gameRoom, err := rm.CreateRoom(botClient)
	require.NoError(t, err)
	gameRoom.SetCreatedAtForTest(time.Now().Add(-2 * time.Hour))

	var callbacks atomic.Int32
	rm.SetOnRoomRemoved(func(removed RoomRemoval) {
		require.Same(t, gameRoom, removed.Room)
		callbacks.Add(1)
	})

	var workers sync.WaitGroup
	for index := range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			switch index % 4 {
			case 0:
				rm.LeaveRoom(botClient)
			case 1:
				rm.NotifyPlayerOffline(botClient)
			case 2:
				rm.RemoveRoom(gameRoom, RoomRemovalRollback)
			case 3:
				rm.cleanup()
			}
		}()
	}
	workers.Wait()

	require.Nil(t, rm.GetRoom(gameRoom.Code))
	require.EqualValues(t, 1, callbacks.Load())
	require.EqualValues(t, 1, botClient.closeCount.Load())
}

func TestEveryPublishedRoomRemovalPathDeletesRedisAndNotifiesOnce(t *testing.T) {
	tests := []struct {
		name   string
		remove func(*testing.T, *RoomManager) *Room
	}{
		{
			name: "last player leaves",
			remove: func(t *testing.T, rm *RoomManager) *Room {
				t.Helper()

				client := newConcurrencyClient("leave")
				gameRoom, err := rm.CreateRoom(client)
				require.NoError(t, err)
				require.NoError(t, rm.redisStore.SaveRoom(context.Background(), gameRoom.Code, gameRoom.ToRoomData()))
				require.True(t, rm.LeaveRoom(client))
				return gameRoom
			},
		},
		{
			name: "all players offline",
			remove: func(t *testing.T, rm *RoomManager) *Room {
				t.Helper()

				client := newConcurrencyClient("offline")
				gameRoom, err := rm.CreateRoom(client)
				require.NoError(t, err)
				require.NoError(t, rm.redisStore.SaveRoom(context.Background(), gameRoom.Code, gameRoom.ToRoomData()))
				rm.NotifyPlayerOffline(client)
				return gameRoom
			},
		},
		{
			name: "waiting room timeout",
			remove: func(t *testing.T, rm *RoomManager) *Room {
				t.Helper()

				client := newConcurrencyClient("timeout")
				gameRoom, err := rm.CreateRoom(client)
				require.NoError(t, err)
				gameRoom.SetCreatedAtForTest(time.Now().Add(-2 * time.Hour))
				require.NoError(t, rm.redisStore.SaveRoom(context.Background(), gameRoom.Code, gameRoom.ToRoomData()))
				rm.cleanup()
				return gameRoom
			},
		},
		{
			name: "published match rollback",
			remove: func(t *testing.T, rm *RoomManager) *Room {
				t.Helper()

				tx, _ := beginCompleteMatchRoom(t, rm)
				gameRoom, err := tx.Commit()
				require.NoError(t, err)
				require.NoError(t, rm.redisStore.SaveRoom(context.Background(), gameRoom.Code, gameRoom.ToRoomData()))
				tx.Rollback()
				tx.Rollback()
				return gameRoom
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mini, err := miniredis.Run()
			require.NoError(t, err)
			t.Cleanup(mini.Close)
			redisClient := redis.NewClient(&redis.Options{Addr: mini.Addr()})
			t.Cleanup(func() { require.NoError(t, redisClient.Close()) })

			rm := &RoomManager{
				redisStore:   storage.NewRedisStore(redisClient),
				roomTimeout:  time.Hour,
				gameConfig:   config.GameConfig{RoomTimeout: 10},
				rooms:        make(map[string]*Room),
				pendingRooms: make(map[string]*MatchRoomTransaction),
			}
			var removals []RoomRemoval
			rm.SetOnRoomRemoved(func(removed RoomRemoval) { removals = append(removals, removed) })

			gameRoom := test.remove(t, rm)
			require.Len(t, removals, 1)
			require.Same(t, gameRoom, removals[0].Room)
			require.Nil(t, rm.GetRoom(gameRoom.Code))
			require.Eventually(t, func() bool {
				exists, existsErr := redisClient.Exists(context.Background(), "room:"+gameRoom.Code).Result()
				return existsErr == nil && exists == 0
			}, time.Second, time.Millisecond)
		})
	}
}

func TestStaleMatchRollbackCannotRemoveReusedRoomCode(t *testing.T) {
	mini, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mini.Close)
	redisClient := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })

	rm := &RoomManager{
		redisStore:   storage.NewRedisStore(redisClient),
		roomTimeout:  time.Hour,
		gameConfig:   config.GameConfig{RoomTimeout: 10},
		rooms:        make(map[string]*Room),
		pendingRooms: make(map[string]*MatchRoomTransaction),
	}
	tx, clients := beginCompleteMatchRoom(t, rm)
	oldRoom, err := tx.Commit()
	require.NoError(t, err)

	replacement := newRoom(oldRoom.Code, time.Now())
	rm.mu.Lock()
	rm.rooms[oldRoom.Code] = replacement
	rm.mu.Unlock()
	require.NoError(t, rm.redisStore.SaveRoom(context.Background(), replacement.Code, replacement.ToRoomData()))

	var calls atomic.Int32
	rm.SetOnRoomRemoved(func(RoomRemoval) { calls.Add(1) })
	tx.Rollback()

	require.Same(t, replacement, rm.GetRoom(replacement.Code))
	require.Zero(t, calls.Load())
	for _, client := range clients {
		require.Equal(t, replacement.Code, client.GetRoom(), "stale room identity cleared a reused room binding")
	}
	exists, err := redisClient.Exists(context.Background(), "room:"+replacement.Code).Result()
	require.NoError(t, err)
	require.EqualValues(t, 1, exists, "stale rollback deleted the replacement room's Redis state")
}

func TestRoomPersistenceSerializesBlockedSaveBeforeRemovalDelete(t *testing.T) {
	rm := newMatchTransactionManager()
	saveStarted := make(chan struct{})
	releaseSave := make(chan struct{})
	deleteStarted := make(chan struct{})
	var saveOnce sync.Once
	var deleteOnce sync.Once
	var saves atomic.Int32
	rm.saveRoomFunc = func(context.Context, string, *storage.RoomData) error {
		saves.Add(1)
		saveOnce.Do(func() { close(saveStarted) })
		<-releaseSave
		return nil
	}
	rm.deleteRoomFunc = func(context.Context, string) error {
		deleteOnce.Do(func() { close(deleteStarted) })
		return nil
	}

	client := newConcurrencyClient("save-delete")
	gameRoom, err := rm.CreateRoom(client)
	require.NoError(t, err)
	<-saveStarted
	require.True(t, rm.LeaveRoom(client))
	select {
	case <-deleteStarted:
		t.Fatal("room delete overtook the blocked authoritative save")
	default:
	}

	close(releaseSave)
	select {
	case <-deleteStarted:
	case <-time.After(time.Second):
		t.Fatal("queued room delete did not run after save completed")
	}
	require.Eventually(t, func() bool {
		rm.mu.RLock()
		_, retiring := rm.retiringRooms[gameRoom.Code]
		rm.mu.RUnlock()
		return !retiring
	}, time.Second, time.Millisecond)

	rm.PersistRoom(gameRoom)
	require.EqualValues(t, 1, saves.Load(), "a stale post-removal save was enqueued")
}

func TestRoomPersistenceCoalescesThousandsOfSavesBehindOneWorker(t *testing.T) {
	rm := newMatchTransactionManager()
	saveStarted := make(chan struct{})
	releaseSave := make(chan struct{})
	deleteDone := make(chan struct{})
	var saveOnce sync.Once
	var saves atomic.Int32
	rm.saveRoomFunc = func(context.Context, string, *storage.RoomData) error {
		saves.Add(1)
		saveOnce.Do(func() { close(saveStarted) })
		<-releaseSave
		return nil
	}
	rm.deleteRoomFunc = func(context.Context, string) error {
		close(deleteDone)
		return nil
	}

	baseline := runtime.NumGoroutine()
	client := newConcurrencyClient("coalesced")
	gameRoom, err := rm.CreateRoom(client)
	require.NoError(t, err)
	<-saveStarted

	for range 5000 {
		rm.PersistRoom(gameRoom)
	}
	rm.persistenceMu.Lock()
	require.Len(t, rm.persistenceQueues, 1)
	queue := rm.persistenceQueues[gameRoom.Code]
	require.NotNil(t, queue)
	require.NotNil(t, queue.pendingSave)
	require.Nil(t, queue.pendingDelete)
	rm.persistenceMu.Unlock()
	require.LessOrEqual(t, runtime.NumGoroutine(), baseline+3, "blocked Redis Save spawned per-operation goroutines")

	require.True(t, rm.LeaveRoom(client))
	rm.persistenceMu.Lock()
	require.Nil(t, queue.pendingSave, "Delete did not coalesce obsolete queued Saves")
	require.NotNil(t, queue.pendingDelete)
	rm.persistenceMu.Unlock()

	close(releaseSave)
	select {
	case <-deleteDone:
	case <-time.After(time.Second):
		t.Fatal("Delete did not run after the blocked Save completed")
	}
	require.EqualValues(t, 1, saves.Load())
	require.Eventually(t, func() bool {
		rm.persistenceMu.Lock()
		defer rm.persistenceMu.Unlock()
		return len(rm.persistenceQueues) == 0
	}, time.Second, time.Millisecond)
}

func TestRoomPersistenceReservesCodeWhileDeleteIsBlocked(t *testing.T) {
	rm := newMatchTransactionManager()
	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	var deleteOnce sync.Once
	rm.saveRoomFunc = func(context.Context, string, *storage.RoomData) error { return nil }
	rm.deleteRoomFunc = func(context.Context, string) error {
		deleteOnce.Do(func() { close(deleteStarted) })
		<-releaseDelete
		return nil
	}

	var generated atomic.Int32
	rm.roomCodeFunc = func() string {
		switch generated.Add(1) {
		case 1, 2, 4:
			return "111111"
		default:
			return "222222"
		}
	}

	oldClient := newConcurrencyClient("old")
	oldRoom, err := rm.CreateRoom(oldClient)
	require.NoError(t, err)
	require.Equal(t, "111111", oldRoom.Code)
	require.True(t, rm.LeaveRoom(oldClient))
	select {
	case <-deleteStarted:
	case <-time.After(time.Second):
		t.Fatal("room delete did not start")
	}

	replacement, err := rm.CreateRoom(newConcurrencyClient("replacement"))
	require.NoError(t, err)
	require.Equal(t, "222222", replacement.Code, "code was reused while its prior Redis delete was inflight")

	close(releaseDelete)
	require.Eventually(t, func() bool {
		rm.mu.RLock()
		_, retiring := rm.retiringRooms[oldRoom.Code]
		rm.mu.RUnlock()
		return !retiring
	}, time.Second, time.Millisecond)

	reused, err := rm.CreateRoom(newConcurrencyClient("reused"))
	require.NoError(t, err)
	require.Equal(t, oldRoom.Code, reused.Code, "code reservation was not released after delete completion")
}
