package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gorilla/websocket"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
	"github.com/stretchr/testify/require"
)

func identityServer(t *testing.T, redis *miniredis.Miniredis) (*Server, *httptest.Server) {
	t.Helper()
	cfg := config.Default()
	cfg.Redis.Addr = redis.Addr()
	cfg.Security.RateLimit.MaxPerSecond = 1000
	cfg.Security.RateLimit.MaxPerMinute = 10000
	cfg.Game.BidTimeout, cfg.Game.TurnTimeout, cfg.Game.OfflineWaitTimeout = 600, 600, 600
	s, err := NewServer(cfg)
	require.NoError(t, err)
	httpServer := httptest.NewServer(s.routes())
	t.Cleanup(func() {
		httpServer.Close()
		s.clientsMu.RLock()
		clients := make([]*Client, 0, len(s.clients))
		for _, c := range s.clients {
			clients = append(clients, c)
		}
		s.clientsMu.RUnlock()
		for _, c := range clients {
			if gs := s.handler.GetGameSession(c.GetRoom()); gs != nil {
				gs.StopAllTimers()
			}
			_ = c.conn.Close()
		}
		_ = s.redis.Close()
	})
	return s, httpServer
}

func dialIdentity(t *testing.T, url string, cookie *http.Cookie, browser bool) (*websocket.Conn, *http.Response) {
	t.Helper()
	headers := http.Header{}
	if browser {
		headers.Set("Origin", url)
	}
	if cookie != nil {
		headers.Set("Cookie", cookie.String())
	}
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http")+"/ws", headers)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, resp
}

func receivePayload[T any](t *testing.T, conn *websocket.Conn, want protocol.MessageType) T {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	for {
		_, data, err := conn.ReadMessage()
		require.NoError(t, err)
		msg, err := codec.Decode(data)
		require.NoError(t, err)
		if msg.Type != want {
			continue
		}
		p, err := codec.ParsePayload[T](msg)
		require.NoError(t, err)
		return *p
	}
}

func sendIdentityMessage(t *testing.T, conn *websocket.Conn, typ protocol.MessageType, payload any) {
	t.Helper()
	data, err := codec.Encode(codec.MustNewMessage(typ, payload))
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, data))
}

