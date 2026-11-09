package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redisserver "github.com/alicebob/miniredis/v2/server"
	"github.com/gorilla/websocket"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
	"github.com/stretchr/testify/require"
)

func connectedRecoveryGame(t *testing.T, mr *miniredis.Miniredis) (server *Server, httpServer *httptest.Server, guestCookies []*http.Cookie, gameRoom *room.Room, game *session.GameSession) {
	t.Helper()
	s, web := identityServer(t, mr)
	cookies := make([]*http.Cookie, 3)
	players := make([]types.ClientInterface, 3)
	for i := range players {
		conn, handshake := dialIdentity(t, web.URL, nil, true)
		identity := receivePayload[protocol.ConnectedPayload](t, conn, protocol.MsgConnected)
		cookies[i] = handshake[0]
		players[i] = s.GetClientByID(identity.PlayerID)
	}
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	r, err := s.roomManager.CreateMatchedRoom(players)
	require.NoError(t, err)
	gs := session.NewGameSession(r, s.leaderboard, s.config.Game)
	s.handler.SetGameSession(r.Code, gs)
	gs.Start()
	return s, web, cookies, r, gs
}

func completeRecoveryGame(t *testing.T, gs *session.GameSession, sm *session.SessionManager, playerID string) {
	t.Helper()
	require.Eventually(t, func() bool { return gs.BuildGameStateDTO(playerID, sm).Phase == "bidding" }, 5*time.Second, time.Millisecond)
	dto := gs.BuildGameStateDTO(playerID, sm)
	require.NoError(t, gs.HandleBid(dto.CurrentTurn, true))
	for range 2 {
		dto = gs.BuildGameStateDTO(playerID, sm)
		require.NoError(t, gs.HandleBid(dto.CurrentTurn, false))
	}
	for turn := 0; turn < 300; turn++ {
		dto = gs.BuildGameStateDTO(playerID, sm)
		if dto.Phase == "ended" {
			return
		}
		own := gs.BuildGameStateDTO(dto.CurrentTurn, sm)
		var opponent rule.ParsedHand
		if !own.MustPlay {
			var err error
			opponent, err = rule.ParseHand(convert.InfosToCards(own.LastPlayed))
			require.NoError(t, err)
		}
		cards := rule.FindSmallestBeatingCards(convert.InfosToCards(own.Hand), opponent)
		if len(cards) == 0 {
			require.NoError(t, gs.HandlePass(dto.CurrentTurn))
		} else {
			require.NoError(t, gs.HandlePlayCards(dto.CurrentTurn, convert.CardsToInfos(cards)))
		}
	}
	t.Fatal("game did not finish")
}

func TestRecoveredSettlementCleanupPreservesNewActiveGame(t *testing.T) {
	mr := miniredis.RunT(t)
	original, _, cookies, oldRoom, oldGame := connectedRecoveryGame(t, mr)
	id := oldRoom.PlayerOrder[0]
	completeRecoveryGame(t, oldGame, original.sessionManager, id)
	original.connectionMu.Lock()
	players := make([]types.ClientInterface, 3)
	for i, playerID := range oldRoom.PlayerOrder {
		players[i] = original.GetClientByID(playerID)
	}
	newRoom, err := original.roomManager.CreateMatchedRoom(players)
	require.NoError(t, err)
	newGame := session.NewGameSession(newRoom, original.leaderboard, original.config.Game)
	original.handler.SetGameSession(newRoom.Code, newGame)
	newGame.Start()
	original.connectionMu.Unlock()
	expected := newGame.BuildGameStateDTO(id, original.sessionManager)
	original.SuspendRecovery()

	restarted, web := identityServer(t, mr)
	require.Len(t, restarted.handler.GameSessions(), 2)
	require.Equal(t, newRoom.Code, restarted.sessionManager.Snapshot(id).RoomCode)
	retained := restarted.roomManager.GetRoom(oldRoom.Code)
	require.NotNil(t, retained)
	placeholder := retained.Players[id].Client
	require.Equal(t, oldRoom.Code, placeholder.GetRoom())
	// This is the expiry cleanup's exact client operation. Its local RoomID
	// still names A, while the shared identity has already been restored to B.
	placeholder.SetRoom("")
	require.Empty(t, placeholder.GetRoom())
	require.Equal(t, newRoom.Code, restarted.sessionManager.Snapshot(id).RoomCode)
	require.Empty(t, restarted.sessionManager.Snapshot(id).LastRoomCode)

	conn, _ := dialIdentity(t, web.URL, cookies[0], true)
	recovered := receivePayload[protocol.ReconnectedPayload](t, conn, protocol.MsgReconnected)
	require.Equal(t, newRoom.Code, recovered.RoomCode)
	require.Equal(t, expected.GameID, recovered.GameState.GameID)
	require.Equal(t, expected.Hand, recovered.GameState.Hand)
	sendIdentityMessage(t, conn, protocol.MsgPracticeMatch, nil)
	receivePayload[protocol.ErrorPayload](t, conn, protocol.MsgError)
	require.Equal(t, 1, restarted.roomManager.GetActiveGamesCount())
}

