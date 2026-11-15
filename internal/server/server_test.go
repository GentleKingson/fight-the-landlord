package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

func TestServer_RegisterUnregister_Concurrency(t *testing.T) {
	t.Parallel()

	s := &Server{
		clients: make(map[string]*Client),
	}

	var wg sync.WaitGroup
	count := 100

	// Concurrent Register
	wg.Add(count)
	for i := range count {
		go func(i int) {
			defer wg.Done()
			c := &Client{ID: strconv.Itoa(i)}
			s.RegisterClient(c.ID, c)
		}(i)
	}
	wg.Wait()
	assert.Equal(t, count, s.GetOnlineCount())

	// Concurrent Unregister
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func(i int) {
			defer wg.Done()
			s.UnregisterClient(strconv.Itoa(i))
		}(i)
	}
	wg.Wait()
	assert.Equal(t, 0, s.GetOnlineCount())
}

func TestServer_HandleHealth(t *testing.T) {
	t.Parallel()

	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	assert.Equal(t, http.StatusOK, res.StatusCode)
}

func TestServer_HandleVersion(t *testing.T) {
	t.Parallel()

	s := &Server{config: &config.Config{}}
	s.config.Server.MinClientVersion = "v1.1.0"

	req := httptest.NewRequest(http.MethodGet, "/version", http.NoBody)
	w := httptest.NewRecorder()

	s.handleVersion(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	var body struct {
		ServerVersion    string `json:"server_version"`
		MinClientVersion string `json:"min_client_version"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, "v1.1.0", body.MinClientVersion)
	assert.Equal(t, Version, body.ServerVersion)
}

func TestServer_MaintenanceMode(t *testing.T) {
	t.Parallel()

	s := &Server{}

	assert.False(t, s.IsMaintenanceMode())

	s.EnterMaintenanceMode()
	assert.True(t, s.IsMaintenanceMode())
}

func TestServer_MaxConnectionsLifecycle(t *testing.T) {
	redis := miniredis.RunT(t)
	cfg := config.Default()
	cfg.Redis.Addr = redis.Addr()
	cfg.Server.MaxConnections = 1
	cfg.Security.RateLimit.MaxPerSecond = 1000
	cfg.Security.RateLimit.MaxPerMinute = 10000

	s, err := NewServer(cfg)
	require.NoError(t, err)
	httpServer := httptest.NewServer(s.routes())
	t.Cleanup(func() {
		s.SuspendRecovery()
		httpServer.Close()
		_ = s.redis.Close()
	})

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"

	first, resp1, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp1 != nil {
		_ = resp1.Body.Close()
	}
	require.NoError(t, err)
	receivePayload[protocol.ConnectedPayload](t, first, protocol.MsgConnected)

	second, resp2, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if second != nil {
		_ = second.Close()
	}
	require.Error(t, err)
	require.NotNil(t, resp2)
	_ = resp2.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp2.StatusCode)

	require.NoError(t, first.Close())
	require.Eventually(t, func() bool {
		return len(s.semaphore) == 0
	}, 2*time.Second, 10*time.Millisecond)

	third, resp3, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp3 != nil {
		_ = resp3.Body.Close()
	}
	require.NoError(t, err)
	_ = third.Close()
}

func TestServer_UnlimitedMaxConnections(t *testing.T) {
	redis := miniredis.RunT(t)
	cfg := config.Default()
	cfg.Redis.Addr = redis.Addr()
	cfg.Server.MaxConnections = 0
	cfg.Security.RateLimit.MaxPerSecond = 1000
	cfg.Security.RateLimit.MaxPerMinute = 10000

	s, err := NewServer(cfg)
	require.NoError(t, err)
	assert.Nil(t, s.semaphore)
	httpServer := httptest.NewServer(s.routes())
	t.Cleanup(func() {
		s.SuspendRecovery()
		httpServer.Close()
		_ = s.redis.Close()
	})

	c1, _ := dialIdentity(t, httpServer.URL, nil, false)
	c2, _ := dialIdentity(t, httpServer.URL, nil, false)
	receivePayload[protocol.ConnectedPayload](t, c1, protocol.MsgConnected)
	receivePayload[protocol.ConnectedPayload](t, c2, protocol.MsgConnected)
}

func TestServer_InvalidWebSocketHandshakeDoesNotCreateIdentity(t *testing.T) {
	redis := miniredis.RunT(t)
	_, httpServer := identityServer(t, redis)

	resp, err := http.Get(httpServer.URL + "/ws")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Empty(t, redis.Keys())

	req, err := http.NewRequest(http.MethodGet, httpServer.URL+"/ws", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "not-16-bytes")

	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp2.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)
	assert.Empty(t, redis.Keys())
}
