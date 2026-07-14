package room

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestRoomManager_LeaveRoomClearsStaleClientRoom(t *testing.T) {
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	client := testutil.NewSimpleClient("p1", "Player1")
	client.SetRoom("missing")

	assert.True(t, rm.LeaveRoom(client))
	assert.Empty(t, client.GetRoom())
	assert.False(t, rm.LeaveRoom(client))
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