func TestCookieIdentityRefreshReopenReplacementAndRestart(t *testing.T) {
	redis := miniredis.RunT(t)
	s, web := identityServer(t, redis)
	first, resp := dialIdentity(t, web.URL, nil, true)
	connected := receivePayload[protocol.ConnectedPayload](t, first, protocol.MsgConnected)
	require.Empty(t, connected.ReconnectToken)
	require.Len(t, resp.Cookies(), 1)
	cookie := resp.Cookies()[0]
	require.True(t, cookie.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Greater(t, cookie.MaxAge, 24*60*60)
	require.True(t, cookie.Expires.After(time.Now()))
	require.NotContains(t, redis.Dump(), strings.Split(cookie.Value, ".")[1])
	sendIdentityMessage(t, first, protocol.MsgCreateRoom, nil)
	created := receivePayload[protocol.RoomCreatedPayload](t, first, protocol.MsgRoomCreated)
	// Refresh / second tab: cookie alone restores the same waiting room and evicts the previous connection.
	second, _ := dialIdentity(t, web.URL, cookie, true)
	receivePayload[protocol.ConnectedPayload](t, second, protocol.MsgConnected)
	restored := receivePayload[protocol.ReconnectedPayload](t, second, protocol.MsgReconnected)
	require.Equal(t, connected.PlayerID, restored.PlayerID)
	require.Equal(t, connected.PlayerName, restored.PlayerName)
	require.Equal(t, created.RoomCode, restored.RoomCode)
	for {
		_, _, err := first.ReadMessage()
		if err != nil {
			require.True(t, websocket.IsCloseError(err, closeReplaced))
			break
		}
	}
	// Closing every tab retains the room. A new tab has no sessionStorage state.
	require.NoError(t, second.Close())
	require.Eventually(t, func() bool { return !s.sessionManager.IsOnline(connected.PlayerID) }, time.Second, time.Millisecond)
	third, _ := dialIdentity(t, web.URL, cookie, true)
	got := receivePayload[protocol.ReconnectedPayload](t, third, protocol.MsgReconnected)
	require.Equal(t, created.RoomCode, got.RoomCode)
	require.NoError(t, s.leaderboard.RecordGameResult(context.Background(), connected.PlayerID, connected.PlayerName, true, true))
	_ = third.Close()
	web.Close()
	// Another server instance represents a restart: Redis identity/stats survive; rooms do not.
	restarted, nextWeb := identityServer(t, redis)
	fourth, _ := dialIdentity(t, nextWeb.URL, cookie, true)
	got = receivePayload[protocol.ReconnectedPayload](t, fourth, protocol.MsgReconnected)
	require.Equal(t, connected.PlayerID, got.PlayerID)
	require.Equal(t, connected.PlayerName, got.PlayerName)
	require.Empty(t, got.RoomCode)
	stats, err := restarted.leaderboard.GetPlayerStats(context.Background(), got.PlayerID)
	require.NoError(t, err)
	require.Equal(t, 1, stats.TotalGames)
}

func TestIdentityOriginRedisFailureAndTampering(t *testing.T) {
	redis := miniredis.RunT(t)
	s, web := identityServer(t, redis)
	conn, resp := dialIdentity(t, web.URL, nil, true)
	original := receivePayload[protocol.ConnectedPayload](t, conn, protocol.MsgConnected)
	cookie := resp.Cookies()[0]
	for _, origin := range []string{"https://evil.example", "null", web.URL + "/path"} {
		_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(web.URL, "http")+"/ws", http.Header{"Origin": {origin}, "Cookie": {cookie.String()}})
		require.Error(t, err)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	}
	redis.SetError("ERR unavailable")
	_, failed, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(web.URL, "http")+"/ws", http.Header{"Origin": {web.URL}, "Cookie": {cookie.String()}})
	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, failed.StatusCode)
	require.Empty(t, failed.Cookies())
	require.Equal(t, original.PlayerID, s.GetClientByID(original.PlayerID).GetID())
	redis.SetError("")
	recovered, _ := dialIdentity(t, web.URL, cookie, true)
	require.Equal(t, original.PlayerID, receivePayload[protocol.ReconnectedPayload](t, recovered, protocol.MsgReconnected).PlayerID)
	bad := *cookie
	bad.Value = strings.Split(cookie.Value, ".")[0] + "." + strings.Repeat("0", 64)
	tampered, _ := dialIdentity(t, web.URL, &bad, true)
	require.NotEqual(t, original.PlayerID, receivePayload[protocol.ConnectedPayload](t, tampered, protocol.MsgConnected).PlayerID)
	// Secure is explicitly configurable for TLS terminated by a reverse proxy.
	s.config.Server.CookieSecure = true
	secure, secureResp := dialIdentity(t, web.URL, nil, true)
	require.True(t, secureResp.Cookies()[0].Secure)
	_ = secure.Close()
}

func TestTerminalReconnectUsesPersistentCredential(t *testing.T) {
	redis := miniredis.RunT(t)
	_, web := identityServer(t, redis)
	first, _ := dialIdentity(t, web.URL, nil, false)
	identity := receivePayload[protocol.ConnectedPayload](t, first, protocol.MsgConnected)
	require.Len(t, identity.ReconnectToken, 64)
	_, restarted := identityServer(t, redis)
	next, _ := dialIdentity(t, restarted.URL, nil, false)
	receivePayload[protocol.ConnectedPayload](t, next, protocol.MsgConnected)
	sendIdentityMessage(t, next, protocol.MsgReconnect, protocol.ReconnectPayload{PlayerID: identity.PlayerID, Token: identity.ReconnectToken})
	require.Equal(t, identity.PlayerID, receivePayload[protocol.ReconnectedPayload](t, next, protocol.MsgReconnected).PlayerID)
}

func TestQueuedMatchCannotUseReplacedConnection(t *testing.T) {
	s := newReconnectServer()
	old := NewClient(s, nil)
	s.registerClient(old)
	s.sessionManager.CreateSession(old.ID, old.Name)
	next := NewClient(s, nil)
	s.RegisterClient(old.ID, next)
	called := false
	require.False(t, s.withActiveClients([]types.ClientInterface{old}, func() { called = true }))
	require.False(t, called)
	require.True(t, s.withActiveClients([]types.ClientInterface{next}, func() { called = true }))
	require.True(t, called)
}

