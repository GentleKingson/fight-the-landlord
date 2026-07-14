package server

import (
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
)

func TestNewClient(t *testing.T) {
	t.Parallel()

	// 模拟 Server
	server := &Server{}
	// 模拟 Conn (这里只能用 nil 替代，因为 websocket.Conn 很难 mock，
	// 真正的连接测试通常在集成测试中做，或者使用 httptest 启动真实 server)
	var conn *websocket.Conn

	client := NewClient(server, conn)

	assert.NotEmpty(t, client.ID)
	assert.NotEmpty(t, client.Name)
	assert.Equal(t, server, client.server)
	assert.NotNil(t, client.send)
}

func TestClient_SetGetRoom_Concurrency(t *testing.T) {
	t.Parallel()

	client := &Client{}
	var wg sync.WaitGroup
	count := 100

	wg.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			client.SetRoom("room-concurrent")
			_ = client.GetRoom()
		}()
	}

	wg.Wait()
	assert.Equal(t, "room-concurrent", client.GetRoom())
}

func TestClient_SetRoomSynchronizesPlayerSession(t *testing.T) {
	t.Parallel()

	sessionManager := session.NewSessionManager()
	server := &Server{sessionManager: sessionManager}
	client := NewClient(server, nil)
	playerSession := sessionManager.CreateSession(client.GetID(), client.GetName())

	client.SetRoom("123456")
	assert.Equal(t, "123456", playerSession.RoomCode)

	client.SetRoom("")
	assert.Empty(t, playerSession.RoomCode)
}

func TestServer_RebindClientIsPointerSafe(t *testing.T) {
	t.Parallel()

	server := &Server{clients: make(map[string]*Client)}
	previous := NewClient(server, nil)
	previous.rebindIdentity("restored-id", "Restored Player", "123456")
	server.registerClient(previous)

	rebound := NewClient(server, nil)
	temporaryID := rebound.GetID()
	server.registerClient(rebound)

	replaced, err := server.RebindClient(
		temporaryID,
		"restored-id",
		"Restored Player",
		"123456",
		rebound,
	)
	require.NoError(t, err)
	assert.Equal(t, previous, replaced)
	assert.Equal(t, "restored-id", rebound.GetID())
	assert.Equal(t, "Restored Player", rebound.GetName())
	assert.Equal(t, "123456", rebound.GetRoom())
	assert.Nil(t, server.GetClientByID(temporaryID))
	assert.Equal(t, rebound, server.GetClientByID("restored-id"))

	assert.False(t, server.unregisterClient(previous))
	assert.Equal(t, rebound, server.GetClientByID("restored-id"))
	assert.False(t, server.UnregisterClient("restored-id", previous))
	assert.True(t, server.UnregisterClient("restored-id", rebound))
	assert.Nil(t, server.GetClientByID("restored-id"))
}

func TestClient_StaleDisconnectDoesNotClobberReboundConnection(t *testing.T) {
	t.Parallel()

	sessionManager := session.NewSessionManager()
	roomManager := room.NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	server := &Server{
		clients:        make(map[string]*Client),
		sessionManager: sessionManager,
		roomManager:    roomManager,
	}

	previous := NewClient(server, nil)
	previous.rebindIdentity("restored-id", "Restored Player", "")
	server.registerClient(previous)
	restoredSession := sessionManager.CreateSession(previous.GetID(), previous.GetName())
	gameRoom, err := roomManager.CreateRoom(previous)
	require.NoError(t, err)

	rebound := NewClient(server, nil)
	temporaryID := rebound.GetID()
	server.registerClient(rebound)
	sessionManager.CreateSession(temporaryID, rebound.GetName())
	sessionManager.SetOffline(previous.GetID())
	restored, err := sessionManager.RestoreSession(restoredSession.ReconnectToken, previous.GetID(), temporaryID)
	require.NoError(t, err)
	_, err = server.RebindClient(temporaryID, restored.PlayerID, restored.PlayerName, restored.RoomCode, rebound)
	require.NoError(t, err)
	require.NoError(t, roomManager.ReconnectPlayer(restored.PlayerID, restored.RoomCode, rebound))

	previous.handleDisconnect()

	assert.True(t, sessionManager.IsOnline(restored.PlayerID))
	assert.Equal(t, rebound, server.GetClientByID(restored.PlayerID))
	assert.Same(t, rebound, gameRoom.Players[restored.PlayerID].Client)
}

func TestClient_Close(t *testing.T) {
	t.Parallel()

	client := &Client{
		send: make(chan []byte, 1),
	}

	// First close
	client.Close()
	assert.True(t, client.closed)

	// Second close (should be safe)
	assert.NotPanics(t, func() {
		client.Close()
	})

	// Check channel closed
	_, ok := <-client.send
	assert.False(t, ok)
}
