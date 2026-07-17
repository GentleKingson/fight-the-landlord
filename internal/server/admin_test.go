package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

const testAdminKey = "0123456789abcdef0123456789abcdef"

func newAdminTestServer(now *time.Time) *Server {
	cfg := config.Default()
	cfg.Admin.Key = testAdminKey
	store := newModerationStore()
	if now != nil {
		store.now = func() time.Time { return *now }
	}
	return &Server{
		config:     cfg,
		clients:    make(map[string]*Client),
		moderation: store,
		logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
}

func performAdminRequest(
	t *testing.T,
	server *Server,
	method, path, body, key, remoteAddr string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = remoteAddr
	if key != "" {
		request.Header.Set(adminKeyHeader, key)
	}
	recorder := httptest.NewRecorder()
	server.httpHandler(nil).ServeHTTP(recorder, request)
	return recorder
}

func TestAdminEndpointsRequireLoopbackAndIndependentKey(t *testing.T) {
	t.Parallel()
	server := newAdminTestServer(nil)

	remote := performAdminRequest(t, server, http.MethodGet, "/admin/status", "", testAdminKey, "203.0.113.5:41000")
	assert.Equal(t, http.StatusForbidden, remote.Code)
	assert.Equal(t, "no-store", remote.Header().Get("Cache-Control"))

	spoofedRequest := httptest.NewRequest(http.MethodGet, "/admin/status", http.NoBody)
	spoofedRequest.RemoteAddr = "203.0.113.5:41000"
	spoofedRequest.Header.Set("X-Forwarded-For", "127.0.0.1")
	spoofedRequest.Header.Set(adminKeyHeader, testAdminKey)
	spoofed := httptest.NewRecorder()
	server.httpHandler(nil).ServeHTTP(spoofed, spoofedRequest)
	assert.Equal(t, http.StatusForbidden, spoofed.Code)

	proxiedLoopback := httptest.NewRequest(http.MethodGet, "/admin/status", http.NoBody)
	proxiedLoopback.RemoteAddr = "127.0.0.1:41000"
	proxiedLoopback.Header.Set("Forwarded", "for=203.0.113.5")
	proxiedLoopback.Header.Set(adminKeyHeader, testAdminKey)
	proxied := httptest.NewRecorder()
	server.httpHandler(nil).ServeHTTP(proxied, proxiedLoopback)
	assert.Equal(t, http.StatusForbidden, proxied.Code)

	wrongKey := performAdminRequest(t, server, http.MethodGet, "/admin/status", "", "wrong-key", "127.0.0.1:41000")
	assert.Equal(t, http.StatusUnauthorized, wrongKey.Code)

	missingKey := performAdminRequest(t, server, http.MethodGet, "/admin/status", "", "", "[::1]:41000")
	assert.Equal(t, http.StatusUnauthorized, missingKey.Code)

	allowed := performAdminRequest(t, server, http.MethodGet, "/admin/status", "", testAdminKey, "[::1]:41000")
	assert.Equal(t, http.StatusOK, allowed.Code)
	assert.Equal(t, "no-store", allowed.Header().Get("Cache-Control"))
}

func TestAdminMutationsArePOSTOnlyAndBodiesAreBounded(t *testing.T) {
	t.Parallel()
	server := newAdminTestServer(nil)

	mutationGet := performAdminRequest(t, server, http.MethodGet, "/admin/drain", "", testAdminKey, "127.0.0.1:41000")
	assert.Equal(t, http.StatusMethodNotAllowed, mutationGet.Code)
	assert.Equal(t, http.MethodPost, mutationGet.Header().Get("Allow"))

	statusPost := performAdminRequest(t, server, http.MethodPost, "/admin/status", "{}", testAdminKey, "127.0.0.1:41000")
	assert.Equal(t, http.StatusMethodNotAllowed, statusPost.Code)
	assert.Equal(t, http.MethodGet, statusPost.Header().Get("Allow"))

	oversized := performAdminRequest(
		t, server, http.MethodPost, "/admin/mute",
		`{"player_id":"p1","duration_seconds":60,"padding":"`+strings.Repeat("x", maxAdminBodyBytes)+`"}`,
		testAdminKey, "127.0.0.1:41000",
	)
	assert.Equal(t, http.StatusRequestEntityTooLarge, oversized.Code)
	assert.Equal(t, "no-store", oversized.Header().Get("Cache-Control"))
}

func TestAdminStateTransitionsAreIdempotentAndRaceSafe(t *testing.T) {
	server := newAdminTestServer(nil)

	first := performAdminRequest(t, server, http.MethodPost, "/admin/drain", "{}", testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusOK, first.Code)
	var firstStatus adminStatusResponse
	require.NoError(t, json.NewDecoder(first.Body).Decode(&firstStatus))
	assert.Equal(t, OperationalStateDraining, firstStatus.State)
	assert.True(t, firstStatus.SafeToRestart)

	second := performAdminRequest(t, server, http.MethodPost, "/admin/drain", "", testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, OperationalStateDraining, server.OperationalState())

	var wait sync.WaitGroup
	for index := range 200 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			switch index % 3 {
			case 0:
				server.EnterDrainingMode()
			case 1:
				server.setOperationalState(operationalMaintenance)
			default:
				server.ResumeNormalOperation()
			}
		}()
	}
	wait.Wait()
	assert.Contains(t, []string{
		OperationalStateNormal,
		OperationalStateDraining,
		OperationalStateMaintenance,
	}, server.OperationalState())
}

