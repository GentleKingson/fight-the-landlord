package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

const (
	writeWait      = 10 * time.Second    // 写入超时
	pongWait       = 60 * time.Second    // 读取超时（pong 等待时间）
	pingPeriod     = (pongWait * 9) / 10 // ping 发送间隔（必须小于 pongWait）
	maxMessageSize = 4096                // 消息最大大小
)

var (
	ErrClientClosed         = errors.New("client connection is closed")
	ErrClientSendBufferFull = errors.New("client send buffer is full")
)

// 昵称词库
var (
	adjectives = []string{
		"勇敢的", "聪明的", "快乐的", "神秘的", "酷炫的",
		"优雅的", "可爱的", "威武的", "沉稳的", "活泼的",
		"机智的", "潇洒的", "温柔的", "霸气的", "淡定的",
		"闪亮的", "迷人的", "傲娇的", "呆萌的", "高冷的",
	}

	nouns = []string{
		"小鸡", "熊猫", "老虎", "狮子", "猴子",
		"兔子", "狐狸", "海豚", "企鹅", "考拉",
		"柯基", "柴犬", "布偶", "龙猫", "仓鼠",
		"刺猬", "松鼠", "浣熊", "水獭", "羊驼",
	}
)

// GenerateNickname 生成随机昵称
func GenerateNickname() string {
	adj := adjectives[rand.IntN(len(adjectives))]
	noun := nouns[rand.IntN(len(nouns))]
	return adj + noun
}

// Client 代表一个连接的玩家
type Client struct {
	ID     string // 玩家唯一 ID
	Name   string // 玩家昵称
	RoomID string // 当前所在房间 ID
	IP     string // 客户端 IP 地址

	server                        *Server
	conn                          *websocket.Conn
	send                          chan []byte
	done                          chan struct{}
	lease                         *connectionLease
	clientVersion                 string
	clientKind                    string
	capabilities                  []string
	browserTransport              bool
	browserReconnectToken         string
	browserTicketOwnerToken       string
	provisionalWebSessionTicket   string
	trackedWebSessionTickets      map[string]struct{}
	webSessionClosed              bool
	authorizedWebSessionToken     string
	lastAuthorizedWebSessionToken string
	webSessionTransition          bool

	mu            sync.RWMutex
	webSessionMu  sync.Mutex
	webCommandMu  sync.RWMutex
	lifecycleMu   sync.RWMutex
	lifecycleOnce sync.Once
	closeOnce     sync.Once
	slowCloseOnce sync.Once
	closed        atomic.Bool
	commandMu     sync.Mutex
	activeCommand *activeCommandExecution
}

type activeCommandExecution struct {
	requestID string
	command   protocol.MessageType
	responses []*protocol.Message
}

// NewClient 创建新客户端
func NewClient(s *Server, conn *websocket.Conn) *Client {
	return newClientWithLease(s, conn, nil)
}

func newClientWithLease(s *Server, conn *websocket.Conn, lease *connectionLease) *Client {
	client := &Client{
		ID:     uuid.New().String(),
		Name:   GenerateNickname(),
		server: s,
		conn:   conn,
		send:   make(chan []byte, 256),
		done:   make(chan struct{}),
		lease:  lease,
	}
	return client
}

// ReadPump 从 WebSocket 读取消息
func (c *Client) ReadPump() {
	defer func() {
		c.Close()
		c.handleDisconnect()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		frameType, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				// Peer close text is attacker-controlled and may contain secrets or
				// chat content. Keep the ordinary log to a fixed category.
				log.Printf("WebSocket 读取异常关闭")
			}
			break
		}

		if !c.handleIncomingFrame(frameType, message) {
			break
		}
	}
}

func (c *Client) handleIncomingFrame(frameType int, frame []byte) bool {
	msg, decoded := c.decodeIncomingFrame(frameType, frame)
	if !decoded {
		return true
	}
	defer codec.PutMessage(msg)
	started := time.Now()
	c.attachLegacyChatCommand(msg)
	requestID, valid := c.validateIncomingCommand(msg, started)
	if !valid {
		return true
	}
	return c.handleValidatedIncomingCommand(msg, requestID, started)
}

