package server

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	serverhandler "github.com/palemoky/fight-the-landlord/internal/server/handler"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
)

func newWebSocketLimitTestServer(t *testing.T, maxConnections int) (server *Server, websocketURL string) {
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
	t.Cleanup(s.Shutdown)
	t.Cleanup(httpServer.Close)
	return s, "ws" + strings.TrimPrefix(httpServer.URL, "http")
}

func writeHelloAndReadResponse(
	t *testing.T,
	conn *websocket.Conn,
	requestID, protocolVersion, clientVersion, clientKind string,
) *protocol.Message {
	t.Helper()
	hello := codec.MustNewMessage(protocol.MsgHello, protocol.HelloPayload{
		ProtocolVersion: protocolVersion,
		ClientVersion:   clientVersion,
		Capabilities:    append([]string(nil), protocol.RequiredCapabilities...),
		ClientKind:      clientKind,
	})
	hello.Command = &protocol.CommandMeta{RequestID: requestID}
	data, err := codec.Encode(hello)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, data))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err = conn.ReadMessage()
	require.NoError(t, err)
	response, err := codec.Decode(data)
	require.NoError(t, err)
	t.Cleanup(func() { codec.PutMessage(response) })
	return response
}

func dialConnectedWebSocket(t *testing.T, url string) (*websocket.Conn, protocol.ConnectedPayload) {
	t.Helper()

	conn, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil {
		t.Cleanup(func() { _ = response.Body.Close() })
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	hello := codec.MustNewMessage(protocol.MsgHello, protocol.HelloPayload{
		ProtocolVersion: protocol.ProtocolVersion,
		ClientVersion:   "dev",
		Capabilities:    append([]string(nil), protocol.RequiredCapabilities...),
		ClientKind:      protocol.ClientKindWeb,
	})
	hello.Command = &protocol.CommandMeta{RequestID: "hello-test"}
	helloData, err := codec.Encode(hello)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, helloData))

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err)
	message, err := codec.Decode(data)
	require.NoError(t, err)
	require.Equal(t, protocol.MsgNegotiated, message.Type)
	codec.PutMessage(message)

	_, data, err = conn.ReadMessage()
	require.NoError(t, err)
	message, err = codec.Decode(data)
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

func TestHandleWebSocketRejectsIncompatibleProtocolBeforeRegistration(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 1)
	conn, handshakeResponse, err := websocket.DefaultDialer.Dial(url, nil)
	if handshakeResponse != nil {
		defer handshakeResponse.Body.Close()
	}
	require.NoError(t, err)
	response := writeHelloAndReadResponse(t, conn, "hello-incompatible", "0", "dev", protocol.ClientKindWeb)
	require.Equal(t, protocol.MsgProtocolRejected, response.Type)
	payload, err := codec.ParsePayload[protocol.ProtocolRejectedPayload](response)
	require.NoError(t, err)
	assert.Equal(t, "hello-incompatible", payload.RequestID)
	assert.Equal(t, protocol.ProtocolVersion, payload.SupportedProtocolVersion)
	assert.Equal(t, "hello-incompatible", response.Command.RequestID)
	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool {
		return s.GetOnlineCount() == 0 && s.activeConnectionLimiter().activeCount() == 0
	}, 2*time.Second, 10*time.Millisecond)
	replacement, _ := dialConnectedWebSocket(t, url)
	require.NotNil(t, replacement, "a rejected handshake must release connection capacity")
}

func TestHandleWebSocketRejectsClientBelowMinimumVersion(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 1)
	s.config = &config.Config{Server: config.ServerConfig{MinClientVersion: "v2.0.0"}}
	conn, handshakeResponse, err := websocket.DefaultDialer.Dial(url, nil)
	if handshakeResponse != nil {
		defer handshakeResponse.Body.Close()
	}
	require.NoError(t, err)
	response := writeHelloAndReadResponse(t, conn, "hello-old-client", protocol.ProtocolVersion, "v1.9.9", protocol.ClientKindTUI)
	require.Equal(t, protocol.MsgProtocolRejected, response.Type)
	payload, err := codec.ParsePayload[protocol.ProtocolRejectedPayload](response)
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", payload.MinClientVersion)
	assert.Contains(t, payload.Reason, "版本过低")
	_ = conn.Close()
}

