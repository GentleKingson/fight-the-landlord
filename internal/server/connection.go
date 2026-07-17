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
	clientIP := s.webSocketClientIP(r)
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
	pending := &pendingWebSocketActivation{server: s, lease: lease}
	defer pending.release()
	if !s.validateWebSocketUpgradeRequest(w, r, clientIP) {
		return
	}
	browser, prepared := s.prepareBrowserWebSocket(w, r, clientIP)
	if !prepared {
		return
	}
	pending.ownerToken = browser.ownerToken

	conn, err := upgrader.Upgrade(w, r, browser.upgradeHeaders)
	if err != nil {
		s.recordWebSocketRejection("upgrade", clientIP)
		log.Printf("WebSocket 升级失败: %v", err)
		return
	}
	lease.activate()
	negotiated, err := s.negotiateWebSocket(conn, browser.transport)
	if err != nil {
		s.recordWebSocketRejection("handshake", clientIP)
		log.Printf("协议握手失败 (IP: %s): %v", clientIP, err)
		_ = conn.Close()
		return
	}
	negotiated.browserTransport = browser.transport
	negotiated.browserReconnectToken = browser.reconnectToken
	negotiated.browserTicketOwnerToken = browser.ownerToken

	pending.transferred = s.activateWebSocketClient(conn, lease, negotiated, clientIP)
}

type pendingWebSocketActivation struct {
	server      *Server
	lease       *connectionLease
	ownerToken  string
	transferred bool
}

func (pending *pendingWebSocketActivation) release() {
	if pending == nil || pending.transferred {
		return
	}
	pending.lease.release()
	if pending.ownerToken != "" {
		pending.server.activeWebSessionTickets().ReleaseOwnerNonce(pending.ownerToken)
	}
}

type browserWebSocketPreparation struct {
	transport      bool
	reconnectToken string
	ownerToken     string
	upgradeHeaders http.Header
}

func (s *Server) webSocketClientIP(r *http.Request) string {
	if s.ipResolver != nil {
		return s.ipResolver.Resolve(r)
	}
	return GetClientIP(r)
}

func (s *Server) validateWebSocketUpgradeRequest(w http.ResponseWriter, r *http.Request, clientIP string) bool {
	if !s.ipFilter.IsAllowed(clientIP) {
		s.recordWebSocketRejection("ip_filter", clientIP)
		log.Printf("🚫 IP %s 被过滤器拒绝", clientIP)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return false
	}
	if !s.originChecker.Check(r) {
		s.recordWebSocketRejection("origin", clientIP)
		log.Printf("🚫 来源验证失败: %s (IP: %s)", r.Header.Get("Origin"), clientIP)
		http.Error(w, "Origin not allowed", http.StatusForbidden)
		return false
	}
	// Reject excess attempts before allocating a nonce or timer.
	if !s.rateLimiter.Allow(clientIP) {
		s.recordWebSocketRejection("rate_limit", clientIP)
		log.Printf("🚫 IP %s 请求过于频繁", clientIP)
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return false
	}
	return true
}

func (s *Server) prepareBrowserWebSocket(
	w http.ResponseWriter,
	r *http.Request,
	clientIP string,
) (browserWebSocketPreparation, bool) {
	preparation := browserWebSocketPreparation{transport: isBrowserTransportRequest(r)}
	if !preparation.transport {
		return preparation, true
	}
	presentedToken := readWebSessionCookie(r)
	observed, known := s.observeBrowserReconnectToken(presentedToken)
	if presentedToken != "" && !observed && known {
		s.recordWebSocketRejection("session_busy", clientIP)
		http.Error(w, "Web session is unavailable", http.StatusConflict)
		return browserWebSocketPreparation{}, false
	}
	if observed {
		preparation.reconnectToken = presentedToken
		return preparation, true
	}
	ownerToken, err := s.activeWebSessionTickets().AcquireOwnerNonce()
	if err != nil {
		s.recordWebSocketRejection("session_owner", clientIP)
		http.Error(w, "Session service unavailable", http.StatusServiceUnavailable)
		return browserWebSocketPreparation{}, false
	}
	preparation.ownerToken = ownerToken
	preparation.upgradeHeaders = make(http.Header)
	preparation.upgradeHeaders.Add("Set-Cookie", webSessionOwnerCookie(
		ownerToken,
		requestUsesHTTPS(r, s.ipResolver),
		time.Now(),
	).String())
	return preparation, true
}

