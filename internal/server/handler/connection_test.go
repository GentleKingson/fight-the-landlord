package handler

import (
	"errors"
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
	require.True(t, matcher.RemoveFromQueue(provisionalClient), "rebind must transfer matcher ownership to the active connection")
	server.AssertExpectations(t)
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