func TestHandleWebSocketComparesMinimumVersionUsingSemverPrecedence(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 1)
	s.config = &config.Config{Server: config.ServerConfig{MinClientVersion: "v1.2.3+server.7"}}
	conn, handshakeResponse, err := websocket.DefaultDialer.Dial(url, nil)
	if handshakeResponse != nil {
		defer handshakeResponse.Body.Close()
	}
	require.NoError(t, err)
	response := writeHelloAndReadResponse(
		t,
		conn,
		"hello-build-metadata",
		protocol.ProtocolVersion,
		"v1.2.2",
		protocol.ClientKindWeb,
	)
	require.Equal(t, protocol.MsgProtocolRejected, response.Type)
	payload, err := codec.ParsePayload[protocol.ProtocolRejectedPayload](response)
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3+server.7", payload.MinClientVersion)
	_ = conn.Close()
}

func TestHandleWebSocketRequiresHelloAsFirstMessage(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 1)
	conn, handshakeResponse, err := websocket.DefaultDialer.Dial(url, nil)
	if handshakeResponse != nil {
		defer handshakeResponse.Body.Close()
	}
	require.NoError(t, err)
	ping := commandTestMessage("not-a-hello", 1)
	data, err := codec.Encode(ping)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, data))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err = conn.ReadMessage()
	require.NoError(t, err)
	response, err := codec.Decode(data)
	require.NoError(t, err)
	defer codec.PutMessage(response)
	require.Equal(t, protocol.MsgProtocolRejected, response.Type)
	assert.Zero(t, s.GetOnlineCount())
	_ = conn.Close()
}

func TestHandleWebSocket_UpgradeFailureReleasesCapacity(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 1)

	response, err := http.Get(strings.Replace(url, "ws", "http", 1))
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	assert.Zero(t, s.activeConnectionLimiter().activeCount())
	assert.Zero(t, s.activeConnectionLimiter().reserved.Load())

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
	reconnectMessage.Command = &protocol.CommandMeta{RequestID: "reconnect-test"}
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
			assert.Zero(t, limiter.limit)

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

func TestConnectionLimiterLargeLimitUsesConstantSpaceAndContendedLeasesAreExactOnce(t *testing.T) {
	t.Parallel()

	large := newConnectionLimiter(math.MaxInt)
	require.EqualValues(t, math.MaxInt, large.limit)
	require.Zero(t, large.reserved.Load())
	lease, acquired := large.tryAcquire()
	require.True(t, acquired)
	require.EqualValues(t, 1, large.reserved.Load())
	lease.release()
	require.Zero(t, large.reserved.Load())

	const limit = 32
	limiter := newConnectionLimiter(limit)
	start := make(chan struct{})
	leases := make(chan *connectionLease, limit*8)
	var attempts sync.WaitGroup
	for range limit * 8 {
		attempts.Add(1)
		go func() {
			defer attempts.Done()
			<-start
			if lease, ok := limiter.tryAcquire(); ok {
				leases <- lease
			}
		}()
	}
	close(start)
	attempts.Wait()
	close(leases)

	acquiredLeases := make([]*connectionLease, 0, limit)
	for lease := range leases {
		lease.activate()
		acquiredLeases = append(acquiredLeases, lease)
	}
	require.Len(t, acquiredLeases, limit)
	require.EqualValues(t, limit, limiter.reserved.Load())
	require.Equal(t, limit, limiter.activeCount())

	var releases sync.WaitGroup
	for _, lease := range acquiredLeases {
		releases.Add(1)
		go func(lease *connectionLease) {
			defer releases.Done()
			lease.release()
			lease.release()
		}(lease)
	}
	releases.Wait()
	require.Zero(t, limiter.reserved.Load())
	require.Zero(t, limiter.activeCount())
}