func (c *Client) decodeIncomingFrame(frameType int, frame []byte) (*protocol.Message, bool) {
	if frameType != websocket.BinaryMessage {
		c.recordProtocolError("non_binary_frame")
		_ = c.SendMessage(codec.NewErrorMessageWithText(protocol.ErrCodeInvalidMsg, "消息必须使用二进制 protobuf 帧"))
		return nil, false
	}
	msg, err := codec.Decode(frame)
	if err != nil {
		c.recordProtocolError("decode")
		log.Printf("消息解析错误: %v", err)
		_ = c.SendMessage(codec.NewErrorMessage(protocol.ErrCodeInvalidMsg))
		return nil, false
	}
	return msg, true
}

func (c *Client) recordProtocolError(reason string) {
	if c.server != nil && c.server.metrics != nil {
		c.server.metrics.ProtocolError(reason)
	}
}

func (c *Client) validateIncomingCommand(msg *protocol.Message, started time.Time) (string, bool) {
	requestID := ""
	if msg.Command != nil {
		requestID = msg.Command.RequestID
	}
	if isClientCommand(msg.Type) && validRequestID(requestID) {
		return requestID, true
	}
	c.rejectInvalidIncomingCommand(msg.Type, requestID, started)
	return "", false
}

func (c *Client) rejectInvalidIncomingCommand(
	messageType protocol.MessageType,
	requestID string,
	started time.Time,
) {
	reason := "invalid_request_id"
	if !isClientCommand(messageType) {
		reason = "invalid_command"
	}
	if c.server != nil && c.server.metrics != nil {
		c.server.metrics.ProtocolError(reason)
		c.server.metrics.ObserveCommand(string(messageType), "invalid", time.Since(started))
	}
	correlatedRequestID := requestID
	if !validRequestID(correlatedRequestID) {
		correlatedRequestID = ""
	}
	response := codec.NewCorrelatedCommandErrorMessage(
		protocol.ErrCodeInvalidMsg,
		"无效的命令或 request_id",
		correlatedRequestID,
		messageType,
	)
	if correlatedRequestID == "" {
		response.Command = nil
	}
	_ = c.SendMessage(response)
}

func (c *Client) handleValidatedIncomingCommand(
	msg *protocol.Message,
	requestID string,
	started time.Time,
) bool {
	releaseAuthority, authorized := c.beginBrowserCommandAuthority(msg.Type)
	if !authorized {
		_ = c.SendMessage(codec.NewCorrelatedCommandErrorMessage(
			protocol.ErrCodeReconnectInvalid,
			"Web 会话尚未确认或已撤销",
			requestID,
			msg.Type,
		))
		c.Close()
		return false
	}
	defer releaseAuthority()
	playerID := c.GetID()
	cache := c.server.activeCommandCache()
	lookup, err := cache.begin(playerID, requestID, commandFingerprint(msg))
	if err != nil {
		c.rejectCommandCacheLookup(msg.Type, requestID, started, err)
		return true
	}
	if c.replayCachedCommand(cache, lookup, msg.Type, started) {
		return true
	}

	allowed, keepConnection := c.checkMessageRateLimit(playerID, requestID, msg.Type, cache, lookup.entry)
	if !allowed {
		if c.server.metrics != nil {
			c.server.metrics.ObserveCommand(string(msg.Type), "rate_limited", time.Since(started))
		}
		return keepConnection
	}

	result := c.executeCommand(msg, lookup.entry, cache)
	if c.server.metrics != nil {
		c.server.metrics.ObserveCommand(string(msg.Type), result, time.Since(started))
	}
	return true
}

func (c *Client) rejectCommandCacheLookup(
	messageType protocol.MessageType,
	requestID string,
	started time.Time,
	err error,
) {
	code := protocol.ErrCodeCommandCacheFull
	result := "unavailable"
	if errors.Is(err, errRequestConflict) {
		code = protocol.ErrCodeRequestConflict
		result = "conflict"
		if c.server.metrics != nil {
			c.server.metrics.IdempotencyConflict()
		}
	}
	if c.server.metrics != nil {
		c.server.metrics.ObserveCommand(string(messageType), result, time.Since(started))
	}
	_ = c.SendMessage(codec.NewCorrelatedCommandErrorMessage(code, "", requestID, messageType))
}

