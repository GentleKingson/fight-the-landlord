package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// handleWebSocket 处理 WebSocket 连接
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// 获取真实客户端IP
	clientIP := GetClientIP(r)
	if s.ipResolver != nil {
		clientIP = s.ipResolver.Resolve(r)
	}

	// 维护模式检查（最优先）
	if s.shuttingDown.Load() || s.IsMaintenanceMode() {
		s.recordWebSocketRejection("maintenance", clientIP)
		log.Printf("🔧 维护模式，拒绝新连接: %s", clientIP)
		http.Error(w, "Server is under maintenance, please try again later",
			http.StatusServiceUnavailable)
		return
	}

	// Reserve one physical-connection lease before Upgrade. The handler owns
	// the lease until both pumps have been installed; afterwards WritePump
	// releases it only when the WebSocket is actually closed.
	lease, acquired := s.activeConnectionLimiter().tryAcquire()
	if !acquired {
		s.recordWebSocketRejection("capacity", clientIP)
		log.Printf("🚫 达到最大连接数限制 (%d), IP: %s", s.maxConnections, clientIP)
		http.Error(w, "Server Full", http.StatusServiceUnavailable)
		return
	}
	leaseTransferred := false
	defer func() {
		if !leaseTransferred {
			lease.release()
		}
	}()

	// IP 过滤检查
	if !s.ipFilter.IsAllowed(clientIP) {
		s.recordWebSocketRejection("ip_filter", clientIP)
		log.Printf("🚫 IP %s 被过滤器拒绝", clientIP)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// 来源验证
	if !s.originChecker.Check(r) {
		s.recordWebSocketRejection("origin", clientIP)
		log.Printf("🚫 来源验证失败: %s (IP: %s)", r.Header.Get("Origin"), clientIP)
		http.Error(w, "Origin not allowed", http.StatusForbidden)
		return
	}

	// 速率限制检查
	if !s.rateLimiter.Allow(clientIP) {
		s.recordWebSocketRejection("rate_limit", clientIP)
		log.Printf("🚫 IP %s 请求过于频繁", clientIP)
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.recordWebSocketRejection("upgrade", clientIP)
		log.Printf("WebSocket 升级失败: %v", err)
		return
	}
	lease.activate()
	negotiated, err := s.negotiateWebSocket(conn)
	if err != nil {
		s.recordWebSocketRejection("handshake", clientIP)
		log.Printf("协议握手失败 (IP: %s): %v", clientIP, err)
		_ = conn.Close()
		return
	}

	leaseTransferred = s.activateWebSocketClient(conn, lease, negotiated, clientIP)
}

func (s *Server) activateWebSocketClient(
	conn *websocket.Conn,
	lease *connectionLease,
	negotiated negotiatedClient,
	clientIP string,
) bool {
	client := newClientWithLease(s, conn, lease)
	client.IP = clientIP
	client.clientVersion = negotiated.version
	client.clientKind = negotiated.kind
	client.capabilities = append([]string(nil), negotiated.capabilities...)
	s.registerClient(client)

	playerID := client.GetID()
	playerName := client.GetName()
	playerSession, err := s.sessionManager.CreateSession(playerID, playerName)
	if err != nil {
		s.recordWebSocketRejection("session", clientIP)
		log.Printf("创建安全会话失败: %v", err)
		s.unregisterClient(client)
		_ = conn.Close()
		return false
	}

	connected := codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
		PlayerID:       playerID,
		PlayerName:     playerName,
		ReconnectToken: playerSession.ReconnectToken,
	})
	if err := client.SendMessage(connected); err != nil {
		s.recordWebSocketRejection("delivery", clientIP)
		log.Printf("发送连接确认失败: %v", err)
		s.unregisterClient(client)
		s.sessionManager.DeleteSession(playerID)
		_ = conn.Close()
		return false
	}

	if !s.startClientPumps(client) {
		s.recordWebSocketRejection("shutdown", clientIP)
		client.Close()
		s.unregisterClient(client)
		s.sessionManager.DeleteSession(playerID)
		_ = conn.Close()
		return false
	}
	if s.metrics != nil {
		s.metrics.ConnectionAccepted()
	}
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("websocket connected",
		"event", "websocket_connected",
		"player_id", playerID,
		"client_kind", client.clientKind,
		"protocol_version", protocol.ProtocolVersion,
	)
	log.Printf("✅ 玩家 %s (%s) 已连接", playerName, playerID)
	return true
}

func (s *Server) recordWebSocketRejection(reason, clientIP string) {
	if s.metrics != nil {
		s.metrics.ConnectionRejected()
	}
	if s.logger != nil {
		s.logger.Warn("websocket rejected", "event", "websocket_rejected", "error_code", reason, "client_ip", clientIP)
	}
}

func (s *Server) startClientPumps(client *Client) bool {
	s.clientPumpsMu.Lock()
	if s.clientPumpsClosed || s.shuttingDown.Load() {
		s.clientPumpsMu.Unlock()
		return false
	}
	s.clientPumpsWG.Add(2)
	s.clientPumpsMu.Unlock()

	go func() {
		defer s.clientPumpsWG.Done()
		client.WritePump()
	}()
	go func() {
		defer s.clientPumpsWG.Done()
		client.ReadPump()
	}()
	return true
}

