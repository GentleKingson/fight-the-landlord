package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	mux.HandleFunc("/version", s.handleVersion)

	if assets != nil {
		staticHandler, err := newSPAHandler(assets, Version)
		if err != nil {
			log.Printf("Web client unavailable: %v", err)
		} else {
			mux.Handle("/", staticHandler)
		}
	}

	return mux
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
