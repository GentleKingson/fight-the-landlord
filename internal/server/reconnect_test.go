package server

import (
	"sync"
	"testing"

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