func TestAdminStatusWaitsForStartLeaseThroughDrain(t *testing.T) {
	server := newAdminTestServer(nil)
	release, admitted := server.AcquireGameStartLease()
	require.True(t, admitted)
	require.True(t, server.EnterDrainingMode())

	during := performAdminRequest(t, server, http.MethodGet, "/admin/status", "", testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusOK, during.Code)
	var duringStatus adminStatusResponse
	require.NoError(t, json.NewDecoder(during.Body).Decode(&duringStatus))
	require.False(t, duringStatus.SafeToRestart)

	release()
	release()
	after := performAdminRequest(t, server, http.MethodGet, "/admin/status", "", testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusOK, after.Code)
	var afterStatus adminStatusResponse
	require.NoError(t, json.NewDecoder(after.Body).Decode(&afterStatus))
	require.True(t, afterStatus.SafeToRestart)

	_, admitted = server.AcquireGameStartLease()
	require.False(t, admitted, "draining must reject every later authoritative start")
}

func TestDrainWaitsForShortAdmissionThenClosesBoundary(t *testing.T) {
	server := newAdminTestServer(nil)
	release, _, admitted := server.AcquireOperationalAdmission(false)
	require.True(t, admitted)
	drainStarted := make(chan struct{})
	drainDone := make(chan struct{})
	go func() {
		close(drainStarted)
		server.EnterDrainingMode()
		close(drainDone)
	}()
	<-drainStarted
	require.Eventually(t, func() bool {
		if server.operationalTransitionMu.TryLock() {
			server.operationalTransitionMu.Unlock()
			return false
		}
		return true
	}, time.Second, time.Millisecond, "drain never reached the short-admission boundary")
	select {
	case <-drainDone:
		t.Fatal("drain crossed an admitted create or enqueue mutation")
	default:
	}
	release()
	select {
	case <-drainDone:
	case <-time.After(time.Second):
		t.Fatal("drain did not finish after the admitted mutation released")
	}
	_, state, admitted := server.AcquireOperationalAdmission(false)
	require.False(t, admitted, "a short admission landed after drain completed")
	require.Equal(t, OperationalStateDraining, state)
}

func TestOperationalStateNoticesReachActiveGamesWithoutBlockingDrainActions(t *testing.T) {
	t.Parallel()
	server := newAdminTestServer(nil)
	client := NewClient(server, nil)
	client.SetRoom("active-room")
	server.clients[client.GetID()] = client

	require.True(t, server.EnterDrainingMode())
	draining := drainClientMessages(t, client)
	require.Len(t, draining, 2)
	assert.Equal(t, protocol.MsgMaintenancePush, draining[0].Type)
	drainingStatus, err := codec.ParsePayload[protocol.MaintenancePayload](draining[0])
	require.NoError(t, err)
	assert.False(t, drainingStatus.Maintenance)
	assert.Equal(t, protocol.MsgError, draining[1].Type)
	drainNotice, err := codec.ParsePayload[protocol.ErrorPayload](draining[1])
	require.NoError(t, err)
	assert.Equal(t, protocol.ErrCodeServerDraining, drainNotice.Code)
	assert.Contains(t, drainNotice.Message, "排空")

	require.True(t, server.setOperationalState(operationalMaintenance))
	maintenance := drainClientMessages(t, client)
	require.Len(t, maintenance, 2)
	maintenanceStatus, err := codec.ParsePayload[protocol.MaintenancePayload](maintenance[0])
	require.NoError(t, err)
	assert.True(t, maintenanceStatus.Maintenance)

	require.True(t, server.ResumeNormalOperation())
	resumed := drainClientMessages(t, client)
	require.Len(t, resumed, 1)
	resumedStatus, err := codec.ParsePayload[protocol.MaintenancePayload](resumed[0])
	require.NoError(t, err)
	assert.False(t, resumedStatus.Maintenance)
}

