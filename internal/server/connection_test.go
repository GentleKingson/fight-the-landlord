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

	httpServer := httptest.NewServer(s.httpHandler(nil))
	t.Cleanup(s.Shutdown)
	t.Cleanup(httpServer.Close)
	return s, "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
}

func writeHelloAndReadResponse(
	t *testing.T,
	conn *websocket.Conn,
	requestID, protocolVersion, clientVersion, clientKind string,
) *protocol.Message {
	t.Helper()
	capabilities := append([]string(nil), protocol.RequiredCapabilities...)
	if clientKind == protocol.ClientKindWeb {
		capabilities = append(capabilities, protocol.CapabilityHTTPOnlySessionTicket)
	}
	return writeHelloAndReadResponseWithCapabilities(
		t, conn, requestID, protocolVersion, clientVersion, clientKind, capabilities,
	)
}

func writeHelloAndReadResponseWithCapabilities(
	t *testing.T,
	conn *websocket.Conn,
	requestID, protocolVersion, clientVersion, clientKind string,
	capabilities []string,
) *protocol.Message {
	t.Helper()
	hello := codec.MustNewMessage(protocol.MsgHello, protocol.HelloPayload{
		ProtocolVersion: protocolVersion,
		ClientVersion:   clientVersion,
		Capabilities:    append([]string(nil), capabilities...),
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
		ClientKind:      protocol.ClientKindTUI,
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
	negotiated, err := codec.ParsePayload[protocol.NegotiatedPayload](message)
	require.NoError(t, err)
	assert.ElementsMatch(t, protocol.RequiredCapabilities, negotiated.Capabilities)
	assert.NotContains(t, negotiated.Capabilities, protocol.CapabilityHTTPOnlySessionTicket)
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

func dialBrowserWebSocket(t *testing.T, url string, cookie *http.Cookie) (*websocket.Conn, protocol.ConnectedPayload, *http.Cookie) {
	t.Helper()
	header := http.Header{"Origin": []string{"https://game.example"}}
	if cookie != nil {
		header.Set("Cookie", cookie.String())
	}
	conn, response, err := websocket.DefaultDialer.Dial(url, header)
	if response != nil {
		t.Cleanup(func() { _ = response.Body.Close() })
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	responseMessage := writeHelloAndReadResponse(
		t, conn, "hello-web", protocol.ProtocolVersion, "dev", protocol.ClientKindWeb,
	)
	require.Equal(t, protocol.MsgNegotiated, responseMessage.Type)
	negotiated, err := codec.ParsePayload[protocol.NegotiatedPayload](responseMessage)
	require.NoError(t, err)
	assert.Contains(t, negotiated.Capabilities, protocol.CapabilityHTTPOnlySessionTicket)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err)
	message, err := codec.Decode(data)
	require.NoError(t, err)
	defer codec.PutMessage(message)
	require.Equal(t, protocol.MsgConnected, message.Type)
	payload, err := codec.ParsePayload[protocol.ConnectedPayload](message)
	require.NoError(t, err)
	var owner *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == webSessionOwnerCookieName {
			owner = cookie
		}
	}
	return conn, *payload, owner
}

func commitBrowserTicket(t *testing.T, websocketURL, ticket string, cookies ...*http.Cookie) *http.Cookie {
	t.Helper()
	baseURL := strings.TrimSuffix(strings.Replace(websocketURL, "ws://", "http://", 1), "/ws")
	request, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/session/commit",
		strings.NewReader(`{"ticket":"`+ticket+`"}`),
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://game.example")
	for _, cookie := range cookies {
		if cookie != nil {
			request.AddCookie(cookie)
		}
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	require.Len(t, response.Cookies(), 2)
	for _, cookie := range response.Cookies() {
		if cookie.Name == webSessionCookieName {
			return cookie
		}
	}
	require.FailNow(t, "session cookie missing")
	return nil
}

func refreshBrowserSession(t *testing.T, websocketURL string, cookie *http.Cookie) {
	t.Helper()
	baseURL := strings.TrimSuffix(strings.Replace(websocketURL, "ws://", "http://", 1), "/ws")
	request, err := http.NewRequest(http.MethodPost, baseURL+"/session/refresh", strings.NewReader(`{}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://game.example")
	request.AddCookie(cookie)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
}

func TestBrowserCookieReconnectRotatesOnlyAfterCommittedResponse(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 4)

	first, connected, owner := dialBrowserWebSocket(t, url, nil)
	assert.Empty(t, connected.ReconnectToken)
	assert.NotEmpty(t, connected.WebSessionTicket)
	assert.False(t, connected.ReconnectAvailable)
	require.NotNil(t, owner)
	firstCookie := commitBrowserTicket(t, url, connected.WebSessionTicket, owner)
	assert.True(t, firstCookie.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, firstCookie.SameSite)
	require.NoError(t, first.Close())
	require.Eventually(t, func() bool {
		return !s.sessionManager.IsOnline(connected.PlayerID)
	}, 2*time.Second, 10*time.Millisecond)

	second, provisional, _ := dialBrowserWebSocket(t, url, firstCookie)
	assert.True(t, provisional.ReconnectAvailable)
	assert.Empty(t, provisional.ReconnectToken)
	reconnect := codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{})
	reconnect.Command = &protocol.CommandMeta{RequestID: "web-reconnect"}
	data, err := codec.Encode(reconnect)
	require.NoError(t, err)
	require.NoError(t, second.WriteMessage(websocket.BinaryMessage, data))

	require.NoError(t, second.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err = second.ReadMessage()
	require.NoError(t, err)
	message, err := codec.Decode(data)
	require.NoError(t, err)
	defer codec.PutMessage(message)
	require.Equal(t, protocol.MsgReconnected, message.Type)
	reconnected, err := codec.ParsePayload[protocol.ReconnectedPayload](message)
	require.NoError(t, err)
	assert.Equal(t, connected.PlayerID, reconnected.PlayerID)
	assert.Empty(t, reconnected.ReconnectToken)
	assert.NotEmpty(t, reconnected.WebSessionTicket)
	header := http.Header{"Origin": []string{"https://game.example"}}
	header.Set("Cookie", firstCookie.String())
	busyConnection, busyResponse, busyErr := websocket.DefaultDialer.Dial(url, header)
	if busyConnection != nil {
		_ = busyConnection.Close()
	}
	require.Error(t, busyErr)
	require.NotNil(t, busyResponse)
	require.NoError(t, busyResponse.Body.Close())
	assert.Equal(t, http.StatusConflict, busyResponse.StatusCode, "an active pending predecessor must not mint a fresh identity")
	rotatedCookie := commitBrowserTicket(t, url, reconnected.WebSessionTicket, firstCookie)
	assert.NotEqual(t, firstCookie.Value, rotatedCookie.Value)
	refreshBrowserSession(t, url, rotatedCookie)

	header = http.Header{"Origin": []string{"https://game.example"}}
	header.Set("Cookie", firstCookie.String())
	staleConnection, response, dialErr := websocket.DefaultDialer.Dial(url, header)
	if staleConnection != nil {
		_ = staleConnection.Close()
	}
	require.Error(t, dialErr)
	require.NotNil(t, response)
	defer response.Body.Close()
	assert.Equal(t, http.StatusConflict, response.StatusCode, "the consumed cookie must not be replayable")
}

func TestBrowserOpeningHandshakeReplacesPreseededOwnerAndBindsInitialTicket(t *testing.T) {
	_, url := newWebSocketLimitTestServer(t, 2)
	preseeded := &http.Cookie{
		Name:  webSessionOwnerCookieName,
		Value: strings.Repeat("a", 64),
		Path:  "/",
	}
	_, connected, issuedOwner := dialBrowserWebSocket(t, url, preseeded)
	require.NotNil(t, issuedOwner)
	assert.True(t, issuedOwner.HttpOnly)
	assert.NotEqual(t, preseeded.Value, issuedOwner.Value)

	baseURL := strings.TrimSuffix(strings.Replace(url, "ws://", "http://", 1), "/ws")
	request, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/session/commit",
		strings.NewReader(`{"ticket":"`+connected.WebSessionTicket+`"}`),
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://game.example")
	request.AddCookie(preseeded)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)

	cookie := commitBrowserTicket(t, url, connected.WebSessionTicket, issuedOwner)
	assert.Len(t, cookie.Value, 64)
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

func TestHandleWebSocketRejectsCachedWebClientWithoutCookieSessionCapability(t *testing.T) {
	s, url := newWebSocketLimitTestServer(t, 1)
	header := http.Header{"Origin": []string{"https://game.example"}}
	conn, handshakeResponse, err := websocket.DefaultDialer.Dial(url, header)
	if handshakeResponse != nil {
		defer handshakeResponse.Body.Close()
	}
	require.NoError(t, err)
	response := writeHelloAndReadResponseWithCapabilities(
		t,
		conn,
		"hello-cached-web",
		protocol.ProtocolVersion,
		"dev",
		protocol.ClientKindWeb,
		protocol.RequiredCapabilities,
	)
	require.Equal(t, protocol.MsgProtocolRejected, response.Type)
	payload, err := codec.ParsePayload[protocol.ProtocolRejectedPayload](response)
	require.NoError(t, err)
	assert.Contains(t, payload.Reason, protocol.CapabilityHTTPOnlySessionTicket)
	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool {
		return s.GetOnlineCount() == 0 && s.activeConnectionLimiter().activeCount() == 0
	}, 2*time.Second, 10*time.Millisecond)
	replacement, _ := dialConnectedWebSocket(t, url)
	require.NotNil(t, replacement, "Web-only capability rejection must not affect TUI clients")
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

func TestValidateClientTransportSeparatesBrowserAndOriginlessClients(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		browserTransport bool
		clientKind       string
		accepted         bool
	}{
		{name: "browser web", browserTransport: true, clientKind: protocol.ClientKindWeb, accepted: true},
		{name: "browser tui", browserTransport: true, clientKind: protocol.ClientKindTUI},
		{name: "browser bot", browserTransport: true, clientKind: protocol.ClientKindBot},
		{name: "originless web", clientKind: protocol.ClientKindWeb},
		{name: "originless tui", clientKind: protocol.ClientKindTUI, accepted: true},
		{name: "originless bot", clientKind: protocol.ClientKindBot, accepted: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			reason := validateClientTransport(testCase.browserTransport, testCase.clientKind)
			if testCase.accepted {
				assert.Empty(t, reason)
				return
			}
			assert.NotEmpty(t, reason)
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
