package handler

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

type reconnectOnlineBarrierClient struct {
	*testutil.SimpleClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (client *reconnectOnlineBarrierClient) SendMessage(message *protocol.Message) error {
	if message.Type == protocol.MsgPlayerOnline {
		client.once.Do(func() { close(client.entered) })
		<-client.release
	}
	return client.SimpleClient.SendMessage(message)
}

type reconnectedSendBarrierClient struct {
	*synchronizedClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type reconnectSnapshotFailureClient struct {
	*synchronizedClient
	closed bool
}

func (client *reconnectSnapshotFailureClient) SendMessageIfIdentity(expectedPlayerID, expectedRoom string, message *protocol.Message) (bool, error) {
	if client.GetID() != expectedPlayerID || client.GetRoom() != expectedRoom {
		return false, nil
	}
	if message.Type == protocol.MsgReconnected {
		return false, errors.New("injected reconnect snapshot failure")
	}
	return true, client.SendMessage(message)
}

func (client *reconnectSnapshotFailureClient) Close() {
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
}

func (client *reconnectSnapshotFailureClient) isClosed() bool {
	client.mu.RLock()
	defer client.mu.RUnlock()
	return client.closed
}

type closeRecordingClient struct {
	*testutil.SimpleClient
	mu     sync.Mutex
	closed bool
}

func (client *closeRecordingClient) Close() {
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
}

func (client *closeRecordingClient) isClosed() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.closed
}

func (client *reconnectedSendBarrierClient) SendMessage(message *protocol.Message) error {
	err := client.synchronizedClient.SendMessage(message)
	if message.Type == protocol.MsgReconnected {
		client.once.Do(func() { close(client.entered) })
		<-client.release
	}
	return err
}

func TestHandleReconnectRestoresIdentityRoomAndReadyCommand(t *testing.T) {
	t.Parallel()

	sessionManager := session.NewSessionManager()
	roomManager := room.NewRoomManager(storage.NewRedisStore(nil), config.GameConfig{RoomTimeout: 10})
	restoredClient := testutil.NewSimpleClient("player-1", "Player One")
	observer := testutil.NewSimpleClient("player-2", "Player Two")
	gameRoom, err := roomManager.CreateRoom(restoredClient)
	require.NoError(t, err)
	_, err = roomManager.JoinRoom(observer, gameRoom.Code)
	require.NoError(t, err)

	restoredSession := sessionManager.CreateSession(restoredClient.GetID(), restoredClient.GetName())
	sessionManager.SetRoom(restoredClient.GetID(), gameRoom.Code)
	sessionManager.SetOffline(restoredClient.GetID())
	roomManager.NotifyPlayerOffline(restoredClient)
	originalToken := restoredSession.ReconnectToken
	previousConnection := testutil.NewSimpleClient(restoredClient.GetID(), restoredClient.GetName())
	matcher := match.NewMatcher(match.MatcherDeps{QueueTimeout: time.Hour})
	require.True(t, matcher.AddToQueue(previousConnection))
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	provisionalClient := testutil.NewSimpleClient("temporary-player", "Temporary Player")
	provisionalSession := sessionManager.CreateSession(provisionalClient.GetID(), provisionalClient.GetName())
	provisionalToken := provisionalSession.ReconnectToken

	server := new(testutil.MockServer)
	server.On(
		"RebindClient",
		"temporary-player",
		"player-1",
		"Player One",
		gameRoom.Code,
		provisionalClient,
	).Run(func(args mock.Arguments) {
		client := args.Get(4).(*testutil.SimpleClient)
		client.ID = args.String(1)
		client.Name = args.String(2)
		client.RoomCode = args.String(3)
	}).Return(previousConnection, nil).Once()

	h := NewHandler(HandlerDeps{
		Server:         server,
		RoomManager:    roomManager,
		Matcher:        matcher,
		SessionManager: sessionManager,
	})
	reconnectMessage := codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		Token:    originalToken,
		PlayerID: "player-1",
	})

	h.handleReconnect(provisionalClient, reconnectMessage)

	require.Equal(t, "player-1", provisionalClient.GetID())
	require.Equal(t, "Player One", provisionalClient.GetName())
	require.Equal(t, gameRoom.Code, provisionalClient.GetRoom())
	require.Nil(t, sessionManager.GetSession("temporary-player"))
	require.Nil(t, sessionManager.GetSessionByToken(provisionalToken))
	require.Nil(t, sessionManager.GetSessionByToken(originalToken))

	rotatedSession := sessionManager.GetSession("player-1")
	require.NotNil(t, rotatedSession)
	require.NotEqual(t, originalToken, rotatedSession.ReconnectToken)
	require.Same(t, rotatedSession, sessionManager.GetSessionByToken(rotatedSession.ReconnectToken))

	var response *protocol.ReconnectedPayload
	for _, message := range provisionalClient.SentMessages() {
		if message.Type != protocol.MsgReconnected {
			continue
		}
		response, err = codec.ParsePayload[protocol.ReconnectedPayload](message)
		require.NoError(t, err)
	}
	require.NotNil(t, response)
	require.Equal(t, "player-1", response.PlayerID)
	require.Equal(t, gameRoom.Code, response.RoomCode)
	require.Equal(t, rotatedSession.ReconnectToken, response.ReconnectToken)

	h.handleReady(provisionalClient, true)
	player, ok := gameRoom.PlayerForTest("player-1")
	require.True(t, ok)
	require.True(t, player.Ready)
	recipient, ok := gameRoom.PrivateRecipient("player-1")
	require.True(t, ok)
	require.Same(t, provisionalClient, recipient)
	require.False(t, matcher.RemoveFromQueue(provisionalClient), "a room-bound reconnect must retire incompatible queued matchmaking work")
	require.Zero(t, matcher.GetQueueLength())
	server.AssertExpectations(t)
}

