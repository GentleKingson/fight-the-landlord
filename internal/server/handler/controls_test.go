package handler

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	payloadconv "github.com/palemoky/fight-the-landlord/internal/protocol/convert/payload"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

type operationalTestServer struct {
	*testutil.MockServer
	mu     sync.RWMutex
	state  string
	muted  map[string]bool
	banned map[string]bool
}

func newOperationalTestServer(state string) *operationalTestServer {
	return &operationalTestServer{
		MockServer: new(testutil.MockServer),
		state:      state,
		muted:      make(map[string]bool),
		banned:     make(map[string]bool),
	}
}

func (server *operationalTestServer) OperationalState() string {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.state
}

func (server *operationalTestServer) AcquireOperationalAdmission(
	allowDraining bool,
) (release func(), operationalState string, admitted bool) {
	server.mu.RLock()
	state := server.state
	if state != operationalNormal && state != "future-state" && (!allowDraining || state != operationalDraining) {
		server.mu.RUnlock()
		return nil, state, false
	}
	return server.mu.RUnlock, state, true
}

func (server *operationalTestServer) setOperationalState(state string) {
	server.mu.Lock()
	server.state = state
	server.mu.Unlock()
}

type joinBarrierClient struct {
	*synchronizedClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (client *joinBarrierClient) SendMessage(message *protocol.Message) error {
	err := client.synchronizedClient.SendMessage(message)
	if message.Type == protocol.MsgRoomJoined {
		client.once.Do(func() { close(client.entered) })
		<-client.release
	}
	return err
}

type resumedRejectionServer struct {
	*operationalTestServer
}

func (server *resumedRejectionServer) AcquireOperationalAdmission(bool) (
	release func(),
	operationalState string,
	admitted bool,
) {
	server.setOperationalState(operationalNormal)
	return nil, operationalDraining, false
}

func (server *operationalTestServer) IsPlayerMuted(playerID string) bool {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.muted[playerID]
}

func (server *operationalTestServer) IsPlayerBanned(playerID string) bool {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.banned[playerID]
}

func requireOperationalError(
	t *testing.T,
	client *testutil.SimpleClient,
	command protocol.MessageType,
	code int,
	text string,
) {
	t.Helper()
	messages := client.SentMessages()
	require.Len(t, messages, 1)
	payload, err := codec.ParsePayload[protocol.ErrorPayload](messages[0])
	require.NoError(t, err)
	assert.Equal(t, code, payload.Code)
	assert.Equal(t, command, payload.CommandType)
	assert.Contains(t, payload.Message, text)
}

func TestDrainingRejectsNewRoomsMatchesAndPracticeButAllowsExistingRoomJoin(t *testing.T) {
	t.Parallel()
	server := newOperationalTestServer(operationalDraining)
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	matcher := match.NewMatcher(match.MatcherDeps{})
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })
	handler := NewHandler(HandlerDeps{Server: server, RoomManager: roomManager, Matcher: matcher})

	creator := testutil.NewSimpleClient("creator", "Creator")
	handler.handleCreateRoom(creator)
	requireOperationalError(t, creator, protocol.MsgCreateRoom, protocol.ErrCodeServerDraining, "排空")

	matching := testutil.NewSimpleClient("matching", "Matching")
	handler.handleQuickMatch(matching)
	requireOperationalError(t, matching, protocol.MsgQuickMatch, protocol.ErrCodeServerDraining, "排空")
	assert.Zero(t, matcher.GetQueueLength())

	practicing := testutil.NewSimpleClient("practicing", "Practicing")
	handler.handlePracticeMatch(practicing)
	requireOperationalError(t, practicing, protocol.MsgPracticeMatch, protocol.ErrCodeServerDraining, "排空")

	host := testutil.NewSimpleClient("host", "Host")
	existingRoom, err := roomManager.CreateRoom(host)
	require.NoError(t, err)
	joining := testutil.NewSimpleClient("joining", "Joining")
	handler.handleJoinRoom(joining, codec.MustNewMessage(protocol.MsgJoinRoom, protocol.JoinRoomPayload{RoomCode: existingRoom.Code}))
	require.Len(t, joining.SentMessages(), 1)
	assert.Equal(t, protocol.MsgRoomJoined, joining.SentMessages()[0].Type)
}

