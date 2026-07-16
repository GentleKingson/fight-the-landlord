package room

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestNotifyPlayerOffline_AllPlayersOffline(t *testing.T) {
	t.Parallel()

	// Setup
	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	client1 := testutil.NewSimpleClient("p1", "Player1")
	client2 := testutil.NewSimpleClient("p2", "Player2")
	client3 := testutil.NewSimpleClient("p3", "Player3")

	// Create room with 3 players
	room, err := rm.CreateRoom(client1)
	require.NoError(t, err)
	_, err = rm.JoinRoom(client2, room.Code)
	require.NoError(t, err)
	_, err = rm.JoinRoom(client3, room.Code)
	require.NoError(t, err)

	// All players go offline
	rm.NotifyPlayerOffline(client1)
	rm.NotifyPlayerOffline(client2)
	rm.NotifyPlayerOffline(client3)

	// Room should be deleted
	assert.Nil(t, rm.GetRoom(room.Code))
}

func TestNotifyPlayerOffline_PartialOffline(t *testing.T) {
	t.Parallel()

	// Setup
	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	client1 := testutil.NewSimpleClient("p1", "Player1")
	client2 := testutil.NewSimpleClient("p2", "Player2")
	client3 := testutil.NewSimpleClient("p3", "Player3")

	// Create room with 3 players
	room, err := rm.CreateRoom(client1)
	require.NoError(t, err)
	_, err = rm.JoinRoom(client2, room.Code)
	require.NoError(t, err)
	_, err = rm.JoinRoom(client3, room.Code)
	require.NoError(t, err)

	// Only one player goes offline
	rm.NotifyPlayerOffline(client1)

	// Room should still exist
	assert.NotNil(t, rm.GetRoom(room.Code))

	// Verify offline notification was sent to other players
	assert.Eventually(t, func() bool {
		return len(client2.SentMessages()) > 0 || len(client3.SentMessages()) > 0
	}, time.Second, 10*time.Millisecond)
}

func TestNotifyPlayerOffline_NotInRoom(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	client := testutil.NewSimpleClient("p1", "Player1")

	// Client not in any room - should not panic
	assert.NotPanics(t, func() {
		rm.NotifyPlayerOffline(client)
	})
}

func TestReconnectPlayer_Success(t *testing.T) {
	t.Parallel()

	// Setup
	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	oldClient := testutil.NewSimpleClient("p1", "Player1")
	newClient := testutil.NewSimpleClient("p1", "Player1") // Same ID, new connection
	observer := testutil.NewSimpleClient("p2", "Player2")

	// Create room
	room, err := rm.CreateRoom(oldClient)
	require.NoError(t, err)
	_, err = rm.JoinRoom(observer, room.Code)
	require.NoError(t, err)
	rm.NotifyPlayerOffline(oldClient)

	// Reconnect
	err = rm.ReconnectPlayer(oldClient.GetID(), room.Code, newClient)
	require.NoError(t, err)

	// Verify new client is in room
	assert.Equal(t, room.Code, newClient.GetRoom())

	// Verify room player reference updated
	rm.mu.RLock()
	r := rm.rooms[room.Code]
	rm.mu.RUnlock()

	r.mu.RLock()
	player := r.players[newClient.GetID()]
	r.mu.RUnlock()

	assert.Equal(t, newClient, player.Client)

	foundOnline := false
	for _, msg := range observer.SentMessages() {
		if msg.Type != protocol.MsgPlayerOnline {
			continue
		}
		payload, parseErr := codec.ParsePayload[protocol.PlayerOnlinePayload](msg)
		require.NoError(t, parseErr)
		assert.Equal(t, "p1", payload.PlayerID)
		assert.Equal(t, "Player1", payload.PlayerName)
		foundOnline = true
	}
	assert.True(t, foundOnline)
}

func TestReconnectPlayer_RoomNotFound(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	oldClient := testutil.NewSimpleClient("p1", "Player1")
	newClient := testutil.NewSimpleClient("p1", "Player1")

	// Set room code but room doesn't exist
	oldClient.SetRoom("NONEXISTENT")

	err := rm.ReconnectPlayer(oldClient.GetID(), oldClient.GetRoom(), newClient)
	assert.ErrorIs(t, err, apperrors.ErrRoomNotFound)
}