func (s *Server) observeBrowserReconnectToken(token string) (observed, known bool) {
	if token == "" || s.sessionManager == nil {
		return false, false
	}
	s.sessionAuthorityMu.Lock()
	defer s.sessionAuthorityMu.Unlock()
	observed = s.sessionManager.ObserveWebSessionToken(token)
	if observed {
		s.activeWebSessionTickets().ObserveSuccessor(token)
	}
	return observed, s.sessionManager.IsKnownWebSessionToken(token)
}

func (s *Server) activateWebSocketClient(
	conn *websocket.Conn,
	lease *connectionLease,
	negotiated negotiatedClient,
	clientIP string,
) bool {
	unlockAuthority, authorized := s.acquireBrowserWebSocketActivationAuthority(conn, negotiated, clientIP)
	if !authorized {
		return false
	}
	defer unlockAuthority()
	client := s.newActivatedWebSocketClient(conn, lease, negotiated, clientIP)
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

	connectedPayload, err := s.connectedPayloadForClient(client, playerSession.ReconnectToken)
	if err != nil {
		s.recordWebSocketRejection("session_ticket", clientIP)
		log.Printf("创建 Web 会话确认票据失败: %v", err)
		s.cleanupRejectedClientSession(client, conn, playerID, false)
		return false
	}
	connected := codec.MustNewMessage(protocol.MsgConnected, connectedPayload)
	if err := client.SendMessage(connected); err != nil {
		s.recordWebSocketRejection("delivery", clientIP)
		log.Printf("发送连接确认失败: %v", err)
		s.cleanupRejectedClientSession(client, conn, playerID, true)
		return false
	}

	if !s.startClientPumps(client) {
		s.recordWebSocketRejection("shutdown", clientIP)
		s.cleanupClientPumpStartFailure(client, conn, playerID)
		return false
	}
	if s.metrics != nil {
		s.metrics.ConnectionAccepted()
	}
	s.logAcceptedWebSocket(client, playerID, playerName)
	return true
}

func (s *Server) acquireBrowserWebSocketActivationAuthority(
	conn *websocket.Conn,
	negotiated negotiatedClient,
	clientIP string,
) (func(), bool) {
	if !negotiated.browserTransport {
		return func() {}, true
	}
	s.sessionAuthorityMu.Lock()
	if negotiated.browserReconnectToken == "" || s.sessionManager == nil {
		return s.sessionAuthorityMu.Unlock, true
	}
	observed := s.sessionManager.ObserveWebSessionToken(negotiated.browserReconnectToken)
	if observed {
		s.activeWebSessionTickets().ObserveSuccessor(negotiated.browserReconnectToken)
		return s.sessionAuthorityMu.Unlock, true
	}
	s.recordWebSocketRejection("session_race", clientIP)
	_ = conn.Close()
	s.sessionAuthorityMu.Unlock()
	return func() {}, false
}

func (s *Server) newActivatedWebSocketClient(
	conn *websocket.Conn,
	lease *connectionLease,
	negotiated negotiatedClient,
	clientIP string,
) *Client {
	client := newClientWithLease(s, conn, lease)
	client.IP = clientIP
	client.clientVersion = negotiated.version
	client.clientKind = negotiated.kind
	client.capabilities = append([]string(nil), negotiated.capabilities...)
	client.browserTransport = negotiated.browserTransport
	client.browserReconnectToken = negotiated.browserReconnectToken
	client.browserTicketOwnerToken = negotiated.browserTicketOwnerToken
	return client
}

func (s *Server) connectedPayloadForClient(
	client *Client,
	reconnectToken string,
) (protocol.ConnectedPayload, error) {
	payload := protocol.ConnectedPayload{PlayerID: client.GetID(), PlayerName: client.GetName()}
	if !client.IsBrowserTransport() {
		payload.ReconnectToken = reconnectToken
		return payload, nil
	}
	ticket, err := client.IssueWebSessionTicket(reconnectToken, client.BrowserReconnectToken(), nil, nil)
	if err != nil {
		return protocol.ConnectedPayload{}, err
	}
	if !client.setProvisionalWebSessionTicket(ticket) {
		client.InvalidateWebSessionTicket(ticket)
		return protocol.ConnectedPayload{}, errWebSessionTicketEntropy
	}
	payload.WebSessionTicket = ticket
	payload.ReconnectAvailable = s.sessionManager.CanReconnectToken(client.BrowserReconnectToken())
	return payload, nil
}

