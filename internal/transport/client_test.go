package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

var upgrader = websocket.Upgrader{}

func echoHandler(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = c.Close() }()
	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			break
		}
		// simple echo
		_ = c.WriteMessage(mt, message)
	}
}

func TestClient_ConnectAndSend(t *testing.T) {
	// Start a mock WS server that echoes messages
	s := httptest.NewServer(http.HandlerFunc(echoHandler))
	defer s.Close()

	// Convert http URL to ws URL
	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")

	client := NewClient(wsURL)
	assert.NotNil(t, client)

	// Connect
	err := client.Connect()
	assert.NoError(t, err)
	defer client.Close()

	// Wait for connection to establish
	time.Sleep(100 * time.Millisecond)
	assert.True(t, client.IsConnected())

	// Send a message
	msg := codec.MustNewMessage(protocol.MsgPing, protocol.PingPayload{Timestamp: 123456})
	err = client.SendMessage(msg)
	assert.NoError(t, err)

	// Receive echo (blocks until message)
	// Since we are using an echo server, we expect to get the binary message back.
	// But client.readPump logic tries to Decode it.
	// The echo server sends back exactly what we sent (BinaryMessage).
	// So protocol.Decode should work if it's a valid message.

	receivedMsg, err := client.ReceiveWithTimeout(1 * time.Second)
	assert.NoError(t, err)
	assert.NotNil(t, receivedMsg)
	assert.Equal(t, protocol.MsgPing, receivedMsg.Type)
}

func TestReconnectKeepsOriginalCredentialUntilValidated(t *testing.T) {
	c := NewClient("")
	c.PlayerID, c.ReconnectToken = "original", "original-token"
	c.reconnecting.Store(true)
	c.processMessage(codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{PlayerID: "temporary", ReconnectToken: "temporary-token"}))
	assert.Equal(t, "original", c.PlayerID)
	assert.Equal(t, "original-token", c.ReconnectToken)
	assert.Empty(t, c.receive)
	c.processMessage(codec.MustNewMessage(protocol.MsgReconnected, protocol.ReconnectedPayload{PlayerID: "original"}))
	assert.False(t, c.IsReconnecting())
	assert.Equal(t, "original-token", c.ReconnectToken)
	c.handleReadError(&websocket.CloseError{Code: 4001})
	assert.True(t, c.replaced.Load())
}

func TestIdentityOutageDoesNotFallBackToTemporaryTerminalIdentity(t *testing.T) {
	c := NewClient("")
	c.PlayerID, c.ReconnectToken = "original", "original-token"
	c.reconnecting.Store(true)
	c.processMessage(codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{PlayerID: "temporary", ReconnectToken: "temporary-token"}))
	c.processMessage(codec.NewErrorMessage(protocol.ErrCodeIdentityUnavailable))
	assert.Equal(t, "original", c.PlayerID)
	assert.Equal(t, "original-token", c.ReconnectToken)
	assert.False(t, c.IsReconnecting())
}

func TestClient_ReconnectPreservesReceiveChannelAcrossGenerations(t *testing.T) {
	connSeq := make(chan int, 4)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()

		seq := len(connSeq) + 1
		connSeq <- seq
		if seq == 1 {
			_ = ws.WriteMessage(websocket.BinaryMessage, mustEncode(t, codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
				PlayerID:       "p1",
				PlayerName:     "Alice",
				ReconnectToken: "tok-1",
			})))
			// Drop generation 1 after client receives MsgConnected
			time.Sleep(50 * time.Millisecond)
			_ = ws.Close()
			return
		}

		// Generation 2: send temporary Connected, expect MsgReconnect, reply MsgReconnected + MsgOnlineCount
		_ = ws.WriteMessage(websocket.BinaryMessage, mustEncode(t, codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
			PlayerID:       "temp",
			PlayerName:     "Temp",
			ReconnectToken: "temp-tok",
		})))
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		req, err := codec.Decode(raw)
		if err != nil || req.Type != protocol.MsgReconnect {
			return
		}
		_ = ws.WriteMessage(websocket.BinaryMessage, mustEncode(t, codec.MustNewMessage(protocol.MsgReconnected, protocol.ReconnectedPayload{
			PlayerID:   "p1",
			PlayerName: "Alice",
		})))
		_ = ws.WriteMessage(websocket.BinaryMessage, mustEncode(t, codec.MustNewMessage(protocol.MsgOnlineCount, protocol.OnlineCountPayload{
			Count: 7,
		})))
		time.Sleep(100 * time.Millisecond)
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	client := NewClient(wsURL)
	client.reconnectBackoff = 20 * time.Millisecond
	defer client.Close()

	assert.NoError(t, client.Connect())

	// Single reader loop started before disconnect must receive Connected, Reconnected, and OnlineCount
	first, err := client.ReceiveWithTimeout(2 * time.Second)
	assert.NoError(t, err)
	assert.Equal(t, protocol.MsgConnected, first.Type)

	reconnected, err := client.ReceiveWithTimeout(2 * time.Second)
	assert.NoError(t, err)
	assert.Equal(t, protocol.MsgReconnected, reconnected.Type)

	afterReconnect, err := client.ReceiveWithTimeout(2 * time.Second)
	assert.NoError(t, err)
	assert.Equal(t, protocol.MsgOnlineCount, afterReconnect.Type)
}

func TestClient_CloseCancelsReconnectBackoff(t *testing.T) {
	dialCount := make(chan struct{}, 8)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialCount <- struct{}{}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = ws.WriteMessage(websocket.BinaryMessage, mustEncode(t, codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
			PlayerID:       "p1",
			PlayerName:     "Alice",
			ReconnectToken: "tok-1",
		})))
		time.Sleep(30 * time.Millisecond)
		_ = ws.Close()
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	client := NewClient(wsURL)
	client.reconnectBackoff = 5 * time.Second // Long backoff so Close() hits during sleep

	reconnectingNotified := make(chan struct{}, 1)
	client.OnReconnecting = func(int, int) {
		select {
		case reconnectingNotified <- struct{}{}:
		default:
		}
	}

	assert.NoError(t, client.Connect())
	_, err := client.ReceiveWithTimeout(time.Second)
	assert.NoError(t, err)

	<-reconnectingNotified
	start := time.Now()
	client.Close()
	_, recvErr := client.ReceiveWithTimeout(200 * time.Millisecond)
	assert.Error(t, recvErr)
	assert.Less(t, time.Since(start), 500*time.Millisecond)
	assert.Eventually(t, func() bool { return !client.IsReconnecting() }, 500*time.Millisecond, 10*time.Millisecond)
	assert.Len(t, dialCount, 1)
}

func mustEncode(t *testing.T, msg *protocol.Message) []byte {
	t.Helper()
	b, err := codec.Encode(msg)
	assert.NoError(t, err)
	return b
}