func TestReconnectPlayer_PlayerNotInRoom(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	client1 := testutil.NewSimpleClient("p1", "Player1")
	oldClient := testutil.NewSimpleClient("p2", "Player2")
	newClient := testutil.NewSimpleClient("p2", "Player2")

	// Create room with client1
	room, err := rm.CreateRoom(client1)
	require.NoError(t, err)

	// Try to reconnect client2 who was never in the room
	oldClient.SetRoom(room.Code)
	err = rm.ReconnectPlayer(oldClient.GetID(), oldClient.GetRoom(), newClient)
	assert.ErrorIs(t, err, apperrors.ErrNotInRoom)
}

func TestReconnectPlayer_RejectsProvisionalIdentity(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	oldClient := testutil.NewSimpleClient("p1", "Player1")
	provisionalClient := testutil.NewSimpleClient("temporary", "Temporary")
	gameRoom, err := rm.CreateRoom(oldClient)
	require.NoError(t, err)

	err = rm.ReconnectPlayer(oldClient.GetID(), gameRoom.Code, provisionalClient)
	assert.ErrorIs(t, err, apperrors.ErrNotInRoom)
	assert.Same(t, oldClient, gameRoom.players[oldClient.GetID()].Client)
}

func TestReconnectPlayer_NotInAnyRoom(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	oldClient := testutil.NewSimpleClient("p1", "Player1")
	newClient := testutil.NewSimpleClient("p1", "Player1")

	// Client not in any room
	err := rm.ReconnectPlayer(oldClient.GetID(), oldClient.GetRoom(), newClient)
	assert.NoError(t, err) // Should return nil, not error
}

func TestGenerateRoomCode_Uniqueness(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})

	codes := make(map[string]bool)
	for i := 0; i < 100; i++ {
		code := rm.generateRoomCode()
		assert.Len(t, code, roomCodeLength)
		assert.False(t, codes[code], "Generated duplicate room code: %s", code)
		codes[code] = true

		// Add to rooms to test collision avoidance
		rm.rooms[code] = &Room{Code: code}
	}
}

func TestCleanup_TimeoutRooms(t *testing.T) {
	t.Parallel()

	// Use short timeout for testing
	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{})
	rm.roomTimeout = 100 * time.Millisecond
	client := testutil.NewSimpleClient("p1", "Player1")

	// Create room
	room, err := rm.CreateRoom(client)
	require.NoError(t, err)

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Run cleanup
	rm.cleanup()

	// Room should be deleted
	assert.Nil(t, rm.GetRoom(room.Code))

	// Client should be removed from room
	assert.Empty(t, client.GetRoom())
}

func TestCleanup_DoesNotRemoveActiveRooms(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	client := testutil.NewSimpleClient("p1", "Player1")

	// Create room
	room, err := rm.CreateRoom(client)
	require.NoError(t, err)

	// Run cleanup immediately (room is fresh)
	rm.cleanup()

	// Room should still exist
	assert.NotNil(t, rm.GetRoom(room.Code))
}

func TestCleanup_DoesNotRemovePlayingRooms(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{})
	rm.roomTimeout = 100 * time.Millisecond
	client := testutil.NewSimpleClient("p1", "Player1")

	// Create room
	room, err := rm.CreateRoom(client)
	require.NoError(t, err)

	// Change state to playing
	room.mu.Lock()
	room.state = RoomStatePlaying
	room.mu.Unlock()

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Run cleanup
	rm.cleanup()

	// Room should NOT be deleted (playing state)
	assert.NotNil(t, rm.GetRoom(room.Code))
}

func TestSetAllPlayersReady(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	client1 := testutil.NewSimpleClient("p1", "Player1")
	client2 := testutil.NewSimpleClient("p2", "Player2")
	client3 := testutil.NewSimpleClient("p3", "Player3")

	// Create room with 3 players
	room, err := rm.CreateRoom(client1)
	require.NoError(t, err)
	_, err = rm.JoinRoom(client2, room.Code)
	require.NoError(t, err)
	_, err = rm.JoinRoom(client3, room.Code)
	require.NoError(t, err)

	// Initially not ready
	room.mu.RLock()
	for _, p := range room.players {
		assert.False(t, p.Ready)
	}
	room.mu.RUnlock()

	// Set all ready
	room.SetAllPlayersReady()

	// Verify all ready
	room.mu.RLock()
	for _, p := range room.players {
		assert.True(t, p.Ready)
	}
	room.mu.RUnlock()
}