func TestCookieRecoversGameAndOfflineSettlementWithoutSecondGame(t *testing.T) {
	redis := miniredis.RunT(t)
	s, web := identityServer(t, redis)
	sockets := make([]*websocket.Conn, 3)
	cookies := make([]*http.Cookie, 3)
	players := make([]types.ClientInterface, 3)
	ids := make([]string, 3)
	for i := range sockets {
		var resp *http.Response
		sockets[i], resp = dialIdentity(t, web.URL, nil, true)
		identity := receivePayload[protocol.ConnectedPayload](t, sockets[i], protocol.MsgConnected)
		ids[i], cookies[i] = identity.PlayerID, resp.Cookies()[0]
		players[i] = s.GetClientByID(identity.PlayerID)
	}
	s.connectionMu.Lock()
	r, err := s.roomManager.CreateMatchedRoom(players)
	require.NoError(t, err)
	gs := session.NewGameSession(r, s.leaderboard, s.config.Game)
	s.handler.SetGameSession(r.Code, gs)
	gs.Start()
	s.connectionMu.Unlock()
	t.Cleanup(gs.StopAllTimers)
	// Reload while cards are being dealt must receive only this player's cards.
	reloaded, _ := dialIdentity(t, web.URL, cookies[0], true)
	state := receivePayload[protocol.ReconnectedPayload](t, reloaded, protocol.MsgReconnected)
	require.Equal(t, r.Code, state.RoomCode)
	require.Len(t, state.GameState.Hand, 17)
	require.Empty(t, state.GameState.BottomCards)
	// An authenticated identity in a retained game cannot start another game.
	sendIdentityMessage(t, reloaded, protocol.MsgPracticeMatch, nil)
	receivePayload[protocol.ErrorPayload](t, reloaded, protocol.MsgError)
	require.Equal(t, 1, s.roomManager.GetActiveGamesCount())
	for _, conn := range append(sockets, reloaded) {
		_ = conn.Close()
	}
	require.Eventually(t, func() bool {
		for _, id := range ids {
			if s.sessionManager.IsOnline(id) {
				return false
			}
		}
		return true
	}, time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return gs.BuildGameStateDTO(ids[0], s.sessionManager).Phase == "bidding" }, 5*time.Second, 10*time.Millisecond)
	dto := gs.BuildGameStateDTO(ids[0], s.sessionManager)
	require.NoError(t, gs.HandleBid(dto.CurrentTurn, true))
	for range 2 {
		dto = gs.BuildGameStateDTO(ids[0], s.sessionManager)
		require.NoError(t, gs.HandleBid(dto.CurrentTurn, false))
	}
	for turn := 0; turn < 300; turn++ {
		dto = gs.BuildGameStateDTO(ids[0], s.sessionManager)
		if dto.Phase == "ended" {
			break
		}
		own := gs.BuildGameStateDTO(dto.CurrentTurn, s.sessionManager)
		var opponent rule.ParsedHand
		if !own.MustPlay {
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
	require.Equal(t, "ended", gs.BuildGameStateDTO(ids[0], s.sessionManager).Phase)
	require.Equal(t, r.Code, s.sessionManager.Snapshot(ids[0]).LastRoomCode)
	returned, _ := dialIdentity(t, web.URL, cookies[0], true)
	resultState := receivePayload[protocol.ReconnectedPayload](t, returned, protocol.MsgReconnected)
	require.Equal(t, r.Code, resultState.RoomCode)
	require.Equal(t, "ended", resultState.GameState.Phase)
	result := receivePayload[protocol.GameOverPayload](t, returned, protocol.MsgGameOver)
	require.Len(t, result.Scores, 3)
	stats, err := s.leaderboard.GetPlayerStats(context.Background(), ids[0])
	require.NoError(t, err)
	require.Equal(t, 1, stats.TotalGames)
	sendIdentityMessage(t, returned, protocol.MsgCreateRoom, nil)
	next := receivePayload[protocol.RoomCreatedPayload](t, returned, protocol.MsgRoomCreated)
	another, _ := dialIdentity(t, web.URL, cookies[0], true)
	restored := receivePayload[protocol.ReconnectedPayload](t, another, protocol.MsgReconnected)
	require.Equal(t, next.RoomCode, restored.RoomCode)
	require.Nil(t, restored.GameState)
}