func TestMaintenanceRejectsAllNewGameEntryWithExplicitError(t *testing.T) {
	t.Parallel()
	server := newOperationalTestServer(operationalMaintenance)
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	host := testutil.NewSimpleClient("host", "Host")
	existingRoom, err := roomManager.CreateRoom(host)
	require.NoError(t, err)
	handler := NewHandler(HandlerDeps{Server: server, RoomManager: roomManager})

	joining := testutil.NewSimpleClient("joining", "Joining")
	handler.handleJoinRoom(joining, codec.MustNewMessage(protocol.MsgJoinRoom, protocol.JoinRoomPayload{RoomCode: existingRoom.Code}))
	requireOperationalError(t, joining, protocol.MsgJoinRoom, protocol.ErrCodeServerMaintenance, "维护")
}

func TestOperationalPauseRejectsReadyAndRematchAdmission(t *testing.T) {
	t.Parallel()
	for _, state := range []string{operationalDraining, operationalMaintenance} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			server := newOperationalTestServer(state)
			roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
			player := testutil.NewSimpleClient("player-"+state, "Player")
			gameRoom, err := roomManager.CreateRoom(player)
			require.NoError(t, err)
			handler := NewHandler(HandlerDeps{Server: server, RoomManager: roomManager})

			handler.handleReady(player, true)

			expectedCode := protocol.ErrCodeServerMaintenance
			if state == operationalDraining {
				expectedCode = protocol.ErrCodeServerDraining
			}
			requireOperationalError(t, player, protocol.MsgReady, expectedCode, "新牌局")
			snapshot, exists := gameRoom.PlayerForTest(player.GetID())
			require.True(t, exists)
			assert.False(t, snapshot.Ready)

			// CancelReady remains available and therefore cannot trap a player in
			// a waiting or post-game room during a drain.
			player.Messages = nil
			handler.handleReady(player, false)
			require.Len(t, player.SentMessages(), 1)
			assert.Equal(t, protocol.MsgPlayerReady, player.SentMessages()[0].Type)
			canceled, err := codec.ParsePayload[protocol.PlayerReadyPayload](player.SentMessages()[0])
			require.NoError(t, err)
			assert.False(t, canceled.Ready)
		})
	}
}

func TestReadyAdmissionLinearizesWithDrainTransition(t *testing.T) {
	server := newOperationalTestServer(operationalNormal)
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	t.Cleanup(func() { require.NoError(t, roomManager.Close()) })
	players := []*testutil.SimpleClient{
		testutil.NewSimpleClient("ready-linear-1", "Player1"),
		testutil.NewSimpleClient("ready-linear-2", "Player2"),
		testutil.NewSimpleClient("ready-linear-3", "Player3"),
	}
	gameRoom, err := roomManager.CreateRoom(players[0])
	require.NoError(t, err)
	_, err = roomManager.JoinRoom(players[1], gameRoom.Code)
	require.NoError(t, err)
	_, err = roomManager.JoinRoom(players[2], gameRoom.Code)
	require.NoError(t, err)
	require.NoError(t, roomManager.SetPlayerReady(players[0], true))
	require.NoError(t, roomManager.SetPlayerReady(players[1], true))

	startEntered := make(chan struct{})
	startRelease := make(chan struct{})
	roomManager.SetOnGameStart(func(*room.Room, []room.PlayerSnapshot, func()) {
		close(startEntered)
		<-startRelease
	})
	handler := NewHandler(HandlerDeps{Server: server, RoomManager: roomManager})
	readyDone := make(chan struct{})
	go func() {
		handler.handleReady(players[2], true)
		close(readyDone)
	}()
	<-startEntered

	transitionStarted := make(chan struct{})
	transitionDone := make(chan struct{})
	go func() {
		close(transitionStarted)
		server.setOperationalState(operationalDraining)
		close(transitionDone)
	}()
	<-transitionStarted
	select {
	case <-transitionDone:
		t.Fatal("drain completed before an admitted Ready committed")
	default:
	}
	close(startRelease)
	select {
	case <-readyDone:
	case <-time.After(time.Second):
		t.Fatal("Ready did not finish after the start callback released")
	}
	select {
	case <-transitionDone:
	case <-time.After(time.Second):
		t.Fatal("drain did not finish after Ready committed")
	}

	postDrain := testutil.NewSimpleClient("ready-post-drain", "Post Drain")
	_, err = roomManager.CreateRoom(postDrain)
	require.NoError(t, err)
	postDrain.Messages = nil
	handler.handleReady(postDrain, true)
	requireOperationalError(t, postDrain, protocol.MsgReady, protocol.ErrCodeServerDraining, "排空")
}

