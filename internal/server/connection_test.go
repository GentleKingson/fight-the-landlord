package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	serverhandler "github.com/palemoky/fight-the-landlord/internal/server/handler"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
)

func newWebSocketLimitTestServer(t *testing.T, maxConnections int) (*Server, string) {
	t.Helper()

	s := &Server{
		clients:           make(map[string]*Client),
		sessionManager:    session.NewSessionManager(),
		rateLimiter:       NewRateLimiter(100_000, 100_000, time.Second),
		originChecker:     NewOriginChecker([]string{"*"}),
		messageLimiter:    NewMessageRateLimiter(100_000),
		ipFilter:          NewIPFilter(),
		maxConnections:    maxConnections,
		connectionLimiter: newConnectionLimiter(maxConnections),
	}
	s.handler = serverhandler.NewHandler(serverhandler.HandlerDeps{
		Server:         s,
		SessionManager: s.sessionManager,
	})

	httpServer := httptest.NewServer(http.HandlerFunc(s.handleWebSocket))
	t.Cleanup(httpServer.Close)
	return s, "ws" + strings.TrimPrefix(httpServer.URL, "http")
}

func dialConnectedWebSocket(t *testing.T, url string) (*websocket.Conn, protocol.ConnectedPayload) {
	t.Helper()

	conn, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil {
		t.Cleanup(func() { _ = response.Body.Close() })
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err)
	message, err := codec.Decode(data)
	require.NoError(t, err)
	t.Cleanup(func() { codec.PutMessage(message) })
	require.Equal(t, protocol.MsgConnected, message.Type)

	payload, err := codec.ParsePayload[protocol.ConnectedPayload](message)
	require.NoError(t, err)
	return conn, *payload
}

func TestHandleWebSocket_LimitTracksActiveConnections(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 3)

	connections := make([]*websocket.Conn, 0, 3)
	for range 3 {
		conn, _ := dialConnectedWebSocket(t, url)
		connections = append(connections, conn)
	}
	require.Equal(t, 3, s.GetOnlineCount())
	require.Equal(t, 3, s.activeConnectionLimiter().activeCount())

	conn, response, err := websocket.DefaultDialer.Dial(url, nil)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	require.NotNil(t, response)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)

	require.NoError(t, connections[0].Close())
	require.Eventually(t, func() bool {
		return s.GetOnlineCount() == 2 && s.activeConnectionLimiter().activeCount() == 2
	}, 2*time.Second, 10*time.Millisecond)

	replacement, _ := dialConnectedWebSocket(t, url)
	require.NotNil(t, replacement)
}

func TestHandleWebSocket_ZeroMeansUnlimited(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 0)

	for range 8 {
		conn, _ := dialConnectedWebSocket(t, url)
		require.NotNil(t, conn)
	}
	require.Equal(t, 8, s.activeConnectionLimiter().activeCount())
}

func TestHandleWebSocket_UpgradeFailureReleasesCapacity(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 1)

	response, err := http.Get(strings.Replace(url, "ws", "http", 1))
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	assert.Zero(t, s.activeConnectionLimiter().activeCount())
	assert.Empty(t, s.activeConnectionLimiter().slots)

	conn, _ := dialConnectedWebSocket(t, url)
	require.NotNil(t, conn)
}

func TestHandleWebSocket_ReconnectReplacementDoesNotLeakCapacity(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 2)

	previousConn, previousIdentity := dialConnectedWebSocket(t, url)
	replacementConn, replacementIdentity := dialConnectedWebSocket(t, url)
	require.Equal(t, 2, s.activeConnectionLimiter().activeCount())

	replacement, ok := s.GetClientByID(replacementIdentity.PlayerID).(*Client)
	require.True(t, ok)

	reconnectMessage := codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		PlayerID: previousIdentity.PlayerID,
		Token:    previousIdentity.ReconnectToken,
	})
	t.Cleanup(func() { codec.PutMessage(reconnectMessage) })
	reconnectData, err := codec.Encode(reconnectMessage)
	require.NoError(t, err)
	require.NoError(t, replacementConn.WriteMessage(websocket.BinaryMessage, reconnectData))

	require.NoError(t, replacementConn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err := replacementConn.ReadMessage()
	require.NoError(t, err)
	reconnectedMessage, err := codec.Decode(data)
	require.NoError(t, err)
	t.Cleanup(func() { codec.PutMessage(reconnectedMessage) })
	require.Equal(t, protocol.MsgReconnected, reconnectedMessage.Type)

	require.Eventually(t, func() bool {
		return s.activeConnectionLimiter().activeCount() == 1
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, replacement, s.GetClientByID(previousIdentity.PlayerID))
	require.Nil(t, s.GetClientByID(replacementIdentity.PlayerID))

	thirdConn, _ := dialConnectedWebSocket(t, url)
	require.NotNil(t, thirdConn)
	require.Equal(t, 2, s.activeConnectionLimiter().activeCount())

	_ = previousConn.Close()
	_ = replacementConn.Close()
}

func TestConnectionLimiter_NonPositiveMeansUnlimited(t *testing.T) {
	for _, maxConnections := range []int{0, -1, -100} {
		t.Run(fmt.Sprintf("max_%d", maxConnections), func(t *testing.T) {
			limiter := newConnectionLimiter(maxConnections)
			assert.Nil(t, limiter.slots)

			leases := make([]*connectionLease, 0, 100)
			for range 100 {
				lease, acquired := limiter.tryAcquire()
				require.True(t, acquired)
				lease.activate()
				leases = append(leases, lease)
			}
			assert.Equal(t, 100, limiter.activeCount())
			for _, lease := range leases {
				lease.release()
				lease.release()
			}
			assert.Zero(t, limiter.activeCount())
		})
	}
}