func (c *Client) replayCachedCommand(
	cache *commandCache,
	lookup commandCacheLookup,
	messageType protocol.MessageType,
	started time.Time,
) bool {
	if len(lookup.responses) > 0 {
		if c.server.metrics != nil {
			c.server.metrics.IdempotencyHit()
			c.server.metrics.ObserveCommand(string(messageType), "cache_hit", time.Since(started))
		}
		c.replayCommandResponses(lookup.responses)
		return true
	}
	if lookup.wait != nil {
		if c.server.metrics != nil {
			c.server.metrics.IdempotencyHit()
		}
		c.initializeLifecycle()
		select {
		case <-lookup.wait:
			c.replayCommandResponses(cache.responsesAfter(lookup.entry))
		case <-c.done:
		}
		if c.server.metrics != nil {
			c.server.metrics.ObserveCommand(string(messageType), "cache_hit", time.Since(started))
		}
		return true
	}
	return false
}

func (c *Client) checkMessageRateLimit(
	playerID string,
	requestID string,
	messageType protocol.MessageType,
	cache *commandCache,
	entry *commandCacheEntry,
) (allowed, keepConnection bool) {
	limiter := c.server.messageLimiter
	if limiter == nil {
		return true, true
	}
	allowed, warning := limiter.AllowMessage(playerID)
	if warning {
		_ = c.SendMessage(codec.MustNewMessage(protocol.MsgWarning, protocol.WarningPayload{
			Code: protocol.ErrCodeRateLimit, Message: "请求过于频繁，请放慢速度",
		}))
	}
	if allowed {
		return true, true
	}

	log.Printf("客户端 %s 消息过于频繁", c.GetName())
	response := codec.NewCorrelatedCommandErrorMessage(
		protocol.ErrCodeRateLimit, "消息发送过于频繁", requestID, messageType,
	)
	_ = c.SendMessage(response)
	cache.finish(entry, []*protocol.Message{response}, c.GetID(), requestID)
	if limiter.GetWarningCount(playerID) <= 5 {
		return false, true
	}
	log.Printf("客户端 %s 因多次超速被断开连接", c.GetName())
	return false, false
}

func (c *Client) executeCommand(msg *protocol.Message, entry *commandCacheEntry, cache *commandCache) string {
	requestID := msg.Command.RequestID
	c.beginCommandExecution(requestID, msg.Type)
	finished := false
	defer func() {
		if !finished {
			_ = c.endCommandExecution()
			cache.abort(entry)
		}
	}()

	if c.server.handler == nil {
		response := codec.NewCorrelatedCommandErrorMessage(
			protocol.ErrCodeUnknown, "服务器消息处理器不可用", requestID, msg.Type,
		)
		_ = c.SendMessage(response)
		_ = c.endCommandExecution()
		cache.finish(entry, []*protocol.Message{response}, c.GetID(), requestID)
		finished = true
		return "unavailable"
	}
	c.server.handler.Handle(c, msg)
	responses := c.endCommandExecution()
	if commandResponsesContainError(responses) {
		cache.finish(entry, responses, c.GetID(), requestID)
		finished = true
		return "error"
	}
	ack := codec.NewCommandAckMessage(requestID, msg.Type)
	_ = c.SendMessage(ack)
	responses = append(responses, ack)
	cache.finish(entry, responses, c.GetID(), requestID)
	finished = true
	return "ok"
}

func (c *Client) replayCommandResponses(responses []*protocol.Message) {
	for _, response := range responses {
		if response != nil {
			_ = c.SendMessage(response)
		}
	}
}

func (c *Client) attachLegacyChatCommand(msg *protocol.Message) bool {
	if msg.Type != protocol.MsgChat || msg.Command != nil {
		return false
	}
	payload, legacy, err := codec.ParseChatPayload(msg)
	if err != nil || !legacy {
		return false
	}
	seed := payload.MessageID
	if seed == "" {
		seed = uuid.NewString()
	}
	digest := sha256.Sum256([]byte(seed))
	msg.Command = &protocol.CommandMeta{RequestID: "legacy-chat:" + hex.EncodeToString(digest[:16])}
	return true
}