func TestHandleReconnectPublishesRotatedIdentityBeforeMigratedMatchQueue(t *testing.T) {
	sessionManager := session.NewSessionManager()
	original := sessionManager.CreateSession("queued-player", "Queued Player")
	originalToken := original.ReconnectToken
	sessionManager.SetOffline(original.PlayerID)
	previous := testutil.NewSimpleClient(original.PlayerID, original.PlayerName)
	matcher := match.NewMatcher(match.MatcherDeps{QueueTimeout: time.Hour})
	require.True(t, matcher.AddToQueue(previous))
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })

	provisional := testutil.NewSimpleClient("temporary-queued", "Temporary")
	sessionManager.CreateSession(provisional.GetID(), provisional.GetName())
	server := new(testutil.MockServer)
	server.On(
		"RebindClient",
		provisional.GetID(),
		original.PlayerID,
		original.PlayerName,
		"",
		provisional,
	).Run(func(args mock.Arguments) {
		client := args.Get(4).(*testutil.SimpleClient)
		client.ID = args.String(1)
		client.Name = args.String(2)
		client.RoomCode = args.String(3)
	}).Return(previous, nil).Once()
	h := NewHandler(HandlerDeps{
		Server:         server,
		Matcher:        matcher,
		SessionManager: sessionManager,
	})

	h.handleReconnect(provisional, codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		Token:    originalToken,
		PlayerID: original.PlayerID,
	}))

	messages := provisional.SentMessages()
	require.Len(t, messages, 2)
	require.Equal(t, protocol.MsgReconnected, messages[0].Type)
	require.Equal(t, protocol.MsgMatchQueued, messages[1].Type)
	reconnected, err := codec.ParsePayload[protocol.ReconnectedPayload](messages[0])
	require.NoError(t, err)
	require.Equal(t, original.PlayerID, reconnected.PlayerID)
	require.NotEqual(t, originalToken, reconnected.ReconnectToken)
	require.True(t, matcher.RemoveFromQueue(provisional))
	server.AssertExpectations(t)
}

