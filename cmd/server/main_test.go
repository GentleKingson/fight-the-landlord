package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckHealth(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	require.NoError(t, checkHealth(server.URL))
}

func TestCheckHealthRejectsUnhealthyStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	require.Error(t, checkHealth(server.URL))
}

func TestCheckHealthRejectsRedirects(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthy" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/healthy", http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	require.Error(t, checkHealth(server.URL))
}

func TestDefaultHealthcheckURLUsesServerPort(t *testing.T) {
	t.Setenv("SERVER_PORT", "9876")
	require.Equal(t, "http://127.0.0.1:9876/health", defaultHealthcheckURL())
}

func TestLoadServerConfigFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := loadServerConfig(filepath.Join(t.TempDir(), "missing.yaml"), false)
	require.ErrorContains(t, err, "load config")
}

func TestLoadServerConfigAllowsExplicitDevelopmentFallback(t *testing.T) {
	t.Parallel()

	cfg, err := loadServerConfig(filepath.Join(t.TempDir(), "missing.yaml"), true)
	require.NoError(t, err)
	require.Equal(t, "development", cfg.Server.Environment)
	require.Equal(t, 1780, cfg.Server.Port)
}
