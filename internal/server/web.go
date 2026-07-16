package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	webui "github.com/palemoky/fight-the-landlord/web"
)

const (
	indexCacheControl = "no-cache"
	assetCacheControl = "public, max-age=31536000, immutable"
	fileCacheControl  = "public, max-age=3600"
)

type spaHandler struct {
	assets        fs.FS
	index         []byte
	indexETag     string
	clientVersion string
}

func loadWebAssets() fs.FS {
	assets, err := webui.Assets()
	if err != nil {
		log.Printf("Web client unavailable: %v (use Vite for development or build with -tags=webui)", err)
		return nil
	}
	return assets
}

func (s *Server) httpHandler(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/livez", s.handleLivez)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/version", s.handleVersion)
	mux.HandleFunc("/session/revoke", s.handleSessionRevoke)

	if assets != nil {
		staticHandler, err := newSPAHandler(assets, Version)
		if err != nil {
			log.Printf("Web client unavailable: %v", err)
		} else {
			mux.Handle("/", staticHandler)
		}
	}

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; media-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type revokeSessionRequest struct {
	PlayerID string `json:"player_id"`
	Token    string `json:"token"`
}

func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.originChecker == nil || !s.originChecker.Check(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if s.sessionManager == nil {
		http.Error(w, "session service unavailable", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var payload revokeSessionRequest
	if err := decoder.Decode(&payload); err != nil || payload.PlayerID == "" || payload.Token == "" || len(payload.Token) > 128 {
		http.Error(w, "invalid revoke request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid revoke request", http.StatusBadRequest)
		return
	}
	if !s.sessionManager.RevokeSession(payload.Token, payload.PlayerID) {
		http.Error(w, "invalid session", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func newSPAHandler(assets fs.FS, clientVersion string) (http.Handler, error) {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read embedded index: %w", err)
	}

	digest := sha256.Sum256(index)
	return &spaHandler{
		assets:        assets,
		index:         index,
		indexETag:     `"` + hex.EncodeToString(digest[:]) + `"`,
		clientVersion: clientVersion,
	}, nil
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := cleanAssetPath(r.URL.Path)
	if name == "" || name == "index.html" {
		h.serveIndex(w, r)
		return
	}

	if fs.ValidPath(name) {
		if content, err := fs.ReadFile(h.assets, name); err == nil {
			h.serveAsset(w, r, name, content)
			return
		}
	}

	if !strings.HasPrefix(name, "assets/") && path.Ext(name) == "" {
		h.serveIndex(w, r)
		return
	}

	http.NotFound(w, r)
}

func (h *spaHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", indexCacheControl)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("ETag", h.indexETag)
	w.Header().Set("X-Web-Client-Version", h.clientVersion)
	if r.Header.Get("If-None-Match") == h.indexETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.index))
}

func (h *spaHandler) serveAsset(w http.ResponseWriter, r *http.Request, name string, content []byte) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", assetCacheControl)
	} else {
		w.Header().Set("Cache-Control", fileCacheControl)
	}
	w.Header().Set("X-Web-Client-Version", h.clientVersion)
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(content))
}

func cleanAssetPath(requestPath string) string {
	for _, part := range strings.Split(requestPath, "/") {
		if part == ".." {
			return "-invalid-.asset"
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+requestPath), "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}