func isClientCommand(messageType protocol.MessageType) bool {
	switch messageType {
	case protocol.MsgReconnect, protocol.MsgPing,
		protocol.MsgCreateRoom, protocol.MsgJoinRoom, protocol.MsgLeaveRoom,
		protocol.MsgQuickMatch, protocol.MsgPracticeMatch, protocol.MsgCancelMatch,
		protocol.MsgReady, protocol.MsgCancelReady,
		protocol.MsgBid, protocol.MsgPlayCards, protocol.MsgPass,
		protocol.MsgGetStats, protocol.MsgGetLeaderboard, protocol.MsgGetRoomList,
		protocol.MsgGetOnlineCount, protocol.MsgGetMaintenanceStatus, protocol.MsgChat:
		return true
	default:
		return false
	}
}

// WritePump 向 WebSocket 写入消息
func (c *Client) WritePump() {
	c.initializeLifecycle()
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
		if c.conn != nil {
			_ = c.conn.Close()
		}
		c.lease.release()
	}()
	if c.conn == nil {
		return
	}

	for {
		select {
		case <-c.done:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		default:
		}

		select {
		case <-c.done:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		case message := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))

			w, err := c.conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}
			if _, err := w.Write(message); err != nil {
				_ = w.Close()
				return
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// SendMessage 发送消息给客户端
func (c *Client) SendMessage(msg *protocol.Message) error {
	c.initializeLifecycle()

	data, err := encodeClientMessage(msg)
	if err != nil {
		return err
	}
	sendErr := c.enqueueEncodedMessage(data)
	c.handleSendError(sendErr)
	return sendErr
}

// SendCommandResult correlates and records only a response explicitly emitted
// as the result of the active command. Concurrent broadcasts use SendMessage
// and therefore remain uncorrelated events.
func (c *Client) SendCommandResult(msg *protocol.Message) error {
	c.initializeLifecycle()

	outgoing := c.prepareActiveCommandResponse(msg)
	data, err := encodeClientMessage(outgoing)
	if err != nil {
		return err
	}
	sendErr := c.enqueueEncodedMessage(data)
	c.handleSendError(sendErr)
	return sendErr
}

func (c *Client) beginCommandExecution(requestID string, command protocol.MessageType) {
	c.commandMu.Lock()
	c.activeCommand = &activeCommandExecution{requestID: requestID, command: command}
	c.commandMu.Unlock()
}

func (c *Client) endCommandExecution() []*protocol.Message {
	c.commandMu.Lock()
	defer c.commandMu.Unlock()
	if c.activeCommand == nil {
		return nil
	}
	responses := cloneCommandResponses(c.activeCommand.responses)
	c.activeCommand = nil
	return responses
}

func (c *Client) prepareActiveCommandResponse(msg *protocol.Message) *protocol.Message {
	if msg == nil {
		return nil
	}
	c.commandMu.Lock()
	defer c.commandMu.Unlock()
	active := c.activeCommand
	if active == nil {
		return msg
	}
	if msg.Type == protocol.MsgError {
		payload, err := codec.ParsePayload[protocol.ErrorPayload](msg)
		if err != nil || payload.CommandType != active.command {
			return msg
		}
		correlated := codec.CorrelateError(msg, active.requestID, active.command)
		active.responses = append(active.responses, codec.CloneMessage(correlated))
		return correlated
	}
	if !isCommandResult(active.command, msg.Type) {
		return msg
	}
	correlated := codec.CloneMessage(msg)
	if correlated.Command == nil {
		correlated.Command = &protocol.CommandMeta{}
	}
	correlated.Command.RequestID = active.requestID
	active.responses = append(active.responses, codec.CloneMessage(correlated))
	return correlated
}

// SendMessageIfRoom enqueues msg only while the physical connection still has
// expectedRoom. Holding c.mu through the bounded enqueue gives SetRoom a
// single ordering boundary: either this message is queued first, or the room
// binding wins and the stale message is skipped.
func (c *Client) SendMessageIfRoom(expectedRoom string, msg *protocol.Message) (bool, error) {
	c.initializeLifecycle()

	c.mu.RLock()
	if c.RoomID != expectedRoom {
		c.mu.RUnlock()
		return false, nil
	}
	data, err := encodeClientMessage(msg)
	if err != nil {
		c.mu.RUnlock()
		return false, err
	}
	sendErr := c.enqueueEncodedMessage(data)
	c.mu.RUnlock()

	// Slow-client disconnect reads identity and may close the connection. Keep
	// it outside c.mu so a full queue cannot deadlock room rebinding.
	c.handleSendError(sendErr)
	return sendErr == nil, sendErr
}

// SendMessageIfIdentity enqueues msg only while this physical connection is
// still bound to the exact logical player and room. RebindClient uses the same
// mutex, so an old private delivery and a new identity have one deterministic
// ordering boundary.
func (c *Client) SendMessageIfIdentity(expectedPlayerID, expectedRoom string, msg *protocol.Message) (bool, error) {
	c.initializeLifecycle()

	c.mu.RLock()
	if c.ID != expectedPlayerID || c.RoomID != expectedRoom {
		c.mu.RUnlock()
		return false, nil
	}
	data, err := encodeClientMessage(msg)
	if err != nil {
		c.mu.RUnlock()
		return false, err
	}
	sendErr := c.enqueueEncodedMessage(data)
	c.mu.RUnlock()

	c.handleSendError(sendErr)
	return sendErr == nil, sendErr
}

// SendCommandResultIfIdentity is the direct-result counterpart to
// SendMessageIfIdentity.
func (c *Client) SendCommandResultIfIdentity(expectedPlayerID, expectedRoom string, msg *protocol.Message) (bool, error) {
	c.initializeLifecycle()

	c.mu.RLock()
	if c.ID != expectedPlayerID || c.RoomID != expectedRoom {
		c.mu.RUnlock()
		return false, nil
	}
	outgoing := c.prepareActiveCommandResponse(msg)
	data, err := encodeClientMessage(outgoing)
	if err != nil {
		c.mu.RUnlock()
		return false, err
	}
	sendErr := c.enqueueEncodedMessage(data)
	c.mu.RUnlock()

	c.handleSendError(sendErr)
	return sendErr == nil, sendErr
}

func commandResponsesContainError(responses []*protocol.Message) bool {
	for _, response := range responses {
		if response != nil && response.Type == protocol.MsgError {
			return true
		}
	}
	return false
}

var commandResultTypes = map[protocol.MessageType]protocol.MessageType{
	protocol.MsgReconnect:            protocol.MsgReconnected,
	protocol.MsgPing:                 protocol.MsgPong,
	protocol.MsgCreateRoom:           protocol.MsgRoomCreated,
	protocol.MsgJoinRoom:             protocol.MsgRoomJoined,
	protocol.MsgLeaveRoom:            protocol.MsgRoomLeft,
	protocol.MsgQuickMatch:           protocol.MsgMatchQueued,
	protocol.MsgPracticeMatch:        protocol.MsgMatchQueued,
	protocol.MsgCancelMatch:          protocol.MsgMatchCancelled,
	protocol.MsgReady:                protocol.MsgPlayerReady,
	protocol.MsgCancelReady:          protocol.MsgPlayerReady,
	protocol.MsgBid:                  protocol.MsgBidResult,
	protocol.MsgPlayCards:            protocol.MsgCardPlayed,
	protocol.MsgPass:                 protocol.MsgPlayerPass,
	protocol.MsgGetStats:             protocol.MsgStatsResult,
	protocol.MsgGetLeaderboard:       protocol.MsgLeaderboardResult,
	protocol.MsgGetRoomList:          protocol.MsgRoomListResult,
	protocol.MsgGetOnlineCount:       protocol.MsgOnlineCount,
	protocol.MsgGetMaintenanceStatus: protocol.MsgMaintenancePull,
	protocol.MsgChat:                 protocol.MsgChat,
}

func isCommandResult(command, response protocol.MessageType) bool {
	expected, ok := commandResultTypes[command]
	return ok && response == expected
}

func encodeClientMessage(msg *protocol.Message) ([]byte, error) {
	data, err := codec.Encode(msg)
	if err != nil {
		log.Printf("消息编码错误: %v", err)
		return nil, fmt.Errorf("encode client message: %w", err)
	}
	return data, nil
}

func (c *Client) enqueueEncodedMessage(data []byte) error {
	c.lifecycleMu.RLock()
	if c.closed.Load() {
		c.lifecycleMu.RUnlock()
		return ErrClientClosed
	}

	var sendErr error
	select {
	case <-c.done:
		sendErr = ErrClientClosed
	case c.send <- data:
		sendErr = nil
	default:
		sendErr = ErrClientSendBufferFull
	}
	c.lifecycleMu.RUnlock()
	return sendErr
}

func (c *Client) handleSendError(err error) {
	if errors.Is(err, ErrClientSendBufferFull) {
		c.disconnectSlowClient()
	}
}

func (c *Client) disconnectSlowClient() {
	c.slowCloseOnce.Do(func() {
		log.Printf("客户端 %s 发送缓冲区已满，断开慢连接", c.GetID())
		if c.server != nil {
			c.server.slowClientDisconnects.Add(1)
			if c.server.metrics != nil {
				c.server.metrics.SlowClientDisconnected()
			}
		}
		c.Close()
	})
}

// handleDisconnect 处理断开连接
func (c *Client) handleDisconnect() {
	if c.server == nil || !c.server.unregisterClient(c) {
		return
	}

	// Cancel queued or uncommitted matching before room cleanup observes the
	// client's room identity. A stale replaced connection never reaches this
	// point because unregisterClient uses compare-and-delete semantics.
	if c.server.matcher != nil {
		c.server.matcher.PlayerDisconnected(c)
	}

	playerID, _, roomID := c.identitySnapshot()

	// 标记会话为离线状态
	if c.server.sessionManager != nil {
		c.server.sessionManager.SetOffline(playerID)
	}

	// 如果在房间中，通知房间玩家掉线（但不移除）
	if roomID != "" && c.server.roomManager != nil {
		c.server.roomManager.NotifyPlayerOffline(c)
	}
}

// Close 关闭客户端连接
func (c *Client) Close() {
	c.initializeLifecycle()
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.RevokeWebSessionAuthorization()
		c.invalidateAllWebSessionTickets()
		c.lifecycleMu.Lock()
		defer c.lifecycleMu.Unlock()
		close(c.done)
	})
}