func TestJoinAdmissionAllowsDrainAndLinearizesWithMaintenance(t *testing.T) {
	server := newOperationalTestServer(operationalDraining)
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	t.Cleanup(func() { require.NoError(t, roomManager.Close()) })
	host := testutil.NewSimpleClient("join-linear-host", "Host")
	gameRoom, err := roomManager.CreateRoom(host)
	require.NoError(t, err)
	joining := &joinBarrierClient{
		synchronizedClient: &synchronizedClient{id: "join-linear-player", name: "Player"},
		entered:            make(chan struct{}),
		release:            make(chan struct{}),
	}
	handler := NewHandler(HandlerDeps{Server: server, RoomManager: roomManager})
	joinDone := make(chan struct{})
	go func() {
		handler.handleJoinRoom(joining, codec.MustNewMessage(protocol.MsgJoinRoom, protocol.JoinRoomPayload{RoomCode: gameRoom.Code}))
		close(joinDone)
	}()
	<-joining.entered

	transitionStarted := make(chan struct{})
	transitionDone := make(chan struct{})
	go func() {
		close(transitionStarted)
		server.setOperationalState(operationalMaintenance)
		close(transitionDone)
	}()
	<-transitionStarted
	select {
	case <-transitionDone:
		t.Fatal("maintenance completed before the draining join committed")
	default:
	}
	close(joining.release)
	select {
	case <-joinDone:
	case <-time.After(time.Second):
		t.Fatal("draining join did not finish")
	}
	select {
	case <-transitionDone:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not finish after the join committed")
	}
	require.Equal(t, gameRoom.Code, joining.GetRoom())

	postMaintenance := testutil.NewSimpleClient("join-post-maintenance", "Post Maintenance")
	handler.handleJoinRoom(postMaintenance, codec.MustNewMessage(protocol.MsgJoinRoom, protocol.JoinRoomPayload{RoomCode: gameRoom.Code}))
	requireOperationalError(t, postMaintenance, protocol.MsgJoinRoom, protocol.ErrCodeServerMaintenance, "维护")
}

func TestOperationalRejectionUsesCapturedStateAfterResume(t *testing.T) {
	base := newOperationalTestServer(operationalDraining)
	server := &resumedRejectionServer{operationalTestServer: base}
	client := testutil.NewSimpleClient("captured-rejection", "Player")
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	t.Cleanup(func() { require.NoError(t, roomManager.Close()) })
	handler := NewHandler(HandlerDeps{Server: server, RoomManager: roomManager})

	handler.handleCreateRoom(client)

	require.Equal(t, operationalNormal, server.OperationalState())
	requireOperationalError(t, client, protocol.MsgCreateRoom, protocol.ErrCodeServerDraining, "排空")
}

func TestOperationalPauseDoesNotAffectExistingGameActions(t *testing.T) {
	for _, state := range []string{operationalDraining, operationalMaintenance} {
		t.Run(state, func(t *testing.T) {
			gameRoom, gameSession, clients := setupGameRoom(t)
			roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
			roomManager.AddRoomForTest(gameRoom)
			handler := NewHandler(HandlerDeps{
				Server:      newOperationalTestServer(state),
				RoomManager: roomManager,
			})
			handler.SetGameSession(gameRoom.Code, gameSession)

			current := gameSession.GetCurrentBidderForSerialization()
			playerID := gameRoom.SnapshotPlayers()[current].ID
			var bidder *testutil.MockClient
			for _, client := range clients {
				if client.GetID() == playerID {
					bidder = client
					break
				}
			}
			require.NotNil(t, bidder)
			payload, err := payloadconv.EncodePayload(protocol.MsgBid, protocol.BidPayload{Bid: true})
			require.NoError(t, err)
			handler.handleBid(bidder, &protocol.Message{Type: protocol.MsgBid, Payload: payload})
			assert.NotEqual(t, current, gameSession.GetCurrentBidderForSerialization())
		})
	}
}

