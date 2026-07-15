package room

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

func newMatchTransactionManager() *RoomManager {
	return &RoomManager{
		roomTimeout:  time.Hour,
		gameConfig:   config.GameConfig{RoomTimeout: 10},
		rooms:        make(map[string]*Room),
		pendingRooms: make(map[string]*MatchRoomTransaction),
	}
}

func beginCompleteMatchRoom(t *testing.T, rm *RoomManager) (*MatchRoomTransaction, []types.ClientInterface) {
	t.Helper()
	clients := []types.ClientInterface{
		newConcurrencyClient("match-p1"),
		newConcurrencyClient("match-p2"),
		newConcurrencyClient("match-p3"),
	}
	tx, err := rm.BeginMatchRoom(clients[0])
	require.NoError(t, err)
	require.NoError(t, tx.Join(clients[1]))
	require.NoError(t, tx.Join(clients[2]))
	return tx, clients
}

func TestMatchRoomTransactionKeepsPartialRoomInvisibleAndSideEffectFree(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })

	rm := NewRoomManager(storage.NewRedisStore(redisClient), config.GameConfig{RoomTimeout: 10})
	first := newConcurrencyClient("p1")
	second := newConcurrencyClient("p2")
	tx, err := rm.BeginMatchRoom(first)
	require.NoError(t, err)
	require.NoError(t, tx.Join(second))

	require.Nil(t, rm.GetRoom(tx.room.Code))
	require.Empty(t, rm.GetRoomList())
	require.Nil(t, rm.GetRoomByPlayerID(first.GetID()))
	_, err = rm.JoinRoom(newConcurrencyClient("outsider"), tx.room.Code)
	require.ErrorIs(t, err, apperrors.ErrRoomNotFound)
	require.Empty(t, first.GetRoom())
	require.Empty(t, second.GetRoom())
	require.Zero(t, first.sends.Load())
	require.Zero(t, second.sends.Load())
	exists, err := redisClient.Exists(context.Background(), "room:"+tx.room.Code).Result()
	require.NoError(t, err)
	require.Zero(t, exists)

	_, err = tx.Commit()
	require.ErrorIs(t, err, ErrMatchRoomParticipantCount)
	require.Nil(t, rm.GetRoom(tx.room.Code))
	tx.Rollback()
	require.Equal(t, RoomStateEnded, tx.room.State())
}