func (c *Client) initializeLifecycle() {
	c.lifecycleOnce.Do(func() {
		if c.send == nil {
			c.send = make(chan []byte, 256)
		}
		if c.done == nil {
			c.done = make(chan struct{})
		}
	})
}

func (c *Client) isClosed() bool {
	return c.closed.Load()
}

func (c *Client) IsClosed() bool {
	return c == nil || c.closed.Load()
}

// SetRoom 设置客户端所在房间
func (c *Client) SetRoom(roomID string) {
	c.mu.Lock()
	c.RoomID = roomID
	playerID := c.ID
	if c.server != nil && c.server.sessionManager != nil {
		c.server.sessionManager.SetRoom(playerID, roomID)
	}
	c.mu.Unlock()
}

// CompareAndSetRoom binds or clears room ownership without allowing identity
// rebinding to slip between validation and mutation.
func (c *Client) CompareAndSetRoom(expectedPlayerID, expectedRoom, newRoom string) bool {
	c.mu.Lock()
	if c.ID != expectedPlayerID || c.RoomID != expectedRoom {
		c.mu.Unlock()
		return false
	}
	c.RoomID = newRoom
	playerID := c.ID
	if c.server != nil && c.server.sessionManager != nil {
		c.server.sessionManager.SetRoom(playerID, newRoom)
	}
	c.mu.Unlock()
	return true
}