func TestAdminMuteBanExpiryRemovalAndIdempotence(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	server := newAdminTestServer(&now)

	muteBody := `{"player_id":"player-1","duration_seconds":30}`
	firstMute := performAdminRequest(t, server, http.MethodPost, "/admin/mute", muteBody, testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusOK, firstMute.Code)
	var firstMuteResult map[string]string
	require.NoError(t, json.NewDecoder(firstMute.Body).Decode(&firstMuteResult))
	assert.True(t, server.IsPlayerMuted("player-1"))

	now = now.Add(time.Second)
	secondMute := performAdminRequest(t, server, http.MethodPost, "/admin/mute", muteBody, testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusOK, secondMute.Code)
	var secondMuteResult map[string]string
	require.NoError(t, json.NewDecoder(secondMute.Body).Decode(&secondMuteResult))
	assert.Equal(t, firstMuteResult["expires_at"], secondMuteResult["expires_at"], "a retry must not extend the mute")

	unmute := performAdminRequest(t, server, http.MethodPost, "/admin/unmute", `{"player_id":"player-1"}`, testAdminKey, "127.0.0.1:41000")
	assert.Equal(t, http.StatusNoContent, unmute.Code)
	assert.False(t, server.IsPlayerMuted("player-1"))
	repeatedUnmute := performAdminRequest(t, server, http.MethodPost, "/admin/unmute", `{"player_id":"player-1"}`, testAdminKey, "127.0.0.1:41000")
	assert.Equal(t, http.StatusNoContent, repeatedUnmute.Code)

	client := &Client{ID: "player-1", server: server}
	server.RegisterClient(client.ID, client)
	ban := performAdminRequest(t, server, http.MethodPost, "/admin/ban", `{"player_id":"player-1","duration_seconds":10}`, testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusOK, ban.Code)
	assert.True(t, client.IsClosed())
	assert.True(t, server.IsPlayerBanned("player-1"))

	now = now.Add(10 * time.Second)
	assert.False(t, server.IsPlayerBanned("player-1"))
	unban := performAdminRequest(t, server, http.MethodPost, "/admin/unban", `{"player_id":"player-1"}`, testAdminKey, "127.0.0.1:41000")
	assert.Equal(t, http.StatusNoContent, unban.Code)
}

func TestAdminDisconnectAndRateLimitAreIdempotent(t *testing.T) {
	t.Parallel()
	server := newAdminTestServer(nil)
	client := &Client{ID: "player-1", server: server}
	server.RegisterClient(client.ID, client)

	first := performAdminRequest(t, server, http.MethodPost, "/admin/disconnect", `{"player_id":"player-1"}`, testAdminKey, "127.0.0.1:41000")
	assert.Equal(t, http.StatusNoContent, first.Code)
	assert.True(t, client.IsClosed())
	second := performAdminRequest(t, server, http.MethodPost, "/admin/disconnect", `{"player_id":"player-1"}`, testAdminKey, "127.0.0.1:41000")
	assert.Equal(t, http.StatusNoContent, second.Code)

	limited := newAdminTestServer(nil)
	limited.adminLimiter = newAdminRateLimiter(1, time.Minute)
	allowed := performAdminRequest(t, limited, http.MethodGet, "/admin/status", "", testAdminKey, "127.0.0.1:41000")
	assert.Equal(t, http.StatusOK, allowed.Code)
	rejected := performAdminRequest(t, limited, http.MethodGet, "/admin/status", "", testAdminKey, "127.0.0.1:41000")
	assert.Equal(t, http.StatusTooManyRequests, rejected.Code)
}

func TestAdminAuditLogExcludesManagementKeys(t *testing.T) {
	t.Parallel()
	server := newAdminTestServer(nil)
	var output bytes.Buffer
	server.logger = slog.New(slog.NewJSONHandler(&output, nil))

	response := performAdminRequest(t, server, http.MethodPost, "/admin/mute", `{"player_id":"player-1","duration_seconds":30}`, testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusOK, response.Code)
	assert.NotContains(t, output.String(), testAdminKey)
	assert.Contains(t, output.String(), `"event":"admin_action"`)

	output.Reset()
	status := performAdminRequest(t, server, http.MethodGet, "/admin/status", "", testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusOK, status.Code)
	assert.Contains(t, output.String(), `"action":"status"`)
	assert.Contains(t, output.String(), `"result":"success"`)

	output.Reset()
	wrongMethod := performAdminRequest(t, server, http.MethodGet, "/admin/drain", "", testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusMethodNotAllowed, wrongMethod.Code)
	assert.Contains(t, output.String(), `"reason":"wrong_method"`)

	output.Reset()
	invalidBody := performAdminRequest(t, server, http.MethodPost, "/admin/mute", `{`, testAdminKey, "127.0.0.1:41000")
	require.Equal(t, http.StatusBadRequest, invalidBody.Code)
	assert.Contains(t, output.String(), `"reason":"invalid_body"`)
	assert.NotContains(t, output.String(), testAdminKey)
}