func TestHandleReconnectSnapshotFailureClosesBothConnectionsAndCancelsMatch(t *testing.T) {
	sessionManager := session.NewSessionManager()
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	original := testutil.NewSimpleClient("snapshot-failure-player", "Original")
	peer := testutil.NewSimpleClient("snapshot-failure-peer", "Peer")
	gameRoom, err := roomManager.CreateRoom(original)
	require.NoError(t, err)
	_, err = roomManager.JoinRoom(peer, gameRoom.Code)
	require.NoError(t, err)
	playerSession := sessionManager.CreateSession(original.GetID(), original.GetName())
	sessionManager.SetRoom(original.GetID(), gameRoom.Code)
	sessionManager.SetOffline(original.GetID())
	roomManager.NotifyPlayerOffline(original)

	previous := &closeRecordingClient{SimpleClient: testutil.NewSimpleClient(original.GetID(), original.GetName())}
	matcher := match.NewMatcher(match.MatcherDeps{QueueTimeout: time.Hour})
	require.True(t, matcher.AddToQueue(previous))
	t.Cleanup(func() { require.NoError(t, matcher.Close()) })
	replacement := &reconnectSnapshotFailureClient{synchronizedClient: &synchronizedClient{
		id:       "temporary-snapshot-failure",
		name:     "Temporary",
		messages: make([]*protocol.Message, 0),
	}}
	sessionManager.CreateSession(replacement.GetID(), replacement.GetName())
	server := new(testutil.MockServer)
	server.On(
		"RebindClient",
		replacement.GetID(),
		original.GetID(),
		original.GetName(),
		gameRoom.Code,
		replacement,
	).Run(func(args mock.Arguments) {
		client := args.Get(4).(*reconnectSnapshotFailureClient)
		client.mu.Lock()
		client.id = args.String(1)
		client.name = args.String(2)
		client.roomCode = args.String(3)
		client.mu.Unlock()
	}).Return(previous, nil).Once()
	h := NewHandler(HandlerDeps{
		Server:         server,
		RoomManager:    roomManager,
		Matcher:        matcher,
		SessionManager: sessionManager,
	})

	h.handleReconnect(replacement, codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		Token:    playerSession.ReconnectToken,
		PlayerID: playerSession.PlayerID,
	}))

	require.True(t, replacement.isClosed())
	require.True(t, previous.isClosed())
	require.Zero(t, matcher.GetQueueLength())
	require.Empty(t, replacement.sentMessages())
	server.AssertExpectations(t)
}

func TestHandleReconnectRejectsRoomBoundProvisionalClientBeforeConsumingToken(t *testing.T) {
	sessionManager := session.NewSessionManager()
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	provisional := testutil.NewSimpleClient("temporary-room-member", "Temporary")
	gameRoom, err := roomManager.CreateRoom(provisional)
	require.NoError(t, err)

	provisionalSession := sessionManager.CreateSession(provisional.GetID(), provisional.GetName())
	restoredSession := sessionManager.CreateSession("restored-player", "Restored")
	sessionManager.SetOffline(restoredSession.PlayerID)
	server := new(testutil.MockServer)
	h := NewHandler(HandlerDeps{
		Server:         server,
		RoomManager:    roomManager,
		SessionManager: sessionManager,
	})

	h.handleReconnect(provisional, codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		Token:    restoredSession.ReconnectToken,
		PlayerID: restoredSession.PlayerID,
	}))

	require.Equal(t, "temporary-room-member", provisional.GetID())
	require.Equal(t, gameRoom.Code, provisional.GetRoom())
	require.Same(t, provisionalSession, sessionManager.GetSession(provisional.GetID()))
	require.Same(t, restoredSession, sessionManager.GetSessionByToken(restoredSession.ReconnectToken))
	recipient, online := gameRoom.PrivateRecipient(provisional.GetID())
	require.True(t, online)
	require.Same(t, provisional, recipient)
	require.Len(t, provisional.SentMessages(), 1)
	errorPayload, err := codec.ParsePayload[protocol.ErrorPayload](provisional.SentMessages()[0])
	require.NoError(t, err)
	require.Equal(t, protocol.ErrCodeReconnectInvalid, errorPayload.Code)
	server.AssertNotCalled(t, "RebindClient")
}