// GetRoom 获取客户端所在房间
func (c *Client) GetRoom() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.RoomID
}

// Interface implementations for types.ClientInterface
func (c *Client) GetID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ID
}

func (c *Client) GetName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Name
}

func (c *Client) identitySnapshot() (playerID, playerName, roomID string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ID, c.Name, c.RoomID
}

func (c *Client) rebindIdentity(playerID, playerName, roomID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ID = playerID
	c.Name = playerName
	c.RoomID = roomID
}

func (c *Client) rebindIdentityIfUnbound(expectedTemporaryID, playerID, playerName, roomID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ID != expectedTemporaryID || c.RoomID != "" {
		return false
	}
	c.ID = playerID
	c.Name = playerName
	c.RoomID = roomID
	return true
}

func (c *Client) rollbackReboundIdentity(expectedPlayerID, temporaryID, temporaryName string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ID != expectedPlayerID {
		return false
	}
	c.ID = temporaryID
	c.Name = temporaryName
	c.RoomID = ""
	return true
}

func (c *Client) IsBot() bool { return false }

func (c *Client) IsBrowserTransport() bool {
	return c != nil && c.browserTransport
}

func (c *Client) BrowserReconnectToken() string {
	if c == nil || !c.browserTransport {
		return ""
	}
	return c.browserReconnectToken
}

