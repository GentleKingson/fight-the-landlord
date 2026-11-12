package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/palemoky/fight-the-landlord/internal/config"
)

func validateWebConfig(cfg *config.Config) error {
	if cfg.Server.WebDir != "" {
		if info, err := os.Stat(filepath.Join(cfg.Server.WebDir, "index.html")); err != nil || info.IsDir() {
			return fmt.Errorf("WEB_DIR must contain a built index.html: %s", cfg.Server.WebDir)
		}
	}
	if cfg.Game.CollectTrainingData {
		if err := os.MkdirAll(cfg.Game.TrainingDataDir, 0o700); err != nil {
			return err
		}
		f, err := os.CreateTemp(cfg.Game.TrainingDataDir, ".write-check-*")
		if err != nil {
			return fmt.Errorf("training data directory is not writable: %w", err)
		}
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	return nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/version", s.handleVersion)
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"collect_training_data": s.config.Game.CollectTrainingData})
	})
	if s.config.Server.WebDir != "" {
		files := http.FileServer(http.Dir(s.config.Server.WebDir))
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			// No directory browsing or SPA fallback for private data/API paths.
			if r.URL.Path != "/" && r.URL.Path != "/index.html" && !isWebAsset(r.URL.Path) {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-cache")
			if existingHashedAsset(s.config.Server.WebDir, r.URL.Path) {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
		}))
	}
	return mux
}

func isWebAsset(path string) bool {
	// Vite produces hashed files directly under /assets.
	return filepath.Dir(path) == "/assets" && filepath.Ext(path) != ""
}

var hashedWebAsset = regexp.MustCompile(`^/assets/[^/]+-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$`)

func existingHashedAsset(dir, path string) bool {
	if !hashedWebAsset.MatchString(path) {
		return false
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(path[1:])
	return err == nil && !info.IsDir()
}
