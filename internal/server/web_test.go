package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
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
	for path, status := range map[string]int{
		"/":                         http.StatusOK,
		"/index.html":               http.StatusOK,
		"/assets/app.js":            http.StatusOK,
		"/api/config":               http.StatusOK,
		"/assets/":                  http.StatusNotFound,
		"/data/games/example.jsonl": http.StatusNotFound,
		"/config.yaml":              http.StatusNotFound,
		"/api/missing":              http.StatusNotFound,
	} {
		r := httptest.NewRecorder()
		s.routes().ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		require.Equal(t, status, r.Code, path)
		if path == "/api/config" {
			require.JSONEq(t, `{"collect_training_data":true}`, r.Body.String())
			require.Equal(t, "nosniff", r.Header().Get("X-Content-Type-Options"))
		}
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "app-Ab12_cd3.js"), []byte("js"), 0o600))
	for path, cache := range map[string]string{
		"/":                           "no-cache",
		"/assets/app.js":              "no-cache",
		"/assets/app-Ab12_cd3.js":     "public, max-age=31536000, immutable",
		"/assets/missing-Ab12_cd3.js": "",
		"/api/config":                 "no-store",
	} {
		response := httptest.NewRecorder()
		s.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		require.Equal(t, cache, response.Header().Get("Cache-Control"), path)
	}
	cfg.Server.WebDir = filepath.Join(dir, "missing")
	require.Error(t, validateWebConfig(cfg))
}

func TestWebSecurityHeadersOnPageResponses(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html><body>斗地主</body></html>"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "assets"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "icon-Ab12_cd3.svg"), []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), 0o600))

	s := &Server{config: &config.Config{Server: config.ServerConfig{WebDir: dir}}}
	handler := s.routes()

	for _, pagePath := range []string{"/", "/index.html"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pagePath, http.NoBody))
		require.Equal(t, http.StatusOK, rec.Code, pagePath)

		csp := rec.Header().Get("Content-Security-Policy")
		require.Contains(t, csp, "default-src 'self'", pagePath)
		require.Contains(t, csp, "connect-src 'self' ws: wss:", pagePath)
		require.Contains(t, csp, "img-src 'self' data:", pagePath)
		require.Contains(t, csp, "media-src 'self'", pagePath)
		require.Contains(t, csp, "object-src 'none'", pagePath)
		require.Contains(t, csp, "base-uri 'none'", pagePath)
		require.Contains(t, csp, "form-action 'self'", pagePath)
		require.Contains(t, csp, "frame-ancestors 'none'", pagePath)

		require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), pagePath)
		require.Equal(t, "strict-origin-when-cross-origin", rec.Header().Get("Referrer-Policy"), pagePath)
		require.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"), pagePath)
		require.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"), pagePath)
		require.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"), pagePath)
	}

	svgRec := httptest.NewRecorder()
	handler.ServeHTTP(svgRec, httptest.NewRequest(http.MethodGet, "/assets/icon-Ab12_cd3.svg", http.NoBody))
	require.Equal(t, http.StatusOK, svgRec.Code)
	require.NotEmpty(t, svgRec.Header().Get("Content-Security-Policy"))
	require.Equal(t, "image/svg+xml", svgRec.Header().Get("Content-Type"))
}