func (c *Client) IssueWebSessionTicket(
	token, predecessorToken string,
	rollback, orphan func() bool,
) (string, error) {
	if c == nil || !c.browserTransport || c.server == nil {
		return "", errWebSessionTicketEntropy
	}
	return c.server.activeWebSessionTickets().Issue(
		token,
		predecessorToken,
		c.browserTicketOwnerToken,
		func() bool { return c.webSessionTicketOwnerValid() },
		func() bool { return c.ConfirmWebSession(token) },
		func() bool { return c.abortPendingWebSession(rollback) },
		func() bool { return c.abortPendingWebSession(orphan) },
	)
}

func (c *Client) ConfirmWebSession(token string) bool {
	if c == nil || token == "" || c.IsClosed() || c.server == nil || c.server.sessionManager == nil {
		return false
	}
	playerID := c.GetID()
	if c.server.GetClientByID(playerID) != c || !c.server.sessionManager.CanReconnectToken(token) {
		return false
	}
	c.webSessionMu.Lock()
	if c.webSessionTransition {
		c.webSessionMu.Unlock()
		return false
	}
	defer c.webSessionMu.Unlock()
	if c.webSessionClosed || c.closed.Load() {
		return false
	}
	c.authorizedWebSessionToken = token
	return true
}

func (c *Client) RevokeWebSessionAuthorization() {
	if c == nil {
		return
	}
	c.webSessionMu.Lock()
	if c.authorizedWebSessionToken != "" {
		c.lastAuthorizedWebSessionToken = c.authorizedWebSessionToken
	}
	c.authorizedWebSessionToken = ""
	c.webSessionMu.Unlock()
}

func (c *Client) browserSessionCredentialSnapshot() string {
	if c == nil {
		return ""
	}
	c.webSessionMu.Lock()
	defer c.webSessionMu.Unlock()
	if c.authorizedWebSessionToken != "" {
		return c.authorizedWebSessionToken
	}
	return c.lastAuthorizedWebSessionToken
}

func (c *Client) beginWebSessionTransition(expectedToken string) bool {
	if c == nil || expectedToken == "" {
		return false
	}
	c.webSessionMu.Lock()
	defer c.webSessionMu.Unlock()
	if c.webSessionTransition {
		return false
	}
	if c.authorizedWebSessionToken != "" && c.authorizedWebSessionToken != expectedToken {
		return false
	}
	c.webSessionTransition = true
	return true
}

func (c *Client) endWebSessionTransition() {
	if c == nil {
		return
	}
	c.webSessionMu.Lock()
	c.webSessionTransition = false
	c.webSessionMu.Unlock()
}

func (c *Client) webSessionTicketOwnerValid() bool {
	if c == nil || c.IsClosed() || c.server == nil {
		return false
	}
	return c.server.GetClientByID(c.GetID()) == c
}

func (c *Client) abortPendingWebSession(callback func() bool) bool {
	if c.IsClosed() {
		return callback == nil || callback()
	}
	c.webCommandMu.Lock()
	defer c.webCommandMu.Unlock()
	if c.IsClosed() {
		return callback == nil || callback()
	}
	c.RevokeWebSessionAuthorization()
	result := callback == nil || callback()
	c.Close()
	return result
}

func (c *Client) beginBrowserCommandAuthority(messageType protocol.MessageType) (func(), bool) {
	if c == nil || !c.browserTransport || messageType == protocol.MsgPing || messageType == protocol.MsgReconnect {
		return func() {}, true
	}
	if c.server == nil {
		return func() {}, false
	}
	c.server.sessionAuthorityMu.RLock()
	// Never wait for a per-client writer while holding global authority. A
	// writer means this exact session is crossing an authority transition, so
	// rejecting the command is both safer and keeps unrelated authority writes
	// available.
	if !c.webCommandMu.TryRLock() {
		c.server.sessionAuthorityMu.RUnlock()
		return func() {}, false
	}
	if c.IsClosed() {
		c.webCommandMu.RUnlock()
		c.server.sessionAuthorityMu.RUnlock()
		return func() {}, false
	}
	c.webSessionMu.Lock()
	token := c.authorizedWebSessionToken
	transitioning := c.webSessionTransition
	c.webSessionMu.Unlock()
	playerID := c.GetID()
	if transitioning || token == "" || c.server.GetClientByID(playerID) != c || c.server.sessionManager == nil ||
		!c.server.sessionManager.CanReconnectToken(token) {
		c.webCommandMu.RUnlock()
		c.server.sessionAuthorityMu.RUnlock()
		return func() {}, false
	}
	// The global lock only linearizes the token/client validation against
	// commit, refresh, revoke, reconnect, and ticket expiry. The per-client read
	// lock remains held for command execution so revoking this exact session can
	// drain it without a slow command stalling the authority plane globally.
	c.server.sessionAuthorityMu.RUnlock()
	return func() {
		c.webCommandMu.RUnlock()
	}, true
}