func TestTryRestoreRoomStateRejectsRoomRemovedAfterReconnectBeforeRegistryCallback(t *testing.T) {
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	sessionManager := session.NewSessionManager()
	original := testutil.NewSimpleClient("restore-race-player", "Player")
	peer := &reconnectOnlineBarrierClient{
		SimpleClient: testutil.NewSimpleClient("restore-race-peer", "Peer"),
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	gameRoom, err := roomManager.CreateRoom(original)
	require.NoError(t, err)
	_, err = roomManager.JoinRoom(peer, gameRoom.Code)
	require.NoError(t, err)
	playerSession := sessionManager.CreateSession(original.GetID(), original.GetName())
	sessionManager.SetRoom(original.GetID(), gameRoom.Code)
	game := session.NewGameSession(gameRoom, nil, config.GameConfig{TurnTimeout: 3600, BidTimeout: 3600})
	t.Cleanup(game.StopAllTimers)
	h := NewHandler(HandlerDeps{RoomManager: roomManager, SessionManager: sessionManager})
	require.True(t, h.SetGameSession(gameRoom.Code, game))
	roomManager.NotifyPlayerOffline(original)

	removalEntered := make(chan struct{})
	releaseRemoval := make(chan struct{})
	var peerReleaseOnce sync.Once
	var removalReleaseOnce sync.Once
	t.Cleanup(func() {
		peerReleaseOnce.Do(func() { close(peer.release) })
		removalReleaseOnce.Do(func() { close(releaseRemoval) })
	})
	roomManager.SetOnRoomRemoved(func(removal room.RoomRemoval) {
		close(removalEntered)
		<-releaseRemoval
		h.handleRoomRemoved(removal)
	})
	replacement := testutil.NewSimpleClient(original.GetID(), original.GetName())
	restored := &session.RestoredSession{
		PlayerID:       playerSession.PlayerID,
		PlayerName:     playerSession.PlayerName,
		ReconnectToken: playerSession.ReconnectToken,
		RoomCode:       gameRoom.Code,
	}
	payload := &protocol.ReconnectedPayload{}
	restoredRooms := make(chan *room.Room, 1)
	go func() {
		restoredRooms <- h.tryRestoreRoomState(replacement, restored)
	}()
	waitHandlerSignal(t, peer.entered, "ReconnectPlayer online delivery")

	removed := make(chan bool, 1)
	go func() { removed <- roomManager.RemoveRoom(gameRoom, room.RoomRemovalRollback) }()
	select {
	case <-removalEntered:
		t.Fatal("room removal passed the blocked PlayerOnline publication")
	case <-removed:
		t.Fatal("room removal completed before PlayerOnline publication")
	default:
	}
	peerReleaseOnce.Do(func() { close(peer.release) })
	restoredRoom := waitHandlerValue(t, restoredRooms, "room reconnect completion")
	waitHandlerSignal(t, removalEntered, "room removal after PlayerOnline publication")
	h.sendReconnected(replacement, restored, restoredRoom, payload)

	require.Empty(t, replacement.GetRoom())
	require.Empty(t, payload.RoomCode)
	require.Nil(t, payload.GameState)
	removalReleaseOnce.Do(func() { close(releaseRemoval) })
	require.True(t, waitHandlerValue(t, removed, "room removal completion"))
	require.Nil(t, h.GetGameSession(gameRoom.Code))
}

func TestReconnectRemovalDuringFinalSendOrdersRoomLeftAfterReconnected(t *testing.T) {
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	sessionManager := session.NewSessionManager()
	original := &synchronizedClient{id: "ordered-player", name: "Player"}
	gameRoom, err := roomManager.CreateRoom(original)
	require.NoError(t, err)
	playerSession := sessionManager.CreateSession(original.GetID(), original.GetName())
	sessionManager.SetRoom(original.GetID(), gameRoom.Code)
	game := session.NewGameSession(gameRoom, nil, config.GameConfig{TurnTimeout: 3600, BidTimeout: 3600})
	t.Cleanup(game.StopAllTimers)
	h := NewHandler(HandlerDeps{RoomManager: roomManager, SessionManager: sessionManager})
	require.True(t, h.SetGameSession(gameRoom.Code, game))

	replacement := &reconnectedSendBarrierClient{
		synchronizedClient: &synchronizedClient{id: original.GetID(), name: original.GetName()},
		entered:            make(chan struct{}),
		release:            make(chan struct{}),
	}
	restored := &session.RestoredSession{
		PlayerID:       playerSession.PlayerID,
		PlayerName:     playerSession.PlayerName,
		ReconnectToken: playerSession.ReconnectToken,
		RoomCode:       gameRoom.Code,
	}
	restoredRoom := h.tryRestoreRoomState(replacement, restored)
	require.Same(t, gameRoom, restoredRoom)
	payload := &protocol.ReconnectedPayload{
		PlayerID:       restored.PlayerID,
		PlayerName:     restored.PlayerName,
		ReconnectToken: restored.ReconnectToken,
	}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(replacement.release) }) })
	sendDone := make(chan struct{})
	go func() {
		h.sendReconnected(replacement, restored, restoredRoom, payload)
		close(sendDone)
	}()
	waitHandlerSignal(t, replacement.entered, "blocked final Reconnected enqueue")

	removed := make(chan bool, 1)
	go func() { removed <- roomManager.RemoveRoom(gameRoom, room.RoomRemovalRollback) }()
	require.Same(t, gameRoom, roomManager.GetRoom(gameRoom.Code), "removal must wait for the final Reconnected publication")
	select {
	case <-removed:
		t.Fatal("room removal completed before Reconnected publication")
	default:
	}
	releaseOnce.Do(func() { close(replacement.release) })
	waitHandlerSignal(t, sendDone, "final Reconnected enqueue")
	require.True(t, waitHandlerValue(t, removed, "ordered room removal"))

	messages := replacement.sentMessages()
	require.Len(t, messages, 2)
	require.Equal(t, protocol.MsgReconnected, messages[0].Type)
	require.Equal(t, protocol.MsgRoomLeft, messages[1].Type)
}