func TestWebCompressionNegotiationAndMediaAssets(t *testing.T) {
	dir := t.TempDir()
	rawHTML := []byte("<!doctype html><html lang='zh-CN'><body>斗地主大厅</body></html>")
	rawJS := []byte("console.log('fight-the-landlord bundle');")
	precompressedBr := []byte("precompressed-brotli-payload")
	precompressedGz := []byte("precompressed-gzip-payload")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), rawHTML, 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "assets"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "index-Ab12_cd3.js"), rawJS, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "index-Ab12_cd3.js.br"), precompressedBr, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "index-Ab12_cd3.js.gz"), precompressedGz, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "scene-lobby-1600-HOfWufoG.webp"), []byte("RIFFwebpdata"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "deal-BJhQUqjV.mp3"), []byte("ID3mockmp3audio"), 0o600))

	s := &Server{config: &config.Config{Server: config.ServerConfig{WebDir: dir}}}
	handler := s.routes()

	// 1. Prefer Brotli sidecar when Accept-Encoding advertises both gzip and br.
	brReq := httptest.NewRequest(http.MethodGet, "/assets/index-Ab12_cd3.js", http.NoBody)
	brReq.Header.Set("Accept-Encoding", "gzip, deflate, br")
	brRec := httptest.NewRecorder()
	handler.ServeHTTP(brRec, brReq)
	require.Equal(t, http.StatusOK, brRec.Code)
	require.Equal(t, "br", brRec.Header().Get("Content-Encoding"))
	require.Equal(t, "Accept-Encoding", brRec.Header().Get("Vary"))
	require.Equal(t, "text/javascript; charset=utf-8", brRec.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", brRec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "public, max-age=31536000, immutable", brRec.Header().Get("Cache-Control"))
	require.Equal(t, precompressedBr, brRec.Body.Bytes())

	// 2. Respect q-values when gzip has higher quality or br is excluded (q=0).
	for _, acceptEncoding := range []string{"gzip;q=0.9, br;q=0.5", "br;q=0, gzip;q=1.0", "gzip"} {
		gzReq := httptest.NewRequest(http.MethodGet, "/assets/index-Ab12_cd3.js", http.NoBody)
		gzReq.Header.Set("Accept-Encoding", acceptEncoding)
		gzRec := httptest.NewRecorder()
		handler.ServeHTTP(gzRec, gzReq)
		require.Equal(t, http.StatusOK, gzRec.Code, acceptEncoding)
		require.Equal(t, "gzip", gzRec.Header().Get("Content-Encoding"), acceptEncoding)
		require.Equal(t, "text/javascript; charset=utf-8", gzRec.Header().Get("Content-Type"), acceptEncoding)
		require.Equal(t, precompressedGz, gzRec.Body.Bytes(), acceptEncoding)
	}

	// 3. Dynamic gzip fallback when sidecar does not exist on disk (e.g., index.html in temp dir).
	dynGzReq := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	dynGzReq.Header.Set("Accept-Encoding", "gzip")
	dynGzRec := httptest.NewRecorder()
	handler.ServeHTTP(dynGzRec, dynGzReq)
	require.Equal(t, http.StatusOK, dynGzRec.Code)
	require.Equal(t, "gzip", dynGzRec.Header().Get("Content-Encoding"))
	require.Equal(t, "text/html; charset=utf-8", dynGzRec.Header().Get("Content-Type"))
	gzReader, err := gzip.NewReader(dynGzRec.Body)
	require.NoError(t, err)
	decompressed, err := io.ReadAll(gzReader)
	require.NoError(t, err)
	require.NoError(t, gzReader.Close())
	require.Equal(t, rawHTML, decompressed)

	// 4. Direct access to .br / .gz sidecars must be blocked with 404.
	for _, blockedPath := range []string{
		"/assets/index-Ab12_cd3.js.br",
		"/assets/index-Ab12_cd3.js.gz",
		"/index.html.br",
		"/index.html.gz",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, blockedPath, http.NoBody))
		require.Equal(t, http.StatusNotFound, rec.Code, blockedPath)
	}

	// 5. Image (.webp) and Audio (.mp3, including Range requests) load with exact MIME types and no double compression.
	imgReq := httptest.NewRequest(http.MethodGet, "/assets/scene-lobby-1600-HOfWufoG.webp", http.NoBody)
	imgReq.Header.Set("Accept-Encoding", "gzip, br")
	imgRec := httptest.NewRecorder()
	handler.ServeHTTP(imgRec, imgReq)
	require.Equal(t, http.StatusOK, imgRec.Code)
	require.Equal(t, "image/webp", imgRec.Header().Get("Content-Type"))
	require.Empty(t, imgRec.Header().Get("Content-Encoding"))
	require.Equal(t, "nosniff", imgRec.Header().Get("X-Content-Type-Options"))

	audioReq := httptest.NewRequest(http.MethodGet, "/assets/deal-BJhQUqjV.mp3", http.NoBody)
	audioReq.Header.Set("Accept-Encoding", "gzip, br")
	audioReq.Header.Set("Range", "bytes=0-3")
	audioRec := httptest.NewRecorder()
	handler.ServeHTTP(audioRec, audioReq)
	require.Equal(t, http.StatusPartialContent, audioRec.Code)
	require.Equal(t, "audio/mpeg", audioRec.Header().Get("Content-Type"))
	require.Empty(t, audioRec.Header().Get("Content-Encoding"))
	require.Equal(t, "ID3m", audioRec.Body.String())
}

func TestWebPageWebSocketAudioAndImageIntegration(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html><body>斗地主</body></html>"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "assets"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "scene-lobby-960-MaaCcYaE.webp"), []byte("RIFFwebp"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "play-B3BDG03X.mp3"), []byte("ID3mp3"), 0o600))

	redis := miniredis.RunT(t)
	s, httpServer := identityServer(t, redis)
	s.config.Server.WebDir = dir
	// Rebuild routes with WebDir enabled.
	httpServer.Config.Handler = s.routes()

	client := httpServer.Client()
	for _, tc := range []struct {
		path        string
		contentType string
		wantCSP     bool
	}{
		{path: "/", contentType: "text/html; charset=utf-8", wantCSP: true},
		{path: "/assets/scene-lobby-960-MaaCcYaE.webp", contentType: "image/webp", wantCSP: false},
		{path: "/assets/play-B3BDG03X.mp3", contentType: "audio/mpeg", wantCSP: false},
	} {
		req, err := http.NewRequest(http.MethodGet, httpServer.URL+tc.path, http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode, tc.path)
		require.Equal(t, tc.contentType, resp.Header.Get("Content-Type"), tc.path)
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"), tc.path)
		if tc.wantCSP {
			require.Equal(t, webPageCSP, resp.Header.Get("Content-Security-Policy"))
			require.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
			require.Equal(t, "strict-origin-when-cross-origin", resp.Header.Get("Referrer-Policy"))
		}
	}

	wsConn, _ := dialIdentity(t, httpServer.URL, nil, true)
	defer func() { _ = wsConn.Close() }()
	_, rawMsg, err := wsConn.ReadMessage()
	require.NoError(t, err)
	msg, err := codec.Decode(rawMsg)
	require.NoError(t, err)
	require.Equal(t, protocol.MsgConnected, msg.Type)
}
