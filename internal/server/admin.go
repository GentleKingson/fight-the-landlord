package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	adminKeyHeader           = "X-Admin-Key"
	maxAdminBodyBytes        = 4096
	defaultAdminRequestLimit = 60
	maxModerationDuration    = 7 * 24 * time.Hour
)

type adminRateLimiter struct {
	mu          sync.Mutex
	windowStart time.Time
	requests    int
	limit       int
	window      time.Duration
	now         func() time.Time
}

func newAdminRateLimiter(limit int, window time.Duration) *adminRateLimiter {
	return &adminRateLimiter{limit: limit, window: window, now: time.Now}
}

func (limiter *adminRateLimiter) allow() bool {
	if limiter == nil {
		return false
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	if limiter.now != nil {
		now = limiter.now()
	}
	if limiter.windowStart.IsZero() || now.Sub(limiter.windowStart) >= limiter.window || now.Before(limiter.windowStart) {
		limiter.windowStart = now
		limiter.requests = 0
	}
	if limiter.requests >= limiter.limit {
		return false
	}
	limiter.requests++
	return true
}

func (s *Server) activeAdminLimiter() *adminRateLimiter {
	if s == nil {
		return nil
	}
	s.adminLimiterOnce.Do(func() {
		if s.adminLimiter == nil {
			s.adminLimiter = newAdminRateLimiter(defaultAdminRequestLimit, time.Minute)
		}
	})
	return s.adminLimiter
}

type adminStatusResponse struct {
	State         string `json:"state"`
	ActiveGames   int    `json:"active_games"`
	SafeToRestart bool   `json:"safe_to_restart"`
	OnlinePlayers int    `json:"online_players"`
	MutedPlayers  int    `json:"muted_players"`
	BannedPlayers int    `json:"banned_players"`
	ServerVersion string `json:"server_version"`
}

type adminPlayerRequest struct {
	PlayerID string `json:"player_id"`
}

type adminModerationRequest struct {
	PlayerID       string `json:"player_id"`
	DurationSecond int64  `json:"duration_seconds"`
}

func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdminRequest(w, r, http.MethodGet) {
		return
	}
	s.writeAdminStatus(w)
}

func (s *Server) handleAdminDrain(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdminRequest(w, r, http.MethodPost) || !decodeEmptyAdminBody(w, r) {
		return
	}
	changed := s.EnterDrainingMode()
	s.auditAdminAction("drain", "", changed, 0)
	s.writeAdminStatus(w)
}

func (s *Server) handleAdminMaintenance(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdminRequest(w, r, http.MethodPost) || !decodeEmptyAdminBody(w, r) {
		return
	}
	changed := s.setOperationalState(operationalMaintenance)
	s.auditAdminAction("maintenance", "", changed, 0)
	s.writeAdminStatus(w)
}

func (s *Server) handleAdminResume(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdminRequest(w, r, http.MethodPost) || !decodeEmptyAdminBody(w, r) {
		return
	}
	changed := s.ResumeNormalOperation()
	s.auditAdminAction("resume", "", changed, 0)
	s.writeAdminStatus(w)
}

