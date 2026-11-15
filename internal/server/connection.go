package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

func (s *Server) resolveClientIP(r *http.Request) string {
	if s.ipResolver != nil {
		return s.ipResolver.Resolve(r)
	}
	return GetClientIP(r)
}

func headerContainsToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func hasValidWebSocketHeaders(w http.ResponseWriter, r *http.Request) (gorillaHandled, valid bool) {
	if r.Method != http.MethodGet ||
		!headerContainsToken(r.Header, "Connection", "upgrade") ||
		!headerContainsToken(r.Header, "Upgrade", "websocket") ||
		!headerContainsToken(r.Header, "Sec-Websocket-Version", "13") {
		return true, false
	}
	if _, ok := w.(http.Hijacker); !ok {
		return true, false
	}
	rawKey := strings.TrimSpace(r.Header.Get("Sec-Websocket-Key"))
	if rawKey == "" {
		return true, false
	}
	decoded, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil || len(decoded) != 16 {
		return false, false
	}
	return false, true
}

func (s *Server) checkWebSocketAdmission(w http.ResponseWriter, r *http.Request, clientIP string) bool {
	if s.IsMaintenanceMode() {
		log.Printf("🔧 维护模式，拒绝新连接: %s", clientIP)
		http.Error(w, "Server is under maintenance, please try again later", http.StatusServiceUnavailable)
		return false
	}
	if !s.ipFilter.IsAllowed(clientIP) {
		log.Printf("🚫 IP %s 被过滤器拒绝", clientIP)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return false
	}
	if !s.originChecker.Check(r) {
		log.Printf("🚫 来源验证失败: %s (IP: %s)", r.Header.Get("Origin"), clientIP)
		http.Error(w, "Origin not allowed", http.StatusForbidden)
		return false
	}
	if !s.rateLimiter.Allow(clientIP) {
		log.Printf("🚫 IP %s 请求过于频繁", clientIP)
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return false
	}
	return true
}

func (s *Server) acquireConnectionSlot(w http.ResponseWriter, clientIP string) (func(), bool) {
	var releaseOnce sync.Once
	releaseSlot := func() {
		if s.semaphore != nil {
			releaseOnce.Do(func() { <-s.semaphore })
		}
	}
	if s.semaphore != nil {
		select {
		case s.semaphore <- struct{}{}:
		default:
			log.Printf("🚫 达到最大连接数限制 (%d), IP: %s", s.maxConnections, clientIP)
			http.Error(w, "Server Full", http.StatusServiceUnavailable)
			return nil, false
		}
	}
	return releaseSlot, true
}

// handleWebSocket 处理 WebSocket 连接
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// 获取真实客户端IP（默认仅使用直连对端，仅信任已配置的可信代理）
	clientIP := s.resolveClientIP(r)
	if !s.checkWebSocketAdmission(w, r, clientIP) {
		return
	}

	upgrade := upgrader
	upgrade.CheckOrigin = s.originChecker.Check

	// 在创建持久身份前先校验合法 WebSocket 升级请求，防止普通 HTTP 请求污染 Redis
	if gorillaHandled, valid := hasValidWebSocketHeaders(w, r); !valid {
		if gorillaHandled {
			_, _ = upgrade.Upgrade(w, r, nil)
		} else {
			http.Error(w, "websocket: invalid Sec-WebSocket-Key", http.StatusBadRequest)
		}
		return
	}

	// 连接数限制检查：槽位生命周期覆盖整个存活连接，而非仅限握手阶段
	releaseSlot, ok := s.acquireConnectionSlot(w, clientIP)
	if !ok {
		return
	}
	handedOff := false
	defer func() {
		if !handedOff {
			releaseSlot()
		}
	}()

	identity, token, headers, created, err := s.resolveIdentity(r)
	if err != nil {
		http.Error(w, "Identity service unavailable", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrade.Upgrade(w, r, headers)
	if err != nil {
		if created && identity != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = s.redisStore.DeleteIdentity(cleanupCtx, identity.ID)
			cancel()
		}
		log.Printf("WebSocket 升级失败: %v", err)
		return
	}

	// 创建客户端并移交信号量槽位释放职责
	client := NewClient(s, conn)
	client.onConnClose = releaseSlot
	handedOff = true
	client.IP = clientIP // 记录客户端 IP
	client.ID, client.Name = identity.ID, identity.Name
	client.browser = r.Header.Get("Origin") != ""
	s.sessionManager.EnsureSession(client.ID, client.Name)
	s.RestoreClient(client.ID, client, func() {
		client.SendMessage(codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
			PlayerID: client.ID, PlayerName: client.Name, ReconnectToken: token,
		}))
		if client.browser {
			s.handler.RestoreConnection(client, client.ID)
		} else {
			s.sessionManager.SetOnline(client.ID)
		}
	})

	log.Printf("✅ 玩家 %s (%s) 已连接", client.Name, client.ID)

	// 启动客户端读写协程
	go client.ReadPump()
	go client.WritePump()
}

