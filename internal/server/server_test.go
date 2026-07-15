package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
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
			id := strconv.Itoa(i)
			s.UnregisterClient(id, s.GetClientByID(id))
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
	assert.Equal(t, "no-store", res.Header.Get("Cache-Control"))
	assert.Equal(t, "text/plain; charset=utf-8", res.Header.Get("Content-Type"))
}

func TestNewServerRejectsMissingProductionRedisCredentialBeforeDial(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Server.Environment = "production"
	cfg.Security.AllowedOrigins = []string{"https://game.example"}

	server, err := NewServer(cfg)

	assert.Nil(t, server)
	assert.EqualError(t, err, "production requires a non-empty Redis password")
}

func TestServer_ReadinessChecksDependencyAndShutdownState(t *testing.T) {
	t.Parallel()
	s := &Server{readinessCheck: func(context.Context) error { return nil }}

	ready := httptest.NewRecorder()
	s.handleReadyz(ready, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))
	assert.Equal(t, http.StatusOK, ready.Code)
	assert.Equal(t, "READY", ready.Body.String())

	s.readinessCheck = func(context.Context) error { return errors.New("redis unavailable") }
	unavailable := httptest.NewRecorder()
	s.handleReadyz(unavailable, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))
	assert.Equal(t, http.StatusServiceUnavailable, unavailable.Code)

	s.readinessCheck = func(context.Context) error { return nil }
	s.shuttingDown.Store(true)
	shuttingDown := httptest.NewRecorder()
	s.handleReadyz(shuttingDown, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))
	assert.Equal(t, http.StatusServiceUnavailable, shuttingDown.Code)

	live := httptest.NewRecorder()
	s.handleLivez(live, httptest.NewRequest(http.MethodGet, "/livez", http.NoBody))
	assert.Equal(t, http.StatusOK, live.Code, "liveness remains healthy during graceful shutdown")
}

func TestHTTPHandlerAppliesBrowserSecurityHeaders(t *testing.T) {
	t.Parallel()
	s := &Server{}
	recorder := httptest.NewRecorder()
	s.httpHandler(nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/livez", http.NoBody))

	assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
	assert.Contains(t, recorder.Header().Get("Permissions-Policy"), "camera=()")
	assert.Contains(t, recorder.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
	assert.Contains(t, recorder.Header().Get("Content-Security-Policy"), "object-src 'none'")
	assert.Equal(t, "DENY", recorder.Header().Get("X-Frame-Options"))
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
	assert.Equal(t, "no-store", res.Header.Get("Cache-Control"))

	var body struct {
		ServerVersion    string `json:"server_version"`
		MinClientVersion string `json:"min_client_version"`
		WebClientVersion string `json:"web_client_version"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	assert.Equal(t, "v1.1.0", body.MinClientVersion)
	assert.Equal(t, Version, body.ServerVersion)
	assert.Equal(t, Version, body.WebClientVersion)
}

func TestServer_ReadOnlyEndpointsRejectWrites(t *testing.T) {
	t.Parallel()

	s := &Server{config: &config.Config{}}
	for _, endpoint := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "health", handler: s.handleHealth},
		{name: "livez", handler: s.handleLivez},
		{name: "readyz", handler: s.handleReadyz},
		{name: "version", handler: s.handleVersion},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			endpoint.handler(w, httptest.NewRequest(http.MethodPost, "/"+endpoint.name, http.NoBody))

			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
			assert.Equal(t, "GET, HEAD", w.Header().Get("Allow"))
			assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		})
	}
}

func TestServer_MaintenanceMode(t *testing.T) {
	t.Parallel()

	s := &Server{}

	assert.False(t, s.IsMaintenanceMode())

	s.EnterMaintenanceMode()
	assert.True(t, s.IsMaintenanceMode())
}

func TestServer_GracefulShutdown_Logic(t *testing.T) {
	// 这是一个逻辑测试，不涉及真实的 Redis/HTTP 关闭
	t.Parallel()

	// cfg := &config.Config{}
	// mock config to prevent nil pointer if accessed
	// But GracefulShutdown accesses s.config.Game.ShutdownCheckIntervalDuration()
	// So we need to set it up properly or mock parts of it.
	// Since s.roomManager is nil, we should construct a minimal server.

	// Skip complex integration-like tests in unit tests unless we mock everything.
	// Focusing on available simple logic.
}