func (s *Server) cleanupRejectedClientSession(
	client *Client,
	conn *websocket.Conn,
	playerID string,
	invalidateTicket bool,
) {
	s.unregisterClient(client)
	if invalidateTicket {
		client.InvalidateProvisionalWebSessionTicket()
	}
	s.sessionManager.DeleteSession(playerID)
	_ = conn.Close()
}

func (s *Server) cleanupClientPumpStartFailure(client *Client, conn *websocket.Conn, playerID string) {
	client.Close()
	s.unregisterClient(client)
	client.InvalidateProvisionalWebSessionTicket()
	s.sessionManager.DeleteSession(playerID)
	_ = conn.Close()
}

func (s *Server) logAcceptedWebSocket(client *Client, playerID, playerName string) {
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
}

func (s *Server) recordWebSocketRejection(reason, clientIP string) {
	if s.metrics != nil {
		s.metrics.ConnectionRejected()
	}
	if s.logger != nil {
		s.logger.Warn("websocket rejected", "event", "websocket_rejected", "error_code", reason, "client_ip", clientIP)
	}
}

func (s *Server) LockSessionAuthority() {
	if s != nil {
		s.sessionAuthorityMu.Lock()
	}
}

// AcquireBrowserReconnectAuthority drains the exact active browser generation
// before reconnect can rotate or publish its identity. It never waits for a
// client command while holding sessionAuthorityMu.
func (s *Server) AcquireBrowserReconnectAuthority(
	reconnectToken string,
	reconnectingClient types.ClientInterface,
) (func(), bool) {
	if s == nil {
		return func() {}, false
	}
	s.sessionAuthorityMu.Lock()
	if reconnectToken == "" || s.sessionManager == nil {
		return s.sessionAuthorityMu.Unlock, true
	}
	playerSession := s.sessionManager.GetSessionByToken(reconnectToken)
	if playerSession == nil {
		return s.sessionAuthorityMu.Unlock, true
	}
	playerID := playerSession.PlayerID
	current, ok := s.GetClientByID(playerID).(*Client)
	if !ok || !current.IsBrowserTransport() {
		return s.sessionAuthorityMu.Unlock, true
	}
	// A reconnect command on the connection that already owns this identity is
	// not a migration. Holding its command barrier through ticket rollback would
	// make the ticket callback re-enter the same write lock and deadlock.
	if reconnecting, concrete := reconnectingClient.(*Client); concrete && current == reconnecting {
		s.sessionAuthorityMu.Unlock()
		return func() {}, false
	}

	if !current.beginWebSessionTransition(reconnectToken) {
		s.sessionAuthorityMu.Unlock()
		return func() {}, false
	}
	s.sessionAuthorityMu.Unlock()
	current.webCommandMu.Lock()
	s.sessionAuthorityMu.Lock()
	latest := s.sessionManager.GetSessionByToken(reconnectToken)
	if latest == nil || latest.PlayerID != playerID || s.GetClientByID(playerID) != current {
		current.endWebSessionTransition()
		s.sessionAuthorityMu.Unlock()
		current.webCommandMu.Unlock()
		return func() {}, false
	}
	return func() {
		current.endWebSessionTransition()
		s.sessionAuthorityMu.Unlock()
		current.webCommandMu.Unlock()
	}, true
}

// RetireBrowserSessionClient is called while sessionAuthorityMu is write-held
// by reconnect publication. It gates the displaced connection immediately,
// then drains it asynchronously without retaining global authority.
func (s *Server) RetireBrowserSessionClient(playerID string, client types.ClientInterface) {
	retired, ok := client.(*Client)
	if s == nil || playerID == "" || !ok || !retired.IsBrowserTransport() {
		return
	}
	retired.RevokeWebSessionAuthorization()
	if s.retiredBrowserClients == nil {
		s.retiredBrowserClients = make(map[string]map[*Client]string)
	}
	lineage := s.retiredBrowserClients[playerID]
	if lineage == nil {
		lineage = make(map[*Client]string)
		s.retiredBrowserClients[playerID] = lineage
	}
	if _, exists := lineage[retired]; exists {
		return
	}
	lineage[retired] = retired.browserSessionCredentialSnapshot()
	go s.drainRetiredBrowserSessionClient(playerID, retired)
}

