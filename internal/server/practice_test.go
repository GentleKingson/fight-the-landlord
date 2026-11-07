package server

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/handler"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/stretchr/testify/require"
)

type blockedPracticeClient struct {
	*Client
	once     sync.Once
	creating chan struct{}
	release  chan struct{}
	started  chan struct{}
}

func (c *blockedPracticeClient) SetRoom(code string) {
	c.once.Do(func() { close(c.creating); <-c.release })
	c.Client.SetRoom(code)
}

func (c *blockedPracticeClient) SendMessage(msg *protocol.Message) {
	c.Client.SendMessage(msg)
	if msg.Type == protocol.MsgBidTurn {
		select {
		case c.started <- struct{}{}:
		default:
		}
	}
}

func TestRepeatedPracticeRequestsCreateOnlyOneGame(t *testing.T) {
	s := newReconnectServer()
	var count atomic.Int32
	games := make(chan *session.GameSession, 32)
	s.matcher = match.NewMatcher(match.MatcherDeps{
		RoomManager:     s.roomManager,
		GameConfig:      config.GameConfig{BidTimeout: 600, TurnTimeout: 600},
		RegisterSession: func(_ string, gs *session.GameSession) { count.Add(1); games <- gs },
	})
	s.handler = handler.NewHandler(handler.HandlerDeps{Server: s, RoomManager: s.roomManager, Matcher: s.matcher})
	c := &blockedPracticeClient{Client: NewClient(s, nil), creating: make(chan struct{}), release: make(chan struct{}), started: make(chan struct{}, 1)}
	request := codec.MustNewMessage(protocol.MsgPracticeMatch, nil)
	s.handler.Handle(c, request)
	<-c.creating
	// All duplicates arrive before CreateRoom has even assigned the first room.
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { s.handler.Handle(c, request) })
	}
	wg.Wait()
	close(c.release)
	require.Eventually(t, func() bool { return c.GetRoom() != "" }, time.Second, time.Millisecond)
	code := c.GetRoom()
	// Also exercise duplicates during the 100ms room notification delay.
	for range 20 {
		s.handler.Handle(c, request)
	}
	select {
	case <-c.started:
	case <-time.After(2 * time.Second):
		t.Fatal("practice game did not start")
	}
	gs := <-games
	t.Cleanup(func() {
		for _, player := range s.roomManager.GetRoom(code).Players {
			if player.Client.IsBot() {
				player.Client.Close()
			}
		}
		gs.StopAllTimers()
	})
	require.EqualValues(t, 1, count.Load())
	require.Equal(t, code, c.GetRoom())
	require.Equal(t, 3, len(s.roomManager.GetRoom(code).Players))
}