func TestFinalReconnectUsesCurrentReplacementSession(t *testing.T) {
	gameRoom, oldGame, clients := newRemovalSession(t, "same-room-session-swap")
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	roomManager.AddRoomForTest(gameRoom)
	sessionManager := session.NewSessionManager()
	playerSession := sessionManager.CreateSession(clients[0].GetID(), clients[0].GetName())
	sessionManager.SetRoom(clients[0].GetID(), gameRoom.Code)
	h := NewHandler(HandlerDeps{RoomManager: roomManager, SessionManager: sessionManager})
	require.True(t, h.SetGameSession(gameRoom.Code, oldGame))
	oldGame.Start()
	t.Cleanup(oldGame.StopAllTimers)

	newGame := session.NewGameSession(gameRoom, nil, config.GameConfig{TurnTimeout: 3600, BidTimeout: 3600})
	require.True(t, h.SetGameSession(gameRoom.Code, newGame))
	newGame.Start()
	t.Cleanup(newGame.StopAllTimers)
	newGameID, _, member := newGame.CurrentGameContext(clients[0].GetID())
	require.True(t, member)

	restored := &session.RestoredSession{
		PlayerID:       playerSession.PlayerID,
		PlayerName:     playerSession.PlayerName,
		ReconnectToken: playerSession.ReconnectToken,
		RoomCode:       gameRoom.Code,
	}
	payload := &protocol.ReconnectedPayload{PlayerID: restored.PlayerID, PlayerName: restored.PlayerName}
	h.sendReconnected(clients[0], restored, gameRoom, payload)

	reconnected := requireLastReconnectedPayload(t, clients[0].SentMessages())
	require.Equal(t, gameRoom.Code, reconnected.RoomCode)
	require.NotNil(t, reconnected.GameState)
	require.Equal(t, newGameID, reconnected.GameState.GameID)
}

