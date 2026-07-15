package server

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
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
	playerSession := sessionManager.MustCreateSession(client.GetID(), client.GetName())

	client.SetRoom("123456")
	assert.Equal(t, "123456", playerSession.RoomCode)

	client.SetRoom("")
	assert.Empty(t, playerSession.RoomCode)
}

func TestClient_SendMessageIfRoomSkipsStaleTerminalDelivery(t *testing.T) {
	t.Parallel()

	client := NewClient(nil, nil)
	message := codec.MustNewMessage(protocol.MsgRoomLeft, protocol.RoomLeftPayload{RoomCode: "retired-room"})

	sent, err := client.SendMessageIfRoom("", message)
	require.NoError(t, err)
	require.True(t, sent)
	require.Len(t, client.send, 1)

	client.SetRoom("replacement-room")
	sent, err = client.SendMessageIfRoom("", message)
	require.NoError(t, err)
	require.False(t, sent)
	require.Len(t, client.send, 1)
}

func TestClient_SendMessageIfIdentityRejectsReboundPlayerInSameRoom(t *testing.T) {
	t.Parallel()

	client := NewClient(nil, nil)
	client.rebindIdentity("original-player", "Original", "shared-room")
	message := &protocol.Message{Type: protocol.MsgDealCards}

	client.rebindIdentity("replacement-player", "Replacement", "shared-room")
	sent, err := client.SendMessageIfIdentity("original-player", "shared-room", message)
	require.NoError(t, err)
	require.False(t, sent)
	require.Empty(t, client.send)

	sent, err = client.SendMessageIfIdentity("replacement-player", "shared-room", message)
	require.NoError(t, err)
	require.True(t, sent)
	require.Len(t, client.send, 1)
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

func TestServer_RebindClientAndMatchCommitHaveOneAtomicWinner(t *testing.T) {
	server := &Server{clients: make(map[string]*Client)}
	provisional := NewClient(server, nil)
	temporaryID := provisional.GetID()
	server.registerClient(provisional)

	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	tx, err := roomManager.BeginMatchRoom(provisional)
	require.NoError(t, err)
	require.NoError(t, tx.Join(NewClient(nil, nil)))
	require.NoError(t, tx.Join(NewClient(nil, nil)))
	t.Cleanup(tx.Rollback)

	start := make(chan struct{})
	commitResult := make(chan error, 1)
	rebindResult := make(chan error, 1)
	go func() {
		<-start
		_, commitErr := tx.Commit()
		commitResult <- commitErr
	}()
	go func() {
		<-start
		_, rebindErr := server.RebindClient(
			temporaryID,
			"restored-player",
			"Restored Player",
			"restored-room",
			provisional,
		)
		rebindResult <- rebindErr
	}()
	close(start)

	commitErr := <-commitResult
	rebindErr := <-rebindResult
	require.NotEqual(t, commitErr == nil, rebindErr == nil, "exactly one identity transition must commit")
	if commitErr == nil {
		require.Equal(t, temporaryID, provisional.GetID())
		require.Equal(t, tx.Room().Code, provisional.GetRoom())
		require.Same(t, provisional, server.GetClientByID(temporaryID))
		require.Nil(t, server.GetClientByID("restored-player"))
		return
	}

	require.Equal(t, "restored-player", provisional.GetID())
	require.Equal(t, "restored-room", provisional.GetRoom())
	require.Nil(t, server.GetClientByID(temporaryID))
	require.Same(t, provisional, server.GetClientByID("restored-player"))
}

func TestServerActiveClientResolverReturnsNilForMissingIdentity(t *testing.T) {
	server := &Server{clients: make(map[string]*Client)}
	if active := server.GetClientByID("missing"); active != nil {
		t.Fatalf("missing identity resolved to a non-nil interface: %T", active)
	}
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
	restoredSession := sessionManager.MustCreateSession(previous.GetID(), previous.GetName())
	gameRoom, err := roomManager.CreateRoom(previous)
	require.NoError(t, err)

	rebound := NewClient(server, nil)
	temporaryID := rebound.GetID()
	server.registerClient(rebound)
	sessionManager.MustCreateSession(temporaryID, rebound.GetName())
	sessionManager.SetOffline(previous.GetID())
	restored, err := sessionManager.RestoreSession(restoredSession.ReconnectToken, previous.GetID(), temporaryID)
	require.NoError(t, err)
	_, err = server.RebindClient(temporaryID, restored.PlayerID, restored.PlayerName, restored.RoomCode, rebound)
	require.NoError(t, err)
	require.NoError(t, roomManager.ReconnectPlayer(restored.PlayerID, restored.RoomCode, rebound))

	previous.handleDisconnect()

	assert.True(t, sessionManager.IsOnline(restored.PlayerID))
	assert.Equal(t, rebound, server.GetClientByID(restored.PlayerID))
	recipient, ok := gameRoom.PrivateRecipient(restored.PlayerID)
	require.True(t, ok)
	assert.Same(t, rebound, recipient)
}

func TestClientDisconnectCancelsAuthoritativeMatchState(t *testing.T) {
	server := &Server{clients: make(map[string]*Client)}
	matcher := match.NewMatcher(match.MatcherDeps{
		QueueTimeout:        time.Hour,
		ResolveActiveClient: server.GetClientByID,
	})
	server.matcher = matcher
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	client := NewClient(server, nil)
	server.registerClient(client)
	require.True(t, matcher.AddToQueue(client))

	client.handleDisconnect()

	assert.Zero(t, matcher.GetQueueLength())
	assert.Nil(t, server.GetClientByID(client.GetID()))
}

func TestStaleDisconnectDoesNotCancelReplacementMatchGeneration(t *testing.T) {
	server := &Server{clients: make(map[string]*Client)}
	matcher := match.NewMatcher(match.MatcherDeps{
		QueueTimeout:        time.Hour,
		ResolveActiveClient: server.GetClientByID,
	})
	server.matcher = matcher
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	previous := NewClient(server, nil)
	previous.rebindIdentity("restored-id", "Restored Player", "")
	server.registerClient(previous)
	require.True(t, matcher.AddToQueue(previous))

	replacement := NewClient(server, nil)
	temporaryID := replacement.GetID()
	server.registerClient(replacement)
	replaced, err := server.RebindClient(
		temporaryID,
		previous.GetID(),
		previous.GetName(),
		"",
		replacement,
	)
	require.NoError(t, err)
	require.Same(t, previous, replaced)
	matcher.ReplaceClient(previous, replacement)

	previous.handleDisconnect()

	assert.Equal(t, 1, matcher.GetQueueLength())
	assert.True(t, matcher.RemoveFromQueue(replacement))
	assert.Equal(t, replacement, server.GetClientByID("restored-id"))
}

func TestClient_StaleLeaveDoesNotClobberReboundSessionRoom(t *testing.T) {
	sessionManager := session.NewSessionManager()
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	server := &Server{sessionManager: sessionManager, roomManager: roomManager}
	previous := NewClient(server, nil)
	previous.rebindIdentity("restored-id", "Restored Player", "")
	playerSession := sessionManager.MustCreateSession(previous.GetID(), previous.GetName())
	firstRoom, err := roomManager.CreateRoom(previous)
	require.NoError(t, err)

	rebound := NewClient(server, nil)
	rebound.rebindIdentity(previous.GetID(), previous.GetName(), firstRoom.Code)
	require.NoError(t, roomManager.ReconnectPlayer(previous.GetID(), firstRoom.Code, rebound))
	require.False(t, roomManager.LeaveRoom(previous))
	assert.Equal(t, firstRoom.Code, playerSession.RoomCode)

	require.True(t, roomManager.LeaveRoom(rebound))
	secondRoom, err := roomManager.CreateRoom(rebound)
	require.NoError(t, err)
	require.False(t, roomManager.LeaveRoom(previous))
	assert.Equal(t, secondRoom.Code, playerSession.RoomCode)
}

func TestClient_Close(t *testing.T) {
	t.Parallel()

	client := &Client{
		send: make(chan []byte, 1),
	}

	// First close
	client.Close()
	assert.True(t, client.isClosed())

	// Second close (should be safe)
	assert.NotPanics(t, func() {
		client.Close()
	})

	select {
	case <-client.done:
	default:
		t.Fatal("client done signal was not closed")
	}

	// Producers never own or close the writer queue. Keeping this channel open
	// removes the send/close race; SendMessage observes done instead.
	client.send <- []byte("still open")
	assert.Equal(t, []byte("still open"), <-client.send)
}

func TestClient_SendMessageAndCloseAreConcurrentSafe(t *testing.T) {
	const (
		clientCount = 64
		sendCount   = 20_000
		senders     = 64
	)
	clients := make([]*Client, clientCount)
	for i := range clients {
		clients[i] = &Client{send: make(chan []byte, 1)}
		clients[i].send <- []byte("occupy the queue")
	}
	message := &protocol.Message{Type: protocol.MsgPing}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var unexpected atomic.Int64
	wg.Add(senders + clientCount)
	for worker := range senders {
		go func() {
			defer wg.Done()
			<-start
			for i := worker; i < sendCount; i += senders {
				err := clients[i%clientCount].SendMessage(message)
				if !errors.Is(err, ErrClientClosed) && !errors.Is(err, ErrClientSendBufferFull) {
					unexpected.Add(1)
				}
			}
		}()
	}
	for _, client := range clients {
		go func(client *Client) {
			defer wg.Done()
			<-start
			for range 32 {
				client.Close()
			}
		}(client)
	}

	close(start)
	wg.Wait()
	assert.Zero(t, unexpected.Load())
}

func TestClient_FullSendBufferDisconnectsAndCountsOnce(t *testing.T) {
	server := &Server{}
	client := &Client{server: server, send: make(chan []byte, 1)}
	client.send <- []byte("occupy the queue")
	message := &protocol.Message{Type: protocol.MsgPing}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var unexpected atomic.Int64
	wg.Add(100)
	for range 100 {
		go func() {
			defer wg.Done()
			<-start
			err := client.SendMessage(message)
			if !errors.Is(err, ErrClientSendBufferFull) && !errors.Is(err, ErrClientClosed) {
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Zero(t, unexpected.Load())
	assert.True(t, client.isClosed())
	assert.ErrorIs(t, client.SendMessage(message), ErrClientClosed)
	assert.EqualValues(t, 1, server.slowClientDisconnects.Load())
}

func TestServer_BroadcastAndCloseAreConcurrentSafe(t *testing.T) {
	const clientCount = 16
	server := &Server{clients: make(map[string]*Client, clientCount)}
	clients := make([]*Client, 0, clientCount)
	for range clientCount {
		client := &Client{server: server, send: make(chan []byte, 1)}
		client.send <- []byte("occupy the queue")
		server.registerClient(client)
		clients = append(clients, client)
	}

	message := &protocol.Message{Type: protocol.MsgPing}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for range 1_000 {
			server.Broadcast(message)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range 1_000 {
			for _, client := range clients {
				client.Close()
			}
		}
	}()
	close(start)
	wg.Wait()
}

func TestServer_ShutdownAndSendAreConcurrentSafe(t *testing.T) {
	server := &Server{
		config:  &config.Config{},
		redis:   redis.NewClient(&redis.Options{}),
		clients: make(map[string]*Client),
	}
	client := &Client{server: server, send: make(chan []byte, 20_000)}
	server.registerClient(client)
	message := &protocol.Message{Type: protocol.MsgPing}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for range 20_000 {
			err := client.SendMessage(message)
			if err != nil && !errors.Is(err, ErrClientClosed) {
				t.Errorf("unexpected send error: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		server.Shutdown()
	}()
	close(start)
	wg.Wait()
	assert.True(t, client.isClosed())
}

func TestServerShutdownClosesMatcherBeforeDependencies(t *testing.T) {
	server := &Server{
		config:  &config.Config{},
		redis:   redis.NewClient(&redis.Options{}),
		clients: make(map[string]*Client),
	}
	matcher := match.NewMatcher(match.MatcherDeps{QueueTimeout: time.Hour})
	server.matcher = matcher
	queued := NewClient(server, nil)
	require.True(t, matcher.AddToQueue(queued))

	server.Shutdown()

	assert.Zero(t, matcher.GetQueueLength())
	assert.False(t, matcher.AddToQueue(NewClient(server, nil)))
}

func TestServer_ReconnectReplacementAndSendAreConcurrentSafe(t *testing.T) {
	server := &Server{clients: make(map[string]*Client)}
	previous := &Client{
		ID:   "restored-id",
		Name: "Restored Player",
		send: make(chan []byte, 20_000),
	}
	previous.server = server
	server.registerClient(previous)
	replacement := NewClient(server, nil)
	temporaryID := replacement.GetID()
	server.registerClient(replacement)
	message := &protocol.Message{Type: protocol.MsgPing}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var rebindErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for range 20_000 {
			err := previous.SendMessage(message)
			if err != nil && !errors.Is(err, ErrClientClosed) {
				t.Errorf("unexpected send error: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		var replaced interface{}
		replaced, rebindErr = server.RebindClient(
			temporaryID,
			"restored-id",
			"Restored Player",
			"",
			replacement,
		)
		if rebindErr == nil {
			assert.Same(t, previous, replaced)
			previous.Close()
		}
	}()
	close(start)
	wg.Wait()
	require.NoError(t, rebindErr)
	assert.Equal(t, replacement, server.GetClientByID("restored-id"))
}