func TestRoomManagerCloseStopsCleanupAndRemovesPublishedRooms(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	rm := newRoomManager(ctx, nil, config.GameConfig{RoomTimeout: 10}, 5*time.Millisecond)
	client := testutil.NewSimpleClient("shutdown-player", "Shutdown Player")
	gameRoom, err := rm.CreateRoom(client)
	require.NoError(t, err)
	removals := make(chan RoomRemoval, 1)
	rm.SetOnRoomRemoved(func(removal RoomRemoval) { removals <- removal })
	cancel()

	require.NoError(t, rm.Close())
	require.NoError(t, rm.Close(), "Close must be idempotent")
	require.Nil(t, rm.GetRoom(gameRoom.Code))
	require.Empty(t, client.GetRoom())
	_, err = rm.CreateRoom(testutil.NewSimpleClient("late-player", "Late Player"))
	require.ErrorIs(t, err, ErrRoomManagerClosed)
	select {
	case removal := <-removals:
		require.Same(t, gameRoom, removal.Room)
		require.Equal(t, RoomRemovalShutdown, removal.Reason)
	case <-time.After(time.Second):
		t.Fatal("RoomManager.Close did not dispatch room removal")
	}
}

func TestRoomManagerCloseCancelsAndWaitsForPersistenceWorker(t *testing.T) {
	t.Parallel()

	rm := NewRoomManagerWithContext(context.Background(), nil, config.GameConfig{RoomTimeout: 10})
	saveStarted := make(chan struct{})
	saveExited := make(chan struct{})
	var once sync.Once
	rm.saveRoomFunc = func(ctx context.Context, _ string, _ *storage.RoomData) error {
		once.Do(func() { close(saveStarted) })
		<-ctx.Done()
		close(saveExited)
		return ctx.Err()
	}
	client := testutil.NewSimpleClient("persist-player", "Persist Player")
	_, err := rm.CreateRoom(client)
	require.NoError(t, err)

	select {
	case <-saveStarted:
	case <-time.After(time.Second):
		t.Fatal("persistence worker did not start")
	}
	done := make(chan error, 1)
	go func() { done <- rm.Close() }()
	select {
	case <-saveExited:
	case <-time.After(time.Second):
		t.Fatal("RoomManager.Close did not cancel persistence")
	}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("RoomManager.Close did not wait for persistence")
	}
}

func TestRoomManagerCloseUsesLiveContextForShutdownDelete(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	rm := NewRoomManagerWithContext(ctx, nil, config.GameConfig{RoomTimeout: 10})
	deleteContextErrors := make(chan error, 1)
	rm.deleteRoomFunc = func(deleteCtx context.Context, _ string) error {
		deleteContextErrors <- deleteCtx.Err()
		return deleteCtx.Err()
	}
	client := testutil.NewSimpleClient("delete-player", "Delete Player")
	_, err := rm.CreateRoom(client)
	require.NoError(t, err)
	cancel()

	require.NoError(t, rm.Close())
	select {
	case err := <-deleteContextErrors:
		require.NoError(t, err, "shutdown deletion must not inherit the canceled worker context")
	case <-time.After(time.Second):
		t.Fatal("RoomManager.Close did not execute the shutdown delete")
	}
}

func TestRoomDeleteContextIsDetachedFromManagerCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	rm := NewRoomManagerWithContext(ctx, nil, config.GameConfig{RoomTimeout: 10})
	deleteStarted := make(chan context.Context, 1)
	releaseDelete := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseDelete) }) }
	rm.deleteRoomFunc = func(deleteCtx context.Context, _ string) error {
		deleteStarted <- deleteCtx
		<-releaseDelete
		return deleteCtx.Err()
	}
	t.Cleanup(func() {
		release()
		require.NoError(t, rm.Close())
	})
	client := testutil.NewSimpleClient("delete-race-player", "Delete Race Player")
	_, err := rm.CreateRoom(client)
	require.NoError(t, err)
	require.True(t, rm.LeaveRoom(client))

	deleteCtx := <-deleteStarted
	cancel()
	require.NoError(t, deleteCtx.Err(), "an in-flight bounded delete must survive manager cancellation")
	release()
}