// handleHealth 健康检查接口
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// handleVersion 版本接口，向客户端公布服务端版本及其要求的最低客户端版本。
//
// 客户端启动时据此判断是否需要强制升级，使升级策略由服务端集中控制。
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	resp := struct {
		ServerVersion    string `json:"server_version"`
		MinClientVersion string `json:"min_client_version"`
	}{
		ServerVersion:    Version,
		MinClientVersion: s.config.Server.MinClientVersion,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("⚠️  写入版本响应失败: %v", err)
	}
}

// registerClient 注册客户端
func (s *Server) registerClient(client *Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.clients[client.ID] = client
}

// unregisterClient 注销客户端
func (s *Server) unregisterClient(client *Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	if current, ok := s.clients[client.GetID()]; ok && current == client {
		delete(s.clients, client.GetID())
		log.Printf("❌ 玩家 %s (%s) 已断开", client.Name, client.ID)
	}
}

// Interface implementations for types.ServerContext

func (s *Server) GetClientByID(id string) types.ClientInterface {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	if c := s.clients[id]; c != nil {
		return c
	}
	return nil
}

func (s *Server) RegisterClient(id string, client types.ClientInterface) {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	s.registerIdentity(id, client)
}

// RestoreClient keeps registration and room recovery ordered against other reconnects and disconnects.
func (s *Server) RestoreClient(id string, client types.ClientInterface, restore func()) {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	if c, ok := client.(*Client); ok {
		c.mu.RLock()
		closed := c.closed
		c.mu.RUnlock()
		if closed {
			return
		}
		if c.GetRoom() != "" && c.GetID() != id {
			c.SendMessage(codec.NewErrorMessage(protocol.ErrCodeGameStarted))
			return
		}
	}
	s.registerIdentity(id, client)
	restore()
}

// registerIdentity requires connectionMu; release clientsMu before invoking room recovery.
func (s *Server) registerIdentity(id string, client types.ClientInterface) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	c, ok := client.(*Client)
	if !ok {
		return
	}
	if previous := s.clients[id]; previous != nil && previous != c {
		if s.matcher != nil {
			s.matcher.RemoveFromQueue(previous)
		}
		previous.closeWithCode(closeReplaced)
	}
	oldID := c.GetID()
	if oldID != id {
		if s.matcher != nil {
			s.matcher.RemoveFromQueue(c)
		}
		delete(s.clients, oldID)
		if s.sessionManager != nil {
			s.sessionManager.DeleteSession(oldID)
		}
	}
	c.mu.Lock()
	c.ID = id
	if s.sessionManager != nil {
		if saved := s.sessionManager.Snapshot(id); saved != nil {
			c.Name = saved.PlayerName
		}
	}
	c.mu.Unlock()
	s.clients[id] = c
}

func (s *Server) UnregisterClient(id string) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	delete(s.clients, id)
}

// Matching commits seats under the same lock as socket replacement. A queued
// request from a replaced/disconnected client cannot create an orphaned game.
func (s *Server) withActiveClients(clients []types.ClientInterface, create func()) bool {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	for _, c := range clients {
		if !c.IsBot() && s.GetClientByID(c.GetID()) != c {
			return false
		}
		if client, ok := c.(*Client); ok && client.isClosed() {
			return false
		}
	}
	create()
	return true
}
