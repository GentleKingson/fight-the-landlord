package room

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestRoomManagerDrainWinsLastReadyAdmissionRace(t *testing.T) {
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	t.Cleanup(func() { require.NoError(t, rm.Close()) })
	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient("ready-race-1", "Player1"),
		testutil.NewSimpleClient("ready-race-2", "Player2"),
		testutil.NewSimpleClient("ready-race-3", "Player3"),
	}
	gameRoom, err := rm.CreateRoom(clients[0])
	require.NoError(t, err)
	_, err = rm.JoinRoom(clients[1], gameRoom.Code)
	require.NoError(t, err)
	_, err = rm.JoinRoom(clients[2], gameRoom.Code)
	require.NoError(t, err)
	require.NoError(t, rm.SetPlayerReady(clients[0], true))
	require.NoError(t, rm.SetPlayerReady(clients[1], true))

	admissionEntered := make(chan struct{})
	admissionContinue := make(chan struct{})
	var draining atomic.Bool
	rm.SetStartAdmission(func() (func(), bool) {
		close(admissionEntered)
		<-admissionContinue
		if draining.Load() {
			return nil, false
		}
		return func() {}, true
	})
	var starts atomic.Int32
	rm.SetOnGameStart(func(*Room, []PlayerSnapshot, func()) { starts.Add(1) })

	readyResult := make(chan error, 1)
	go func() { readyResult <- rm.SetPlayerReady(clients[2], true) }()
	<-admissionEntered
	draining.Store(true)
	close(admissionContinue)
	err = <-readyResult
	require.True(t, errors.Is(err, ErrGameStartAdmissionRejected))
	require.Zero(t, starts.Load())
	require.Equal(t, RoomStateWaiting, gameRoom.State())
	for _, player := range gameRoom.SnapshotPlayers() {
		if player.ID == clients[2].GetID() {
			require.False(t, player.Ready, "losing Ready must roll back its mutation")
			return
		}
	}
	t.Fatal("last ready player missing from room")
}

func TestRoomManager_LeaveRoomDoesNotMutateUnownedRoomIdentity(t *testing.T) {
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	client := testutil.NewSimpleClient("p1", "Player1")
	client.SetRoom("missing")

	assert.False(t, rm.LeaveRoom(client))
	assert.Equal(t, "missing", client.GetRoom())
}

func TestRoomManager_LeaveLastPlayerWithoutRedis(t *testing.T) {
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	client := testutil.NewSimpleClient("p1", "Player1")
	created, err := rm.CreateRoom(client)
	require.NoError(t, err)

	assert.True(t, rm.LeaveRoom(client))
	assert.Empty(t, client.GetRoom())
	assert.Nil(t, rm.GetRoom(created.Code))
}
