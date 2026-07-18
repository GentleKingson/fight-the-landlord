package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAdminCommand(t *testing.T) {
	t.Parallel()

	method, payload, err := buildAdminCommand("status", "", 0)
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, method)
	assert.Nil(t, payload)

	method, payload, err = buildAdminCommand("ban", "player-1", 30*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, adminCommandPayload{PlayerID: "player-1", DurationSecond: 1800}, payload)

	_, _, err = buildAdminCommand("mute", "player-1", 500*time.Millisecond)
	require.ErrorContains(t, err, "whole number of seconds")
	_, _, err = buildAdminCommand("disconnect", "", 0)
	require.ErrorContains(t, err, "admin-player")
	_, _, err = buildAdminCommand("unknown", "", 0)
	require.ErrorContains(t, err, "unsupported")
}

func TestExecuteAdminCommandUsesLoopbackKeyAndBoundedPayload(t *testing.T) {
	t.Parallel()
	const key = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		host, _, err := net.SplitHostPort(request.RemoteAddr)
		require.NoError(t, err)
		assert.True(t, net.ParseIP(host).IsLoopback())
		assert.Equal(t, key, request.Header.Get("X-Admin-Key"))
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/admin/mute", request.URL.Path)
		var payload adminCommandPayload
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		assert.Equal(t, adminCommandPayload{PlayerID: "player-1", DurationSecond: 30}, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"player_id":"player-1"}`))
	}))
	t.Cleanup(server.Close)

	var output bytes.Buffer
	err := executeAdminCommand(server.Client(), server.URL, key, "mute", "player-1", 30*time.Second, &output)
	require.NoError(t, err)
	assert.JSONEq(t, `{"player_id":"player-1"}`, output.String())
}