func (s *Server) drainRetiredBrowserSessionClient(playerID string, retired *Client) {
	waitForBrowserCommandDrain(retired)

	s.sessionAuthorityMu.Lock()
	defer s.sessionAuthorityMu.Unlock()
	lineage := s.retiredBrowserClients[playerID]
	delete(lineage, retired)
	if len(lineage) == 0 {
		delete(s.retiredBrowserClients, playerID)
	}
}

func waitForBrowserCommandDrain(client *Client) {
	client.webCommandMu.Lock()
	client.RevokeWebSessionAuthorization()
	client.webCommandMu.Unlock()
}

// collectRetiredBrowserSessionClients is called under sessionAuthorityMu and
// snapshots exact generations for a revoke response barrier. Removal may race
// only after a generation has already drained, so an omitted entry is safe.
func (s *Server) collectRetiredBrowserSessionClients(
	playerID string,
	clients map[*Client]struct{},
	credentials map[string]struct{},
) {
	for retired, credential := range s.retiredBrowserClients[playerID] {
		retired.RevokeWebSessionAuthorization()
		clients[retired] = struct{}{}
		if credential != "" {
			credentials[credential] = struct{}{}
		}
	}
}

// registerBrowserRevokeDrains publishes an immutable response barrier under
// sessionAuthorityMu and returns every pre-existing barrier that intersects
// the credential set. A new barrier contains only clients not already covered
// by those barriers, so one cross-lineage request cannot discard newly revoked
// generations when one of its credentials is already draining.
func (s *Server) registerBrowserRevokeDrains(
	clients map[*Client]struct{},
	credentials map[string]struct{},
) map[*browserRevokeDrain]bool {
	work := make(map[*browserRevokeDrain]bool)
	coveredClients := make(map[*Client]struct{})
	for credential := range credentials {
		if drain := s.browserRevokeDrains[credential]; drain != nil {
			work[drain] = false
			for client := range drain.clients {
				coveredClients[client] = struct{}{}
			}
		}
	}

	uncoveredClients := make(map[*Client]struct{})
	for client := range clients {
		if _, covered := coveredClients[client]; !covered {
			uncoveredClients[client] = struct{}{}
		}
	}
	unmappedCredentials := make(map[string]struct{})
	for credential := range credentials {
		if credential != "" && s.browserRevokeDrains[credential] == nil {
			unmappedCredentials[credential] = struct{}{}
		}
	}
	if len(uncoveredClients) == 0 && (len(work) == 0 || len(unmappedCredentials) == 0) {
		return work
	}

	drain := &browserRevokeDrain{
		clients:      uncoveredClients,
		credentials:  unmappedCredentials,
		dependencies: make(map[*browserRevokeDrain]struct{}, len(work)),
		done:         make(chan struct{}),
	}
	for dependency := range work {
		drain.dependencies[dependency] = struct{}{}
	}
	if s.browserRevokeDrains == nil {
		s.browserRevokeDrains = make(map[string]*browserRevokeDrain)
	}
	for credential := range unmappedCredentials {
		s.browserRevokeDrains[credential] = drain
	}
	work[drain] = true
	return work
}

func (s *Server) browserRevokeDrain(credential string) *browserRevokeDrain {
	if credential == "" {
		return nil
	}
	return s.browserRevokeDrains[credential]
}

func (s *Server) completeBrowserRevokeDrain(drain *browserRevokeDrain) {
	if s == nil || drain == nil {
		return
	}
	drain.completeOnce.Do(func() {
		s.sessionAuthorityMu.Lock()
		for credential := range drain.credentials {
			if s.browserRevokeDrains[credential] == drain {
				delete(s.browserRevokeDrains, credential)
			}
		}
		close(drain.done)
		s.sessionAuthorityMu.Unlock()
	})
}

func (s *Server) UnlockSessionAuthority() {
	if s != nil {
		s.sessionAuthorityMu.Unlock()
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