type blockingRoomBindClient struct {
	*concurrencyClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingRoomBindClient(id string) *blockingRoomBindClient {
	return &blockingRoomBindClient{
		concurrencyClient: newConcurrencyClient(id),
		entered:           make(chan struct{}),
		release:           make(chan struct{}),
	}
}

func (c *blockingRoomBindClient) SetRoom(code string) {
	if code != "" {
		c.once.Do(func() { close(c.entered) })
		<-c.release
	}
	c.concurrencyClient.SetRoom(code)
}

func TestMatchRoomCommitPublishesAndBindsUnderManagerAndRoomOwnership(t *testing.T) {
	rm := newMatchTransactionManager()
	first := newConcurrencyClient("p1")
	blocking := newBlockingRoomBindClient("p2")
	third := newConcurrencyClient("p3")
	tx, err := rm.BeginMatchRoom(first)
	require.NoError(t, err)
	require.NoError(t, tx.Join(blocking))
	require.NoError(t, tx.Join(third))

	result := make(chan error, 1)
	go func() {
		_, commitErr := tx.Commit()
		result <- commitErr
	}()
	<-blocking.entered
	require.False(t, rm.mu.TryRLock(), "commit exposed its partial publication without manager ownership")
	require.False(t, tx.room.mu.TryRLock(), "commit bound clients without room ownership")
	close(blocking.release)
	require.NoError(t, <-result)

	require.Same(t, tx.room, rm.GetRoom(tx.room.Code))
	for _, client := range []types.ClientInterface{first, blocking, third} {
		require.Equal(t, tx.room.Code, client.GetRoom())
	}
	require.Len(t, tx.room.SnapshotPlayers(), 3)
	require.Zero(t, first.sends.Load())
	require.Zero(t, blocking.sends.Load())
	require.Zero(t, third.sends.Load())
}

func TestMatchRoomJoinRejectsDuplicateAndFourthParticipant(t *testing.T) {
	rm := newMatchTransactionManager()
	first := newConcurrencyClient("p1")
	tx, err := rm.BeginMatchRoom(first)
	require.NoError(t, err)
	require.ErrorIs(t, tx.Join(newConcurrencyClient(first.GetID())), ErrMatchRoomRosterChanged)
	require.NoError(t, tx.Join(newConcurrencyClient("p2")))
	require.NoError(t, tx.Join(newConcurrencyClient("p3")))

	require.ErrorIs(t, tx.Join(newConcurrencyClient("p4")), apperrors.ErrRoomFull)
	require.Len(t, tx.room.SnapshotPlayers(), 3)
	tx.Rollback()
}

func TestMatchRoomCommitFailureDoesNotPartiallyPublishOrRebind(t *testing.T) {
	rm := newMatchTransactionManager()
	tx, clients := beginCompleteMatchRoom(t, rm)
	clients[1].SetRoom("other-room")

	_, err := tx.Commit()
	require.ErrorIs(t, err, ErrMatchRoomRosterChanged)
	require.Nil(t, rm.GetRoom(tx.room.Code))
	require.Empty(t, clients[0].GetRoom())
	require.Equal(t, "other-room", clients[1].GetRoom())
	require.Empty(t, clients[2].GetRoom())
	tx.Rollback()
	require.Equal(t, "other-room", clients[1].GetRoom())
}

func TestMatchRoomRollbackIsIdempotentAndOnlyClearsOwnedBindings(t *testing.T) {
	rm := newMatchTransactionManager()
	tx, clients := beginCompleteMatchRoom(t, rm)
	committed, err := tx.Commit()
	require.NoError(t, err)

	outsider := newConcurrencyClient("outsider")
	outsider.SetRoom(committed.Code)
	clients[0].SetRoom("new-room")
	tx.Rollback()
	tx.Rollback()

	require.Nil(t, rm.GetRoom(committed.Code))
	require.Equal(t, RoomStateEnded, committed.State())
	require.Equal(t, "new-room", clients[0].GetRoom())
	require.Empty(t, clients[1].GetRoom())
	require.Empty(t, clients[2].GetRoom())
	require.Equal(t, committed.Code, outsider.GetRoom())
	_, err = tx.Commit()
	require.ErrorIs(t, err, ErrMatchRoomTransactionEnded)
	require.ErrorIs(t, tx.Join(newConcurrencyClient("late")), ErrMatchRoomTransactionEnded)
}

func TestMatchRoomRollbackClearsCurrentReplacementWithoutMutatingStaleHandle(t *testing.T) {
	rm := newMatchTransactionManager()
	tx, clients := beginCompleteMatchRoom(t, rm)
	committed, err := tx.Commit()
	require.NoError(t, err)
	stale := clients[0]
	replacement := newConcurrencyClient(stale.GetID())
	replacement.SetRoom(committed.Code)
	require.True(t, committed.AttachClient(replacement.GetID(), replacement))

	tx.Rollback()

	require.Equal(t, committed.Code, stale.GetRoom(), "rollback mutated a stale connection's shared room identity")
	require.Empty(t, replacement.GetRoom())
	require.Empty(t, clients[1].GetRoom())
	require.Empty(t, clients[2].GetRoom())
}

func TestMatchRoomRollbackDoesNotDeleteReplacementRoomPointer(t *testing.T) {
	rm := newMatchTransactionManager()
	tx, _ := beginCompleteMatchRoom(t, rm)
	committed, err := tx.Commit()
	require.NoError(t, err)
	replacement := newRoom(committed.Code, time.Now())
	rm.mu.Lock()
	rm.rooms[committed.Code] = replacement
	rm.mu.Unlock()

	tx.Rollback()
	require.Same(t, replacement, rm.GetRoom(committed.Code))
}

func TestMatchRoomRollbackDeletesPublishedRedisState(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	rm := NewRoomManager(storage.NewRedisStore(redisClient), config.GameConfig{RoomTimeout: 10})
	tx, _ := beginCompleteMatchRoom(t, rm)
	committed, err := tx.Commit()
	require.NoError(t, err)
	key := "room:" + committed.Code
	require.NoError(t, redisClient.Set(context.Background(), key, "published", time.Hour).Err())

	tx.Rollback()
	require.Eventually(t, func() bool {
		exists, existsErr := redisClient.Exists(context.Background(), key).Result()
		return existsErr == nil && exists == 0
	}, time.Second, time.Millisecond)
}

func TestMatchRoomCodeGenerationSkipsPendingReservation(t *testing.T) {
	rm := newMatchTransactionManager()
	codes := []string{"111111", "111111", "222222"}
	var codeIndex int
	rm.roomCodeFunc = func() string {
		code := codes[codeIndex]
		codeIndex++
		return code
	}
	pending, err := rm.BeginMatchRoom(newConcurrencyClient("pending"))
	require.NoError(t, err)
	require.Equal(t, "111111", pending.room.Code)

	published, err := rm.CreateRoom(newConcurrencyClient("published"))
	require.NoError(t, err)
	require.Equal(t, "222222", published.Code)
	pending.Rollback()
}

func TestMatchRoomCommitAndRollbackAreRaceSafe(t *testing.T) {
	for iteration := range 128 {
		rm := newMatchTransactionManager()
		tx, clients := beginCompleteMatchRoom(t, rm)
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			_, err := tx.Commit()
			if err != nil && !errors.Is(err, ErrMatchRoomTransactionEnded) {
				t.Errorf("iteration %d: unexpected commit error: %v", iteration, err)
			}
		}()
		go func() {
			defer workers.Done()
			<-start
			tx.Rollback()
		}()
		close(start)
		workers.Wait()

		require.Nil(t, rm.GetRoom(tx.room.Code))
		require.Equal(t, RoomStateEnded, tx.room.State())
		for _, client := range clients {
			require.Empty(t, client.GetRoom())
		}
	}
}

