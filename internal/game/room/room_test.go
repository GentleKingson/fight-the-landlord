package room

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestRoomManager_GetRoomList(t *testing.T) {
	t.Parallel()

	// Initialize RoomManager with nil server (ok for this test)
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})

	// Manually add a suitable room
	room := &Room{
		Code:        "123456",
		State:       RoomStateWaiting,
		Players:     make(map[string]*RoomPlayer),
		PlayerOrder: []string{},
		CreatedAt:   time.Now(),
	}
	// Add a dummy player
	room.Players["p1"] = &RoomPlayer{
		Client: &testutil.SimpleClient{ID: "p1", Name: "Player1"},
		Seat:   0,
	}

	rm.rooms["123456"] = room

	// Execute
	rooms := rm.GetRoomList()

	// Verify
	assert.Len(t, rooms, 1)
	roomItem := rooms[0]
	assert.Equal(t, "123456", roomItem.RoomCode)
	assert.Equal(t, 1, roomItem.PlayerCount)
	assert.Equal(t, 3, roomItem.MaxPlayers)
}

func TestRoom_CheckAllReady(t *testing.T) {
	t.Parallel()

	room := &Room{
		Players: make(map[string]*RoomPlayer),
	}

	// Case 1: Not enough players
	room.Players["p1"] = &RoomPlayer{Ready: true}
	room.Players["p2"] = &RoomPlayer{Ready: true}
	assert.False(t, room.checkAllReady())

	// Case 2: Enough players, but not all ready
	room.Players["p3"] = &RoomPlayer{Ready: false}
	assert.False(t, room.checkAllReady())

	// Case 3: All ready
	room.Players["p3"].Ready = true
	assert.True(t, room.checkAllReady())
}

func TestRoom_GetPlayerInfo(t *testing.T) {
	t.Parallel()

	room := &Room{
		Players: make(map[string]*RoomPlayer),
	}
	client := &testutil.SimpleClient{ID: "p1", Name: "TestPlayer"}
	room.Players["p1"] = &RoomPlayer{
		Client:     client,
		Seat:       1,
		Ready:      true,
		IsLandlord: false,
	}

	info := room.GetPlayerInfo("p1")

	assert.Equal(t, "p1", info.ID)
	assert.Equal(t, "TestPlayer", info.Name)
	assert.Equal(t, 1, info.Seat)
	assert.True(t, info.Ready)
	assert.True(t, info.Online)
}

func TestRoom_GetPlayerInfoMarksDisconnectedPlayerOffline(t *testing.T) {
	t.Parallel()

	gameRoom := &Room{
		Players: map[string]*RoomPlayer{
			"p1": {Client: nil, Seat: 2, Ready: false},
		},
	}

	info := gameRoom.GetPlayerInfo("p1")

	assert.Equal(t, "p1", info.ID)
	assert.Equal(t, 2, info.Seat)
	assert.False(t, info.Online)
}

func TestRoom_BroadcastSkipsOfflinePlayers(t *testing.T) {
	t.Parallel()

	online := testutil.NewSimpleClient("p2", "Player2")
	room := &Room{
		Players: map[string]*RoomPlayer{
			"p1": {Client: nil},
			"p2": {Client: online},
		},
	}
	msg := &protocol.Message{Type: protocol.MsgPlayerOnline}

	assert.NotPanics(t, func() {
		room.Broadcast(msg)
		room.BroadcastExcept("p2", msg)
	})
	assert.Equal(t, []*protocol.Message{msg}, online.SentMessages())
}