func (c *Client) revokeAuthorizedWebSessionAndClose() {
	if c == nil {
		return
	}
	c.webCommandMu.Lock()
	defer c.webCommandMu.Unlock()
	// Even an independently closed client must cross this barrier: Close can
	// race an earlier authorized command and does not itself wait for commands.
	if c.IsClosed() {
		return
	}
	c.RevokeWebSessionAuthorization()
	c.Close()
}

func (c *Client) TrackWebSessionTicket(ticket string) bool {
	if c == nil || ticket == "" {
		return false
	}
	c.webSessionMu.Lock()
	defer c.webSessionMu.Unlock()
	if c.webSessionClosed {
		return false
	}
	if c.trackedWebSessionTickets == nil {
		c.trackedWebSessionTickets = make(map[string]struct{})
	}
	c.trackedWebSessionTickets[ticket] = struct{}{}
	return true
}

func (c *Client) InvalidateWebSessionTicket(ticket string) {
	if c == nil || ticket == "" {
		return
	}
	c.webSessionMu.Lock()
	delete(c.trackedWebSessionTickets, ticket)
	if c.provisionalWebSessionTicket == ticket {
		c.provisionalWebSessionTicket = ""
	}
	c.webSessionMu.Unlock()
	if c.server == nil {
		return
	}
	c.server.activeWebSessionTickets().Invalidate(ticket)
}

func (c *Client) setProvisionalWebSessionTicket(ticket string) bool {
	if ticket == "" {
		return false
	}
	c.webSessionMu.Lock()
	defer c.webSessionMu.Unlock()
	if c.webSessionClosed {
		return false
	}
	if c.trackedWebSessionTickets == nil {
		c.trackedWebSessionTickets = make(map[string]struct{})
	}
	c.trackedWebSessionTickets[ticket] = struct{}{}
	c.provisionalWebSessionTicket = ticket
	return true
}

func (c *Client) InvalidateProvisionalWebSessionTicket() {
	if c == nil {
		return
	}
	c.webSessionMu.Lock()
	ticket := c.provisionalWebSessionTicket
	c.provisionalWebSessionTicket = ""
	c.webSessionMu.Unlock()
	c.InvalidateWebSessionTicket(ticket)
}

func (c *Client) DiscardProvisionalWebSessionTicket() {
	if c == nil {
		return
	}
	c.webSessionMu.Lock()
	ticket := c.provisionalWebSessionTicket
	c.provisionalWebSessionTicket = ""
	delete(c.trackedWebSessionTickets, ticket)
	c.webSessionMu.Unlock()
	if c.server != nil {
		c.server.activeWebSessionTickets().Discard(ticket)
	}
}

func (c *Client) invalidateAllWebSessionTickets() {
	if c == nil {
		return
	}
	c.webSessionMu.Lock()
	c.webSessionClosed = true
	tickets := make([]string, 0, len(c.trackedWebSessionTickets)+1)
	for ticket := range c.trackedWebSessionTickets {
		tickets = append(tickets, ticket)
	}
	if c.provisionalWebSessionTicket != "" {
		if _, tracked := c.trackedWebSessionTickets[c.provisionalWebSessionTicket]; !tracked {
			tickets = append(tickets, c.provisionalWebSessionTicket)
		}
	}
	c.trackedWebSessionTickets = nil
	c.provisionalWebSessionTicket = ""
	c.webSessionMu.Unlock()

	if c.server == nil {
		return
	}
	manager := c.server.activeWebSessionTickets()
	for _, ticket := range tickets {
		manager.Invalidate(ticket)
	}
}
