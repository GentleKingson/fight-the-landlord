package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/stretchr/testify/require"
)

func TestWebRoutesAndPrivateDataIsolation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>斗地主</html>"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "assets"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("js"), 0o600))
	cfg := &config.Config{Server: config.ServerConfig{WebDir: dir}, Game: config.GameConfig{CollectTrainingData: true, TrainingDataDir: t.TempDir()}}
	require.NoError(t, validateWebConfig(cfg))
	s := &Server{config: cfg}
	for path, status := range map[string]int{"/": 200, "/assets/app.js": 200, "/api/config": 200, "/assets/": 404, "/data/games/example.jsonl": 404, "/config.yaml": 404, "/api/missing": 404} {
		r := httptest.NewRecorder()
		s.routes().ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		require.Equal(t, status, r.Code, path)
		if path == "/api/config" {
			require.JSONEq(t, `{"collect_training_data":true}`, r.Body.String())
		}
	}
	cfg.Server.WebDir = filepath.Join(dir, "missing")
	require.Error(t, validateWebConfig(cfg))
}
