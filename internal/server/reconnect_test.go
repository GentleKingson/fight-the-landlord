package server

import (
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/server/handler"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"sync"
	"testing"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/stretchr/testify/require"
)

func TestRecoveredClientIdentityAndRoomAreRetained(t *testing.T) {
	s := &Server{clients: map[string]*Client{}, sessionManager: session.NewSessionManager()}
	s.sessionManager.CreateSession("original", "Original Name")
	c := NewClient(s, nil)
	s.RegisterClient("original", c)
	require.Equal(t, "original", c.GetID())
	require.Equal(t, "Original Name", c.GetName())
	c.SetRoom("123456")
	require.Equal(t, "123456", s.sessionManager.Snapshot("original").RoomCode)
	require.Nil(t, s.GetClientByID("absent"))
	// A delayed disconnect from the old socket must not remove its replacement.
	old := NewClient(s, nil)
	old.ID = "original"
	s.unregisterClient(old)
	require.Same(t, c, s.GetClientByID("original"))
}

func TestConcurrentSendAndClose(t *testing.T) {
	for range 100 {
		c := NewClient(nil, nil)
		msg := codec.MustNewMessage(protocol.MsgOnlineCount, protocol.OnlineCountPayload{Count: 1})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); c.SendMessage(msg) }()
		go func() { defer wg.Done(); c.Close() }()
		wg.Wait()
	}
}

func newReconnectServer() *Server {
	cfg := config.GameConfig{TurnTimeout: 600, BidTimeout: 600, OfflineWaitTimeout: 600}
	s := &Server{clients: map[string]*Client{}, sessionManager: session.NewSessionManager()}
	s.roomManager = room.NewRoomManager(nil, cfg)
	s.matcher = match.NewMatcher(match.MatcherDeps{RoomManager: s.roomManager, GameConfig: cfg})
	s.handler = handler.NewHandler(handler.HandlerDeps{Server: s, RoomManager: s.roomManager, Matcher: s.matcher, SessionManager: s.sessionManager})
	return s
}

func TestReconnectRetiresQueueAndTemporarySessions(t *testing.T) {
	s := newReconnectServer()
	current := NewClient(s, nil)
	s.registerClient(current)
	original := s.sessionManager.CreateSession(current.GetID(), current.GetName())
	for range 20 {
		s.matcher.AddToQueue(current)
		next := NewClient(s, nil)
		s.registerClient(next)
		temporary := s.sessionManager.CreateSession(next.GetID(), next.GetName())
		s.handler.Handle(next, codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{PlayerID: original.PlayerID, Token: original.ReconnectToken}))
		require.Zero(t, s.matcher.GetQueueLength())
		require.Nil(t, s.GetClientByID(temporary.PlayerID))
		require.Nil(t, s.sessionManager.Snapshot(temporary.PlayerID))
		require.Nil(t, s.sessionManager.GetSessionByToken(temporary.ReconnectToken))
		require.True(t, s.sessionManager.CanReconnect(original.ReconnectToken, original.PlayerID))
		// A delayed old callback must not remove the new client's queue entry.
		s.matcher.AddToQueue(next)
		current.handleDisconnect()
		require.Equal(t, 1, s.matcher.GetQueueLength())
		require.True(t, s.sessionManager.IsOnline(original.PlayerID))
		require.Same(t, next, s.GetClientByID(original.PlayerID))
		current = next
	}
	current.handleDisconnect()
	require.Zero(t, s.matcher.GetQueueLength())
	require.False(t, s.sessionManager.IsOnline(original.PlayerID))
}

func TestDisconnectAndReplacementSerializeOfflineEffects(t *testing.T) {
	s := newReconnectServer()
	old := NewClient(s, nil)
	s.registerClient(old)
	saved := s.sessionManager.CreateSession(old.GetID(), old.GetName())
	r, err := s.roomManager.CreateRoom(old)
	require.NoError(t, err)
	for _, id := range []string{"p2", "p3"} {
		_, err = s.roomManager.JoinRoom(testutil.NewSimpleClient(id, id), r.Code)
		require.NoError(t, err)
	}
	gs := session.NewGameSession(r, nil, config.GameConfig{BidTimeout: 600, OfflineWaitTimeout: 600})
	s.handler.SetGameSession(r.Code, gs)
	gs.Start()
	t.Cleanup(gs.StopAllTimers)
	blocked, release := make(chan struct{}), make(chan struct{})
	go gs.SerializeForRedis(func() { close(blocked); <-release })
	<-blocked
	disconnected := make(chan struct{})
	go func() { old.handleDisconnect(); close(disconnected) }()
	// Offline session metadata proves the old callback passed its identity check;
	// its game-state update is now blocked by our read lock.
	require.Eventually(t, func() bool { return !s.sessionManager.IsOnline(saved.PlayerID) }, time.Second, time.Millisecond)
	locked := s.connectionMu.TryLock()
	if locked {
		s.connectionMu.Unlock()
	}
	// Release even on assertion failure so cleanup cannot deadlock.
	close(release)
	require.False(t, locked)
	next := NewClient(s, nil)
	s.registerClient(next)
	s.sessionManager.CreateSession(next.GetID(), next.GetName())
	s.handler.Handle(next, codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{PlayerID: saved.PlayerID, Token: saved.ReconnectToken}))
	<-disconnected
	require.Same(t, next, s.GetClientByID(saved.PlayerID))
	require.True(t, s.sessionManager.IsOnline(saved.PlayerID))
	require.Same(t, next, r.Players[saved.PlayerID].Client)
	gs.SerializeForRedis(func() {
		for _, p := range gs.GetPlayersForSerialization() {
			if p.ID == saved.PlayerID {
				require.False(t, p.IsOffline)
			}
		}
	})
}

func TestOverlappingRestoresKeepNewestRoomClient(t *testing.T) {
	s := newReconnectServer()
	old := NewClient(s, nil)
	s.registerClient(old)
	s.sessionManager.CreateSession(old.GetID(), old.GetName())
	r, err := s.roomManager.CreateRoom(old)
	require.NoError(t, err)
	a, b := NewClient(s, nil), NewClient(s, nil)
	registered, release := make(chan struct{}), make(chan struct{})
	doneA, doneB := make(chan struct{}), make(chan struct{})
	// Pause A exactly between identity registration and room restoration.
	go func() {
		s.RestoreClient(old.GetID(), a, func() {
			close(registered)
			<-release
			a.SetRoom(r.Code)
			_ = s.roomManager.ReconnectPlayer(a, a)
		})
		close(doneA)
	}()
	<-registered
	go func() {
		s.RestoreClient(old.GetID(), b, func() {
			b.SetRoom(r.Code)
			_ = s.roomManager.ReconnectPlayer(b, b)
		})
		close(doneB)
	}()
	var overtook bool
	select {
	case <-doneB:
		overtook = true
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-doneA
	<-doneB
	require.False(t, overtook, "B must not restore while A is still recovering")
	require.Same(t, b, s.GetClientByID(old.GetID()))
	require.Same(t, b, r.Players[old.GetID()].Client)
	require.Equal(t, closeReplaced, a.closeCode)
	// A request already read by the displaced socket cannot take the room back.
	s.RestoreClient(old.GetID(), a, func() { t.Error("closed socket restored") })
	require.Same(t, b, s.GetClientByID(old.GetID()))
}