// handleHealth 健康检查接口
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !allowReadMethod(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (s *Server) handleLivez(w http.ResponseWriter, r *http.Request) {
	s.handleHealth(w, r)
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !allowReadMethod(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.shuttingDown.Load() || s.readinessCheck == nil {
		if s.metrics != nil {
			s.metrics.SetReady(false)
		}
		http.Error(w, "NOT READY", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.readinessCheck(ctx); err != nil {
		if s.metrics != nil {
			s.metrics.SetReady(false)
		}
		http.Error(w, "NOT READY", http.StatusServiceUnavailable)
		return
	}
	if s.metrics != nil {
		s.metrics.SetReady(true)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("READY"))
}

// handleVersion 版本接口，向客户端公布服务端版本及其要求的最低客户端版本。
//
// 客户端启动时据此判断是否需要强制升级，使升级策略由服务端集中控制。
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !allowReadMethod(w, r) {
		return
	}

	resp := struct {
		ServerVersion    string `json:"server_version"`
		MinClientVersion string `json:"min_client_version"`
		WebClientVersion string `json:"web_client_version"`
	}{
		ServerVersion:    Version,
		WebClientVersion: Version,
	}
	if s.config != nil {
		resp.MinClientVersion = s.config.Server.MinClientVersion
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("⚠️  写入版本响应失败: %v", err)
	}
}

func allowReadMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

// registerClient 注册客户端
func (s *Server) registerClient(client *Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.clients[client.GetID()] = client
}

// unregisterClient 注销客户端
func (s *Server) unregisterClient(client *Client) bool {
	playerID := client.GetID()
	playerName := client.GetName()

	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	current, ok := s.clients[playerID]
	if !ok || current != client {
		return false
	}

	delete(s.clients, playerID)
	log.Printf("❌ 玩家 %s (%s) 已断开", playerName, playerID)
	return true
}

// Interface implementations for types.ServerContext

func (s *Server) GetClientByID(id string) types.ClientInterface {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	client, ok := s.clients[id]
	if !ok || client == nil {
		return nil
	}
	return client
}

func (s *Server) RegisterClient(id string, client types.ClientInterface) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if c, ok := client.(*Client); ok {
		s.clients[id] = c
	}
}

func (s *Server) UnregisterClient(id string, client types.ClientInterface) bool {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	expected, ok := client.(*Client)
	if !ok || s.clients[id] != expected {
		return false
	}
	delete(s.clients, id)
	return true
}

// RebindClient replaces the provisional connection mapping with the restored
// player identity. The mapping and effective identity change together while
// the client registry is locked, so stale disconnects cannot remove the new
// owner of the player ID.
func (s *Server) RebindClient(temporaryID, playerID, playerName, roomCode string, client types.ClientInterface) (types.ClientInterface, error) {
	rebound, ok := client.(*Client)
	if !ok {
		return nil, fmt.Errorf("client %T does not support identity rebinding", client)
	}

	s.clientsMu.Lock()
	current, exists := s.clients[temporaryID]
	if !exists || current != rebound {
		s.clientsMu.Unlock()
		return nil, fmt.Errorf("temporary client %q is no longer active", temporaryID)
	}

	previous := s.clients[playerID]
	if !rebound.rebindIdentityIfUnbound(temporaryID, playerID, playerName, roomCode) {
		s.clientsMu.Unlock()
		return nil, fmt.Errorf("temporary client %q gained a room or changed identity during reconnect", temporaryID)
	}
	if temporaryID != playerID {
		delete(s.clients, temporaryID)
	}
	s.clients[playerID] = rebound
	s.clientsMu.Unlock()

	if s.messageLimiter != nil && temporaryID != playerID {
		s.messageLimiter.ClearRateLimit(temporaryID)
	}
	if s.chatLimiter != nil && temporaryID != playerID {
		s.chatLimiter.ClearRateLimit(temporaryID)
	}

	if previous == nil || previous == rebound {
		return nil, nil
	}
	return previous, nil
}

// RollbackRebindClient restores the exact registry and physical identity that
// existed before RebindClient. It is used when the final reconnect snapshot
// cannot be enqueued, so the rotated credential and identity never commit
// without an observable success response.
func (s *Server) RollbackRebindClient(temporaryID, temporaryName, playerID, roomCode string, client, previous types.ClientInterface) error {
	_ = roomCode
	rebound, ok := client.(*Client)
	if !ok {
		return fmt.Errorf("client %T does not support identity rollback", client)
	}
	var previousClient *Client
	if previous != nil {
		var previousOK bool
		previousClient, previousOK = previous.(*Client)
		if !previousOK {
			return fmt.Errorf("previous client %T cannot be restored", previous)
		}
	}

	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if s.clients[playerID] != rebound {
		return fmt.Errorf("restored player %q is no longer owned by rebound client", playerID)
	}
	if current := s.clients[temporaryID]; current != nil && current != rebound {
		return fmt.Errorf("temporary identity %q has already been reused", temporaryID)
	}
	if !rebound.rollbackReboundIdentity(playerID, temporaryID, temporaryName) {
		return fmt.Errorf("rebound client identity changed before rollback")
	}
	delete(s.clients, playerID)
	if previousClient != nil {
		s.clients[playerID] = previousClient
	}
	s.clients[temporaryID] = rebound
	return nil
}
