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
	mux.HandleFunc("/session/commit", s.handleSessionCommit)
	mux.HandleFunc("/session/refresh", s.handleSessionRefresh)
	mux.HandleFunc("/session/revoke", s.handleSessionRevoke)
	if s.config != nil && s.config.Observability.MetricsPath != "" {
		metricsHandler := http.NotFoundHandler()
		if s.config.Observability.MetricsEnabled && s.metrics != nil {
			metricsHandler = s.metrics.Handler()
		}
		// Register even when disabled so the SPA fallback cannot turn the
		// configured metrics path into a misleading HTTP 200 response.
		mux.Handle(s.config.Observability.MetricsPath, metricsHandler)
	}

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

type commitWebSessionRequest struct {
	Ticket string `json:"ticket"`
}

func (s *Server) handleSessionCommit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validateWebSessionRequest(w, r, s.originChecker) {
		return
	}
	var payload commitWebSessionRequest
	if !decodeBoundedJSON(w, r, &payload) || payload.Ticket == "" || len(payload.Ticket) > 128 {
		http.Error(w, "invalid commit request", http.StatusBadRequest)
		return
	}
	if s.sessionManager == nil || s.activeWebSessionTickets() == nil {
		http.Error(w, "session service unavailable", http.StatusServiceUnavailable)
		return
	}
	s.sessionAuthorityMu.Lock()
	defer s.sessionAuthorityMu.Unlock()
	token, ok := s.activeWebSessionTickets().Commit(
		payload.Ticket,
		readWebSessionCookie(r),
		readWebSessionOwnerCookie(r),
		s.sessionManager.CanReconnectToken,
	)
	if !ok {
		http.Error(w, "invalid session ticket", http.StatusUnauthorized)
		return
	}
	secure := requestUsesHTTPS(r, s.ipResolver)
	http.SetCookie(w, webSessionCookie(token, secure, time.Now()))
	http.SetCookie(w, expiredWebSessionOwnerCookie(secure))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validateWebSessionRequest(w, r, s.originChecker) {
		return
	}
	var payload map[string]json.RawMessage
	if !decodeBoundedJSON(w, r, &payload) || payload == nil || len(payload) != 0 {
		http.Error(w, "invalid refresh request", http.StatusBadRequest)
		return
	}
	if s.sessionManager == nil || s.activeWebSessionTickets() == nil {
		http.Error(w, "session service unavailable", http.StatusServiceUnavailable)
		return
	}
	s.sessionAuthorityMu.Lock()
	defer s.sessionAuthorityMu.Unlock()
	token := readWebSessionCookie(r)
	if token == "" || !s.sessionManager.ObserveWebSessionToken(token) {
		http.Error(w, "invalid web session", http.StatusUnauthorized)
		return
	}
	s.activeWebSessionTickets().ObserveSuccessor(token)
	http.SetCookie(w, webSessionCookie(token, requestUsesHTTPS(r, s.ipResolver), time.Now()))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validateWebSessionRequest(w, r, s.originChecker) {
		return
	}

	var payload map[string]json.RawMessage
	if !decodeBoundedJSON(w, r, &payload) || payload == nil || len(payload) != 0 {
		http.Error(w, "invalid revoke request", http.StatusBadRequest)
		return
	}
	secure := requestUsesHTTPS(r, s.ipResolver)
	http.SetCookie(w, expiredWebSessionCookie(secure))
	http.SetCookie(w, expiredWebSessionOwnerCookie(secure))
	if s.sessionManager != nil {
		token := readWebSessionCookie(r)
		owner := readWebSessionOwnerCookie(r)
		if token != "" || owner != "" {
			drainWork := make(map[*browserRevokeDrain]bool)
			func() {
				s.sessionAuthorityMu.Lock()
				defer s.sessionAuthorityMu.Unlock()
				browserClientsToClose := make(map[*Client]struct{})
				credentials := make(map[string]struct{})
				queueBrowserClient := func(browserClient *Client) {
					if browserClient == nil {
						return
					}
					if credential := browserClient.browserSessionCredentialSnapshot(); credential != "" {
						credentials[credential] = struct{}{}
					}
					// Deny new commands while authority is still exclusively
					// serialized. Waiting for an already-running command happens
					// after releasing the global lock so one slow player cannot
					// stall every unrelated browser session operation.
					browserClient.RevokeWebSessionAuthorization()
					browserClientsToClose[browserClient] = struct{}{}
				}
				closePlayer := func(playerID string) {
					if client := s.GetClientByID(playerID); client != nil {
						if browserClient, ok := client.(*Client); ok {
							queueBrowserClient(browserClient)
						} else {
							client.Close()
						}
					}
					s.collectRetiredBrowserSessionClients(playerID, browserClientsToClose, credentials)
				}
				if token != "" {
					credentials[token] = struct{}{}
					if existing := s.browserRevokeDrain(token); existing != nil {
						drainWork[existing] = false
					} else {
						s.collectPendingWebSessionCredentialLineage(token, credentials)
						playerID, lineage, revoked := s.sessionManager.RevokeSessionByTokenWithLineage(token)
						for _, credential := range lineage {
							credentials[credential] = struct{}{}
						}
						s.activeWebSessionTickets().InvalidatePendingCredential(token)
						if revoked {
							closePlayer(playerID)
						}
					}
				}
				if owner != "" {
					credentials[owner] = struct{}{}
					if existing := s.browserRevokeDrain(owner); existing != nil {
						drainWork[existing] = false
					} else {
						s.activeWebSessionTickets().InvalidatePendingOwner(owner, func(successor string) {
							credentials[successor] = struct{}{}
							if existing := s.browserRevokeDrain(successor); existing != nil {
								drainWork[existing] = false
								return
							}
							playerID, lineage, revoked := s.sessionManager.RevokeSessionByTokenWithLineage(successor)
							for _, credential := range lineage {
								credentials[credential] = struct{}{}
							}
							if revoked {
								closePlayer(playerID)
							}
						})
					}
				}
				for drain, leader := range s.registerBrowserRevokeDrains(browserClientsToClose, credentials) {
					if leader || !drainWork[drain] {
						drainWork[drain] = leader
					}
				}
			}()
			for drain, leader := range drainWork {
				if leader {
					go func() {
						defer s.completeBrowserRevokeDrain(drain)
						for browserClient := range drain.clients {
							browserClient.revokeAuthorizedWebSessionAndClose()
						}
						for dependency := range drain.dependencies {
							<-dependency.done
						}
					}()
				}
			}
			for drain := range drainWork {
				<-drain.done
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// collectPendingWebSessionCredentialLineage snapshots both sides of an
// unresolved ticket before revoke invalidation removes its indexes. It lets
// predecessor and successor retry requests share one in-flight drain.
func (s *Server) collectPendingWebSessionCredentialLineage(token string, credentials map[string]struct{}) {
	manager := s.activeWebSessionTickets()
	if manager == nil || token == "" {
		return
	}
	manager.mu.Lock()
	tickets := make(map[string]struct{})
	for ticket := range manager.predecessorTickets[token] {
		tickets[ticket] = struct{}{}
	}
	for ticket := range manager.successorTickets[token] {
		tickets[ticket] = struct{}{}
	}
	for ticket := range tickets {
		entry, ok := manager.entries[ticket]
		if !ok {
			continue
		}
		if entry.predecessorToken != "" {
			credentials[entry.predecessorToken] = struct{}{}
		}
		if entry.token != "" {
			credentials[entry.token] = struct{}{}
		}
	}
	manager.mu.Unlock()
}

func validateWebSessionRequest(w http.ResponseWriter, r *http.Request, checker *OriginChecker) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !browserOriginAllowed(checker, r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return false
	}
	return true
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