func TestDrainingAllowsReconnect(t *testing.T) {
	t.Parallel()
	sessionManager := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sessionManager.Close()) })
	original := sessionManager.MustCreateSession("player-1", "Player One")
	sessionManager.SetOffline(original.PlayerID)
	previous := testutil.NewSimpleClient(original.PlayerID, original.PlayerName)
	provisional := testutil.NewSimpleClient("temporary", "Temporary")
	sessionManager.MustCreateSession(provisional.GetID(), provisional.GetName())
	server := newOperationalTestServer(operationalDraining)
	server.On(
		"RebindClient",
		provisional.GetID(), original.PlayerID, original.PlayerName, "", provisional,
	).Run(func(arguments mock.Arguments) {
		client := arguments.Get(4).(*testutil.SimpleClient)
		client.ID = arguments.String(1)
		client.Name = arguments.String(2)
	}).Return(previous, nil).Once()
	handler := NewHandler(HandlerDeps{Server: server, SessionManager: sessionManager})

	handler.handleReconnect(provisional, codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		Token:    original.ReconnectToken,
		PlayerID: original.PlayerID,
	}))

	assert.Equal(t, original.PlayerID, provisional.GetID())
	messages := provisional.SentMessages()
	require.NotEmpty(t, messages)
	assert.Equal(t, protocol.MsgReconnected, messages[0].Type)
	server.AssertExpectations(t)
}

func TestBannedPlayerCannotReconnect(t *testing.T) {
	t.Parallel()
	sessionManager := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sessionManager.Close()) })
	original := sessionManager.MustCreateSession("banned-player", "Banned Player")
	originalToken := original.ReconnectToken
	sessionManager.SetOffline(original.PlayerID)
	provisional := &closeRecordingClient{SimpleClient: testutil.NewSimpleClient("temporary", "Temporary")}
	sessionManager.MustCreateSession(provisional.GetID(), provisional.GetName())
	server := newOperationalTestServer(operationalDraining)
	server.banned[original.PlayerID] = true
	handler := NewHandler(HandlerDeps{Server: server, SessionManager: sessionManager})

	handler.handleReconnect(provisional, codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		Token:    originalToken,
		PlayerID: original.PlayerID,
	}))

	assert.True(t, provisional.isClosed())
	require.NotEmpty(t, provisional.SentMessages())
	errorPayload, err := codec.ParsePayload[protocol.ErrorPayload](provisional.SentMessages()[0])
	require.NoError(t, err)
	assert.Contains(t, errorPayload.Message, "封禁")
	assert.Same(t, sessionManager.GetSession(original.PlayerID), sessionManager.GetSessionByToken(originalToken))
}

func TestMutedPlayerCannotChatAndUnmuteTakesEffect(t *testing.T) {
	t.Parallel()
	server := newOperationalTestServer(operationalNormal)
	server.muted["player-1"] = true
	client := testutil.NewSimpleClient("player-1", "Player One")
	handler := NewHandler(HandlerDeps{Server: server})
	message := codec.MustNewMessage(protocol.MsgChat, protocol.ChatPayload{
		Content: "hidden content", Scope: "lobby", MessageID: "message-1",
	})

	handler.handleChat(client, message)
	requireChatError(t, client, protocol.ErrCodeRateLimit)

	client.Messages = nil
	server.mu.Lock()
	server.muted["player-1"] = false
	server.mu.Unlock()
	server.On("BroadcastToLobby", mock.Anything).Once()
	handler.handleChat(client, message)
	server.AssertExpectations(t)
}

func TestUnknownOperationalStateFailsOpenAsNormal(t *testing.T) {
	t.Parallel()
	server := newOperationalTestServer("future-state")
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	client := testutil.NewSimpleClient("creator", "Creator")
	handler := NewHandler(HandlerDeps{Server: server, RoomManager: roomManager})

	handler.handleCreateRoom(client)

	require.Len(t, client.SentMessages(), 1)
	assert.Equal(t, protocol.MsgRoomCreated, client.SentMessages()[0].Type)
}