func (s *Server) handleAdminDisconnect(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdminRequest(w, r, http.MethodPost) {
		return
	}
	var request adminPlayerRequest
	if !decodeAdminJSON(w, r, &request) || !validateAdminPlayerID(w, request.PlayerID) {
		return
	}
	s.sessionAuthorityMu.Lock()
	client := s.GetClientByID(request.PlayerID)
	changed := client != nil
	if client != nil {
		if closed, ok := client.(interface{ IsClosed() bool }); ok && closed.IsClosed() {
			changed = false
		}
		client.Close()
	}
	s.sessionAuthorityMu.Unlock()
	s.auditAdminAction("disconnect", request.PlayerID, changed, 0)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminMute(w http.ResponseWriter, r *http.Request) {
	s.handleAdminModeration(w, r, "mute")
}

func (s *Server) handleAdminBan(w http.ResponseWriter, r *http.Request) {
	s.handleAdminModeration(w, r, "ban")
}

func (s *Server) handleAdminModeration(w http.ResponseWriter, r *http.Request, action string) {
	if !s.authorizeAdminRequest(w, r, http.MethodPost) {
		return
	}
	var request adminModerationRequest
	if !decodeAdminJSON(w, r, &request) || !validateAdminPlayerID(w, request.PlayerID) {
		return
	}
	if request.DurationSecond <= 0 || request.DurationSecond > int64(maxModerationDuration/time.Second) {
		http.Error(w, "duration_seconds must be between 1 and 604800", http.StatusBadRequest)
		return
	}
	duration := time.Duration(request.DurationSecond) * time.Second
	store := s.activeModerationStore()
	var expiresAt time.Time
	var changed bool
	if action == "mute" {
		expiresAt, changed = store.mute(request.PlayerID, duration)
	} else {
		s.sessionAuthorityMu.Lock()
		expiresAt, changed = store.ban(request.PlayerID, duration)
		if client := s.GetClientByID(request.PlayerID); client != nil {
			client.Close()
		}
		s.sessionAuthorityMu.Unlock()
	}
	s.auditAdminAction(action, request.PlayerID, changed, expiresAt.Unix())
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"player_id":  request.PlayerID,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleAdminUnmute(w http.ResponseWriter, r *http.Request) {
	s.handleAdminModerationRemoval(w, r, "unmute")
}

func (s *Server) handleAdminUnban(w http.ResponseWriter, r *http.Request) {
	s.handleAdminModerationRemoval(w, r, "unban")
}

func (s *Server) handleAdminModerationRemoval(w http.ResponseWriter, r *http.Request, action string) {
	if !s.authorizeAdminRequest(w, r, http.MethodPost) {
		return
	}
	var request adminPlayerRequest
	if !decodeAdminJSON(w, r, &request) || !validateAdminPlayerID(w, request.PlayerID) {
		return
	}
	store := s.activeModerationStore()
	changed := false
	if action == "unmute" {
		changed = store.unmute(request.PlayerID)
	} else {
		s.sessionAuthorityMu.Lock()
		changed = store.unban(request.PlayerID)
		s.sessionAuthorityMu.Unlock()
	}
	s.auditAdminAction(action, request.PlayerID, changed, 0)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authorizeAdminRequest(w http.ResponseWriter, r *http.Request, method string) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !requestFromLoopback(r) {
		s.auditAdminRejection("non_loopback")
		http.Error(w, "Forbidden", http.StatusForbidden)
		return false
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return false
	}
	if limiter := s.activeAdminLimiter(); limiter == nil || !limiter.allow() {
		s.auditAdminRejection("rate_limit")
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return false
	}
	expected := ""
	if s != nil && s.config != nil {
		expected = s.config.Admin.Key
	}
	if expected == "" || !constantTimeSecretEqual(r.Header.Get(adminKeyHeader), expected) {
		s.auditAdminRejection("authentication")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func requestFromLoopback(r *http.Request) bool {
	if r == nil {
		return false
	}
	// Admin traffic must be a direct local connection, never a request relayed
	// by the public reverse proxy even when that proxy itself runs on loopback.
	if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func constantTimeSecretEqual(provided, expected string) bool {
	providedDigest := sha256.Sum256([]byte(provided))
	expectedDigest := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedDigest[:], expectedDigest[:]) == 1
}

func decodeEmptyAdminBody(w http.ResponseWriter, r *http.Request) bool {
	var payload map[string]json.RawMessage
	decoder := newAdminJSONDecoder(w, r)
	err := decoder.Decode(&payload)
	if errors.Is(err, io.EOF) {
		return true
	}
	if err != nil {
		writeAdminDecodeError(w, err)
		return false
	}
	if payload == nil || len(payload) != 0 || !adminJSONEnded(decoder) {
		http.Error(w, "request body must be empty or {}", http.StatusBadRequest)
		return false
	}
	return true
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := newAdminJSONDecoder(w, r)
	if err := decoder.Decode(target); err != nil {
		writeAdminDecodeError(w, err)
		return false
	}
	if !adminJSONEnded(decoder) {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return false
	}
	return true
}

func newAdminJSONDecoder(w http.ResponseWriter, r *http.Request) *json.Decoder {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder
}

func adminJSONEnded(decoder *json.Decoder) bool {
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func writeAdminDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid JSON request", http.StatusBadRequest)
}

func validateAdminPlayerID(w http.ResponseWriter, playerID string) bool {
	if playerID == "" || len(playerID) > 128 || playerID != strings.TrimSpace(playerID) {
		http.Error(w, "invalid player_id", http.StatusBadRequest)
		return false
	}
	for i := range len(playerID) {
		value := playerID[i]
		if (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') || value == '-' || value == '_' || value == '.' || value == ':' {
			continue
		}
		http.Error(w, "invalid player_id", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) writeAdminStatus(w http.ResponseWriter) {
	activeGames := 0
	if s != nil && s.roomManager != nil {
		activeGames = s.roomManager.GetActiveGamesCount()
	}
	muted, banned := 0, 0
	if store := s.activeModerationStore(); store != nil {
		muted, banned = store.counts()
	}
	state := s.OperationalState()
	writeAdminJSON(w, http.StatusOK, adminStatusResponse{
		State:         state,
		ActiveGames:   activeGames,
		SafeToRestart: state != OperationalStateNormal && activeGames == 0,
		OnlinePlayers: s.GetOnlineCount(),
		MutedPlayers:  muted,
		BannedPlayers: banned,
		ServerVersion: Version,
	})
}

func writeAdminJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) auditAdminAction(action, playerID string, changed bool, expiresAt int64) {
	logger := s.adminLogger()
	result := "no_change"
	if changed {
		result = "applied"
	}
	attributes := []any{
		"event", "admin_action",
		"action", action,
		"result", result,
	}
	if playerID != "" {
		attributes = append(attributes, "player_id", playerID)
	}
	if expiresAt != 0 {
		attributes = append(attributes, "expires_at", expiresAt)
	}
	logger.Info("admin action completed", attributes...)
}

func (s *Server) auditAdminRejection(reason string) {
	s.adminLogger().Warn("admin request rejected",
		"event", "admin_request_rejected",
		"reason", reason,
	)
}

func (s *Server) adminLogger() *slog.Logger {
	if s != nil && s.logger != nil {
		return s.logger
	}
	return slog.Default()
}
