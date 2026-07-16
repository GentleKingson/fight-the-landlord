package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/server/session"
)

func TestSPAHandlerServesIndexAndFallback(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	for _, requestPath := range []string{"/", "/room/836219", "/game/table/"} {
		t.Run(requestPath, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, requestPath, http.NoBody)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			res := w.Result()
			defer func() { _ = res.Body.Close() }()
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, res.StatusCode)
			assert.Contains(t, string(body), `<div id="root"></div>`)
			assert.Equal(t, indexCacheControl, res.Header.Get("Cache-Control"))
			assert.Equal(t, "v1.2.3", res.Header.Get("X-Web-Client-Version"))
			assert.NotEmpty(t, res.Header.Get("ETag"))
		})
	}
}

func TestSPAHandlerCachesHashedAssets(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/app-a1b2c3.js", http.NoBody)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "console.log('ready')", string(body))
	assert.Equal(t, assetCacheControl, res.Header.Get("Cache-Control"))
	assert.Contains(t, res.Header.Get("Content-Type"), "javascript")
}

func TestSPAHandlerDoesNotFallbackForMissingFiles(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	for _, requestPath := range []string{"/assets/missing.js", "/favicon.ico", "/../secret.txt"} {
		t.Run(requestPath, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, requestPath, http.NoBody)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	}
}

func TestSPAHandlerHonorsIndexETag(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("If-None-Match", first.Header().Get("ETag"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotModified, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestSPAHandlerRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body")))

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, "GET, HEAD", w.Header().Get("Allow"))
}

func TestSPAHandlerRequiresIndex(t *testing.T) {
	t.Parallel()

	_, err := newSPAHandler(fstest.MapFS{}, "dev")
	require.Error(t, err)
}

func TestSessionRevokeRequiresExactCredentialAndAllowedOrigin(t *testing.T) {
	t.Parallel()
	manager := session.NewSessionManager()
	playerSession := manager.MustCreateSession("player-1", "Player One")
	server := &Server{
		sessionManager: manager,
		originChecker:  NewOriginChecker([]string{"https://game.example"}),
	}

	badOrigin := httptest.NewRequest(http.MethodPost, "/session/revoke", strings.NewReader(
		`{"player_id":"player-1","token":"`+playerSession.ReconnectToken+`"}`,
	))
	badOrigin.Header.Set("Content-Type", "application/json")
	badOrigin.Header.Set("Origin", "https://evil.example")
	badOriginRecorder := httptest.NewRecorder()
	server.handleSessionRevoke(badOriginRecorder, badOrigin)
	assert.Equal(t, http.StatusForbidden, badOriginRecorder.Code)
	assert.True(t, manager.CanReconnect(playerSession.ReconnectToken, "player-1"))

	request := httptest.NewRequest(http.MethodPost, "/session/revoke", strings.NewReader(
		`{"player_id":"player-1","token":"`+playerSession.ReconnectToken+`"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://game.example")
	recorder := httptest.NewRecorder()
	server.handleSessionRevoke(recorder, request)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	assert.False(t, manager.CanReconnect(playerSession.ReconnectToken, "player-1"))

	replayed := httptest.NewRequest(http.MethodPost, "/session/revoke", strings.NewReader(
		`{"player_id":"player-1","token":"`+playerSession.ReconnectToken+`"}`,
	))
	replayed.Header.Set("Content-Type", "application/json")
	replayed.Header.Set("Origin", "https://game.example")
	replayedRecorder := httptest.NewRecorder()
	server.handleSessionRevoke(replayedRecorder, replayed)
	assert.Equal(t, http.StatusUnauthorized, replayedRecorder.Code)
}

func TestSessionRevokeRejectsCrossSiteFormAndUnsupportedMethod(t *testing.T) {
	t.Parallel()
	server := &Server{
		sessionManager: session.NewSessionManager(),
		originChecker:  NewOriginChecker([]string{"https://game.example"}),
	}

	form := httptest.NewRequest(http.MethodPost, "/session/revoke", strings.NewReader("player_id=p1&token=secret"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	form.Header.Set("Origin", "https://game.example")
	formRecorder := httptest.NewRecorder()
	server.handleSessionRevoke(formRecorder, form)
	assert.Equal(t, http.StatusUnsupportedMediaType, formRecorder.Code)

	getRecorder := httptest.NewRecorder()
	server.handleSessionRevoke(getRecorder, httptest.NewRequest(http.MethodGet, "/session/revoke", http.NoBody))
	assert.Equal(t, http.StatusMethodNotAllowed, getRecorder.Code)
	assert.Equal(t, http.MethodPost, getRecorder.Header().Get("Allow"))
}

func newTestSPAHandler(t *testing.T) http.Handler {
	t.Helper()
	assets := fstest.MapFS{
		"index.html":           {Data: []byte(`<html><body><div id="root"></div></body></html>`)},
		"assets/app-a1b2c3.js": {Data: []byte("console.log('ready')")},
	}
	handler, err := newSPAHandler(assets, "v1.2.3")
	require.NoError(t, err)
	return handler
}
