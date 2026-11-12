package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/config"
)

const webPageCSP = "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; media-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

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
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = json.NewEncoder(w).Encode(map[string]any{"collect_training_data": s.config.Game.CollectTrainingData})
	})
	if s.config.Server.WebDir != "" {
		mux.Handle("/", http.HandlerFunc(s.handleWebStatic))
	}
	return mux
}

func (s *Server) handleWebStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// No directory browsing or SPA fallback for private data/API paths.
	relPath, isPage, ok := resolveWebTarget(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(s.config.Server.WebDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = root.Close() }()

	file, err := root.Open(relPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	applyWebHeaders(w, r.URL.Path, relPath, isPage)
	serveCompressedOrRaw(w, r, root, relPath, file, info.ModTime())
}

func resolveWebTarget(urlPath string) (relPath string, isPage, ok bool) {
	if urlPath == "/" || urlPath == "/index.html" {
		return "index.html", true, true
	}
	if isWebAsset(urlPath) {
		return urlPath[1:], false, true
	}
	return "", false, false
}

func applyWebHeaders(w http.ResponseWriter, urlPath, relPath string, isPage bool) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if isPage || strings.EqualFold(filepath.Ext(relPath), ".svg") {
		w.Header().Set("Content-Security-Policy", webPageCSP)
		w.Header().Set("X-Frame-Options", "DENY")
	}
	if !isPage && hashedWebAsset.MatchString(urlPath) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if contentType := webMimeType(relPath); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
}

func serveCompressedOrRaw(
	w http.ResponseWriter,
	r *http.Request,
	root *os.Root,
	relPath string,
	rawFile *os.File,
	modTime time.Time,
) {
	ext := strings.ToLower(filepath.Ext(relPath))
	baseName := filepath.Base(relPath)
	if !isCompressibleExt(ext) {
		http.ServeContent(w, r, baseName, modTime, rawFile)
		return
	}

	w.Header().Set("Vary", "Accept-Encoding")
	if r.Header.Get("Range") != "" {
		http.ServeContent(w, r, baseName, modTime, rawFile)
		return
	}

	encodings := negotiateEncodings(r.Header.Values("Accept-Encoding"))
	for _, enc := range encodings {
		sidecarExt := ".gz"
		if enc == "br" {
			sidecarExt = ".br"
		}
		if tryServeSidecar(w, r, root, relPath, sidecarExt, enc, modTime) {
			return
		}
	}

	if slices.Contains(encodings, "gzip") && serveDynamicGzip(w, r, baseName, modTime, rawFile) {
		return
	}

	http.ServeContent(w, r, baseName, modTime, rawFile)
}

func tryServeSidecar(
	w http.ResponseWriter,
	r *http.Request,
	root *os.Root,
	relPath, sidecarExt, encoding string,
	modTime time.Time,
) bool {
	sidecar, err := root.Open(relPath + sidecarExt)
	if err != nil {
		return false
	}
	defer func() { _ = sidecar.Close() }()

	info, err := sidecar.Stat()
	if err != nil || info.IsDir() {
		return false
	}

	w.Header().Set("Content-Encoding", encoding)
	http.ServeContent(w, r, filepath.Base(relPath), modTime, sidecar)
	return true
}

func serveDynamicGzip(
	w http.ResponseWriter,
	r *http.Request,
	baseName string,
	modTime time.Time,
	rawFile *os.File,
) bool {
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	if _, err := io.Copy(gzWriter, rawFile); err != nil {
		_ = gzWriter.Close()
		_, _ = rawFile.Seek(0, io.SeekStart)
		return false
	}
	if err := gzWriter.Close(); err != nil {
		_, _ = rawFile.Seek(0, io.SeekStart)
		return false
	}
	w.Header().Set("Content-Encoding", "gzip")
	http.ServeContent(w, r, baseName, modTime, bytes.NewReader(buf.Bytes()))
	return true
}

func negotiateEncodings(headerValues []string) []string {
	if len(headerValues) == 0 {
		return nil
	}
	qByEncoding := map[string]float64{}
	wildcardQ := -1.0

	for _, rawHeader := range headerValues {
		for _, part := range strings.Split(rawHeader, ",") {
			name, q, ok := parseEncodingPart(part)
			if !ok {
				continue
			}
			if name == "*" {
				wildcardQ = max(wildcardQ, q)
				continue
			}
			qByEncoding[name] = q
		}
	}

	brQ := resolveEncodingQuality("br", qByEncoding, wildcardQ)
	gzipQ := resolveEncodingQuality("gzip", qByEncoding, wildcardQ)

	result := make([]string, 0, 2)
	if brQ > 0 && brQ >= gzipQ {
		result = append(result, "br")
	}
	if gzipQ > 0 {
		result = append(result, "gzip")
	}
	if brQ > 0 && brQ < gzipQ {
		result = append(result, "br")
	}
	return result
}

func resolveEncodingQuality(name string, qByEncoding map[string]float64, wildcardQ float64) float64 {
	if q, exists := qByEncoding[name]; exists {
		return q
	}
	if wildcardQ > 0 {
		return wildcardQ
	}
	return 0
}

func parseEncodingPart(part string) (name string, q float64, ok bool) {
	segments := strings.Split(strings.TrimSpace(part), ";")
	name = strings.ToLower(strings.TrimSpace(segments[0]))
	if name == "" {
		return "", 0, false
	}
	q = 1.0
	for _, param := range segments[1:] {
		key, val, found := strings.Cut(strings.TrimSpace(param), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil || parsed < 0 || parsed > 1 {
			return "", 0, false
		}
		q = parsed
	}
	return name, q, true
}

func isCompressibleExt(ext string) bool {
	switch ext {
	case ".html", ".js", ".mjs", ".css", ".svg", ".json", ".webmanifest", ".txt", ".xml", ".wasm":
		return true
	default:
		return false
	}
}

func webMimeType(relPath string) string {
	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".webmanifest":
		return "application/manifest+json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".mp3":
		return "audio/mpeg"
	case ".wasm":
		return "application/wasm"
	default:
		return mime.TypeByExtension(ext)
	}
}

func isWebAsset(path string) bool {
	// Vite produces hashed files directly under /assets; never expose sidecar archives directly.
	if filepath.Dir(path) != "/assets" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext != "" && ext != ".br" && ext != ".gz"
}

var hashedWebAsset = regexp.MustCompile(`^/assets/[^/]+-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$`)