func TestEndedSessionSettlementIsNotRestoredToReplacementRoomMember(t *testing.T) {
	gameRoom, completedGame, oldPlayers := newRemovalSession(t, "ended-member-replacement")
	roomManager := room.NewRoomManager(nil, config.GameConfig{RoomTimeout: 60})
	roomManager.AddRoomForTest(gameRoom)
	sessionManager := session.NewSessionManager()
	h := NewHandler(HandlerDeps{RoomManager: roomManager, SessionManager: sessionManager})
	require.True(t, h.SetGameSession(gameRoom.Code, completedGame))
	completedGame.Start()
	t.Cleanup(completedGame.StopAllTimers)
	completedGame.EndWithSettlementForTest(&protocol.GameSettlementDTO{
		WinnerID:         oldPlayers[0].GetID(),
		WinnerName:       oldPlayers[0].GetName(),
		WinnerIsLandlord: true,
		Multiplier:       8,
		PlayerHands: []protocol.PlayerHand{{
			PlayerID: oldPlayers[2].GetID(),
			Cards:    []protocol.CardInfo{{Suit: 1, Rank: 14, Color: 1}},
		}},
	})

	require.True(t, roomManager.LeaveRoom(oldPlayers[2]))
	newcomer := testutil.NewSimpleClient("newcomer", "New Player")
	_, err := roomManager.JoinRoom(newcomer, gameRoom.Code)
	require.NoError(t, err)
	newcomerSession := sessionManager.CreateSession(newcomer.GetID(), newcomer.GetName())
	sessionManager.SetRoom(newcomer.GetID(), gameRoom.Code)
	roomManager.NotifyPlayerOffline(newcomer)
	reconnectedNewcomer := testutil.NewSimpleClient(newcomer.GetID(), newcomer.GetName())
	restoredNewcomer := &session.RestoredSession{
		PlayerID:       newcomerSession.PlayerID,
		PlayerName:     newcomerSession.PlayerName,
		ReconnectToken: newcomerSession.ReconnectToken,
		RoomCode:       gameRoom.Code,
	}
	restoredRoom := h.tryRestoreRoomState(reconnectedNewcomer, restoredNewcomer)
	h.sendReconnected(reconnectedNewcomer, restoredNewcomer, restoredRoom, &protocol.ReconnectedPayload{
		PlayerID: restoredNewcomer.PlayerID, PlayerName: restoredNewcomer.PlayerName,
	})
	newcomerPayload := requireLastReconnectedPayload(t, reconnectedNewcomer.SentMessages())
	require.Equal(t, gameRoom.Code, newcomerPayload.RoomCode)
	require.NotNil(t, newcomerPayload.GameState)
	require.Equal(t, "waiting", newcomerPayload.GameState.Phase)
	require.Nil(t, newcomerPayload.GameState.Settlement, "a new room member must not receive the previous settlement")
	require.Len(t, newcomerPayload.GameState.Players, 3)
	require.Contains(t, []string{
		newcomerPayload.GameState.Players[0].ID,
		newcomerPayload.GameState.Players[1].ID,
		newcomerPayload.GameState.Players[2].ID,
	}, newcomer.GetID())

	originalSession := sessionManager.CreateSession(oldPlayers[0].GetID(), oldPlayers[0].GetName())
	sessionManager.SetRoom(oldPlayers[0].GetID(), gameRoom.Code)
	restoredOriginal := &session.RestoredSession{
		PlayerID:       originalSession.PlayerID,
		PlayerName:     originalSession.PlayerName,
		ReconnectToken: originalSession.ReconnectToken,
		RoomCode:       gameRoom.Code,
	}
	h.sendReconnected(oldPlayers[0], restoredOriginal, gameRoom, &protocol.ReconnectedPayload{
		PlayerID: restoredOriginal.PlayerID, PlayerName: restoredOriginal.PlayerName,
	})
	originalPayload := requireLastReconnectedPayload(t, oldPlayers[0].SentMessages())
	require.NotNil(t, originalPayload.GameState)
	require.NotNil(t, originalPayload.GameState.Settlement)
	require.EqualValues(t, 8, originalPayload.GameState.Settlement.Multiplier)
}