func TestReadyAllAndStartRequiresExactCommittedRoster(t *testing.T) {
	rm := newMatchTransactionManager()
	tx, clients := beginCompleteMatchRoom(t, rm)
	committed, err := tx.Commit()
	require.NoError(t, err)

	replacement := newConcurrencyClient(clients[0].GetID())
	require.True(t, committed.AttachClient(replacement.GetID(), replacement))
	_, err = committed.ReadyAllAndStart(clients)
	require.ErrorIs(t, err, ErrMatchRoomRosterChanged)
	require.Equal(t, RoomStateWaiting, committed.State())
	for _, player := range committed.SnapshotPlayers() {
		require.False(t, player.Ready)
	}

	clients[0] = replacement
	replacement.SetRoom(committed.Code)
	snapshot, err := committed.ReadyAllAndStart(clients)
	require.NoError(t, err)
	require.Equal(t, RoomStateReady, committed.State())
	require.Len(t, snapshot, 3)
	for index, player := range snapshot {
		require.True(t, player.Ready)
		require.Same(t, clients[index], player.Client)
	}
	require.Zero(t, replacement.sends.Load())
	require.Zero(t, clients[1].(*concurrencyClient).sends.Load())
	require.Zero(t, clients[2].(*concurrencyClient).sends.Load())
	_, err = committed.ReadyAllAndStart(clients)
	require.ErrorIs(t, err, apperrors.ErrGameStarted)
}