func TestFailedRecoveryClosesSocketAndRetryRestoresSnapshot(t *testing.T) {
	for _, pendingBeforeReplacement := range []bool{true, false} {
		t.Run(fmt.Sprintf("pending_before_replacement=%v", pendingBeforeReplacement), func(t *testing.T) {
			mr := miniredis.RunT(t)
			var failWrites atomic.Bool
			mr.Server().SetPreHook(func(peer *redisserver.Peer, cmd string, args ...string) bool {
				if failWrites.Load() && (cmd == "EVAL" || cmd == "EVALSHA") && len(args) > 2 && strings.HasPrefix(args[2], "game:v2:") {
					peer.WriteError("READONLY game writes temporarily unavailable")
					return true
				}
				return false
			})
			s, web, cookies, r, gs := connectedRecoveryGame(t, mr)
			id := r.PlayerOrder[0]
			expected := gs.BuildGameStateDTO(id, s.sessionManager)
			previous := s.GetClientByID(id)
			failWrites.Store(pendingBeforeReplacement)
			previous.Close()
			require.Eventually(t, func() bool { return s.GetClientByID(id) == nil }, time.Second, time.Millisecond)
			failWrites.Store(true)

			failed, _ := dialIdentity(t, web.URL, cookies[0], true)
			receivePayload[protocol.ConnectedPayload](t, failed, protocol.MsgConnected)
			receivePayload[protocol.ErrorPayload](t, failed, protocol.MsgError)
			_, _, err := failed.ReadMessage()
			require.True(t, websocket.IsCloseError(err, websocket.CloseNormalClosure), "failed recovery must trigger automatic reconnect, got %v", err)
			require.Eventually(t, func() bool { return s.GetClientByID(id) == nil }, time.Second, time.Millisecond)
			require.Equal(t, r.Code, s.sessionManager.Snapshot(id).RoomCode)
			r.SerializeForRedis(func() {
				if pendingBeforeReplacement {
					require.Same(t, previous, r.Players[id].Client)
				} else {
					require.NotSame(t, previous, r.Players[id].Client)
				}
			})

			failWrites.Store(false)
			retry, _ := dialIdentity(t, web.URL, cookies[0], true)
			restored := receivePayload[protocol.ReconnectedPayload](t, retry, protocol.MsgReconnected)
			require.Equal(t, r.Code, restored.RoomCode)
			require.Equal(t, expected.GameID, restored.GameState.GameID)
			require.Equal(t, expected.Hand, restored.GameState.Hand)
			r.Broadcast(codec.MustNewMessage(protocol.MsgPong, protocol.PongPayload{ServerTimestamp: 123}))
			received := receivePayload[protocol.PongPayload](t, retry, protocol.MsgPong)
			require.EqualValues(t, 123, received.ServerTimestamp, "broadcasts must reach the replacement socket")
		})
	}
}

func TestReconnectClearsMissingRoomAssociation(t *testing.T) {
	s, web := identityServer(t, miniredis.RunT(t))
	first, cookies := dialIdentity(t, web.URL, nil, true)
	identity := receivePayload[protocol.ConnectedPayload](t, first, protocol.MsgConnected)
	s.sessionManager.SetRoom(identity.PlayerID, "missing-room")
	retry, _ := dialIdentity(t, web.URL, cookies[0], true)
	restored := receivePayload[protocol.ReconnectedPayload](t, retry, protocol.MsgReconnected)
	require.Empty(t, restored.RoomCode)
	require.Empty(t, s.sessionManager.Snapshot(identity.PlayerID).RoomCode)
}