func TestHandleReconnectReturnsDistinctCredentialErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(*session.SessionManager) (token, playerID string)
		wantCode int
	}{
		{
			name: "invalid",
			setup: func(*session.SessionManager) (string, string) {
				return "invalid-token", "player-1"
			},
			wantCode: protocol.ErrCodeReconnectInvalid,
		},
		{
			name: "expired",
			setup: func(manager *session.SessionManager) (string, string) {
				playerSession := manager.CreateSession("player-1", "Player One")
				manager.SetOffline("player-1")
				playerSession.DisconnectedAt = time.Now().Add(-3 * time.Minute)
				return playerSession.ReconnectToken, playerSession.PlayerID
			},
			wantCode: protocol.ErrCodeReconnectExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := session.NewSessionManager()
			token, playerID := tt.setup(manager)
			provisional := testutil.NewSimpleClient("temporary", "Temporary")
			manager.CreateSession(provisional.GetID(), provisional.GetName())
			h := NewHandler(HandlerDeps{
				Server:         new(testutil.MockServer),
				SessionManager: manager,
			})

			h.handleReconnect(provisional, codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
				Token:    token,
				PlayerID: playerID,
			}))

			require.Len(t, provisional.SentMessages(), 1)
			errorPayload, err := codec.ParsePayload[protocol.ErrorPayload](provisional.SentMessages()[0])
			require.NoError(t, err)
			require.Equal(t, tt.wantCode, errorPayload.Code)
			require.NotNil(t, manager.GetSession("temporary"))
		})
	}
}

func TestHandleReconnectRebindFailureRestoresConsumedCredential(t *testing.T) {
	t.Parallel()

	manager := session.NewSessionManager()
	original := manager.CreateSession("player-1", "Player One")
	manager.SetOffline(original.PlayerID)
	originalToken := original.ReconnectToken
	provisional := testutil.NewSimpleClient("temporary", "Temporary")
	manager.CreateSession(provisional.GetID(), provisional.GetName())

	server := new(testutil.MockServer)
	server.On(
		"RebindClient",
		provisional.GetID(),
		original.PlayerID,
		original.PlayerName,
		"",
		provisional,
	).Return(nil, errors.New("temporary mapping disappeared")).Once()
	h := NewHandler(HandlerDeps{Server: server, SessionManager: manager})

	h.handleReconnect(provisional, codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		Token:    originalToken,
		PlayerID: original.PlayerID,
	}))

	require.Same(t, original, manager.GetSessionByToken(originalToken))
	require.Equal(t, originalToken, original.ReconnectToken)
	require.False(t, manager.IsOnline(original.PlayerID))
	require.Nil(t, manager.GetSession("temporary"))
	require.Len(t, provisional.SentMessages(), 1)
	errorPayload, err := codec.ParsePayload[protocol.ErrorPayload](provisional.SentMessages()[0])
	require.NoError(t, err)
	require.Equal(t, protocol.ErrCodeUnknown, errorPayload.Code)
	server.AssertExpectations(t)
}

var _ types.ServerInterface = (*testutil.MockServer)(nil)

func waitHandlerSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitHandlerValue[T any](t *testing.T, values <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func requireLastReconnectedPayload(t *testing.T, messages []*protocol.Message) *protocol.ReconnectedPayload {
	t.Helper()
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Type != protocol.MsgReconnected {
			continue
		}
		payload, err := codec.ParsePayload[protocol.ReconnectedPayload](messages[index])
		require.NoError(t, err)
		return payload
	}
	t.Fatal("missing Reconnected message")
	return nil
}
