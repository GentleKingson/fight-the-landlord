package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// handleWebSocket 处理 WebSocket 连接
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// 获取真实客户端IP
	clientIP := GetClientIP(r)

	// 维护模式检查（最优先）
	if s.IsMaintenanceMode() {
		log.Printf("🔧 维护模式，拒绝新连接: %s", clientIP)
		http.Error(w, "Server is under maintenance, please try again later",
			http.StatusServiceUnavailable)
		return
	}

	// 连接数限制检查
	select {
	case s.semaphore <- struct{}{}:
		// 成功获取信号量，连接建立后释放
		defer func() { <-s.semaphore }()
	default:
		log.Printf("🚫 达到最大连接数限制 (%d), IP: %s", s.maxConnections, clientIP)
		http.Error(w, "Server Full", http.StatusServiceUnavailable)
		return
	}

	// IP 过滤检查
	if !s.ipFilter.IsAllowed(clientIP) {
		log.Printf("🚫 IP %s 被过滤器拒绝", clientIP)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// 来源验证
	if !s.originChecker.Check(r) {
		log.Printf("🚫 来源验证失败: %s (IP: %s)", r.Header.Get("Origin"), clientIP)
		http.Error(w, "Origin not allowed", http.StatusForbidden)
		return
	}

	// 速率限制检查
	if !s.rateLimiter.Allow(clientIP) {
		log.Printf("🚫 IP %s 请求过于频繁", clientIP)
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket 升级失败: %v", err)
		return
	}

	// 创建客户端
	client := NewClient(s, conn)
	client.IP = clientIP // 记录客户端 IP
	s.registerClient(client)

	// 创建会话
	playerID := client.GetID()
	playerName := client.GetName()
	session := s.sessionManager.CreateSession(playerID, playerName)

	// 发送连接成功消息（包含重连令牌）
	client.SendMessage(codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
		PlayerID:       playerID,
		PlayerName:     playerName,
		ReconnectToken: session.ReconnectToken,
	}))

	log.Printf("✅ 玩家 %s (%s) 已连接", playerName, playerID)

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
	return s.clients[id]
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
	rebound.rebindIdentity(playerID, playerName, roomCode)
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
