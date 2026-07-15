package transport

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

const (
	writeWait     = 10 * time.Second
	pongWait      = 60 * time.Second
	pingPeriod    = (pongWait * 9) / 10
	handshakeWait = 10 * time.Second

	heartbeatInterval    = 5 * time.Second // 心跳检测间隔
	maxReconnectAttempts = 5               // 最大重连次数
	reconnectInterval    = 2 * time.Second // 重连间隔
)

const defaultClientVersion = "dev"

// ProtocolRejectedError reports a server-side protocol negotiation refusal.
type ProtocolRejectedError struct {
	Reason                   string
	SupportedProtocolVersion string
	MinClientVersion         string
}

func (e *ProtocolRejectedError) Error() string {
	if e == nil {
		return "protocol rejected"
	}
	if e.MinClientVersion != "" {
		return fmt.Sprintf("protocol rejected: %s (minimum client version %s)", e.Reason, e.MinClientVersion)
	}
	return fmt.Sprintf("protocol rejected: %s", e.Reason)
}

// Client WebSocket 客户端
type Client struct {
	ServerURL     string
	clientVersion string
	conn          *websocket.Conn
	send          chan []byte
	receive       chan *protocol.Message
	done          chan struct{}

	PlayerID       string
	PlayerName     string
	ReconnectToken string // 重连令牌

	// 网络延迟（毫秒）
	Latency int64

	// 回调
	OnMessage       func(*protocol.Message)     // 消息回调
	OnError         func(error)                 // 错误回调
	OnClose         func()                      // 关闭回调
	OnReconnecting  func(attempt, maxTries int) // 正在重连回调
	OnReconnect     func()                      // 重连成功回调
	OnLatencyUpdate func(int64)                 // 延迟更新回调

	mu                   sync.RWMutex
	closed               bool
	currentGameID        string
	currentTurnID        int64
	reconnecting         atomic.Bool
	reconnectCount       int
	reconnectRequestID   string
	provisionalConnected *protocol.Message
	closeNotifyOnce      sync.Once
}

// NewClient 创建客户端
func NewClient(serverURL string, clientVersions ...string) *Client {
	clientVersion := defaultClientVersion
	if len(clientVersions) > 0 && strings.TrimSpace(clientVersions[0]) != "" {
		clientVersion = strings.TrimSpace(clientVersions[0])
	}
	return &Client{
		ServerURL:     serverURL,
		clientVersion: clientVersion,
		send:          make(chan []byte, 256),
		receive:       make(chan *protocol.Message, 256),
		done:          make(chan struct{}),
	}
}

// Connect 连接服务器
func (c *Client) Connect() error {
	dialer := websocket.Dialer{
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: false,
	}

	conn, resp, err := dialer.Dial(c.ServerURL, nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return err
	}
	if err := c.negotiate(conn); err != nil {
		_ = conn.Close()
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return errors.New("connection closed")
	}
	c.conn = conn
	send := c.send
	c.mu.Unlock()

	// 启动读写协程
	go c.readPump(conn)
	go c.writePump(conn, send)

	return nil
}

func (c *Client) negotiate(conn *websocket.Conn) error {
	hello := codec.MustNewMessage(protocol.MsgHello, protocol.HelloPayload{
		ProtocolVersion: protocol.ProtocolVersion,
		ClientVersion:   c.clientVersion,
		Capabilities:    append([]string(nil), protocol.RequiredCapabilities...),
		ClientKind:      protocol.ClientKindTUI,
	})
	c.prepareOutgoingMessage(hello)
	requestID := hello.Command.RequestID
	data, err := codec.Encode(hello)
	if err != nil {
		return fmt.Errorf("encode protocol hello: %w", err)
	}

	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		return fmt.Errorf("write protocol hello: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(handshakeWait))
	frameType, frame, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read protocol negotiation: %w", err)
	}
	if frameType != websocket.BinaryMessage {
		return fmt.Errorf("protocol negotiation used websocket frame type %d", frameType)
	}
	response, err := codec.Decode(frame)
	if err != nil {
		return fmt.Errorf("decode protocol negotiation: %w", err)
	}
	defer codec.PutMessage(response)

	responseRequestID := ""
	if response.Command != nil {
		responseRequestID = response.Command.RequestID
	}
	switch response.Type {
	case protocol.MsgProtocolRejected:
		if responseRequestID != requestID {
			return fmt.Errorf("protocol rejection request_id mismatch")
		}
		payload, parseErr := codec.ParsePayload[protocol.ProtocolRejectedPayload](response)
		if parseErr != nil {
			return fmt.Errorf("decode protocol rejection: %w", parseErr)
		}
		if payload.RequestID != requestID {
			return fmt.Errorf("protocol rejection payload request_id mismatch")
		}
		return &ProtocolRejectedError{
			Reason:                   payload.Reason,
			SupportedProtocolVersion: payload.SupportedProtocolVersion,
			MinClientVersion:         payload.MinClientVersion,
		}
	case protocol.MsgNegotiated:
		if responseRequestID != requestID {
			return fmt.Errorf("protocol negotiation request_id mismatch")
		}
		payload, parseErr := codec.ParsePayload[protocol.NegotiatedPayload](response)
		if parseErr != nil {
			return fmt.Errorf("decode protocol negotiation payload: %w", parseErr)
		}
		if payload.ProtocolVersion != protocol.ProtocolVersion {
			return fmt.Errorf("server negotiated protocol %q, want %q", payload.ProtocolVersion, protocol.ProtocolVersion)
		}
		if payload.ClientKind != protocol.ClientKindTUI {
			return fmt.Errorf("server negotiated client kind %q, want %q", payload.ClientKind, protocol.ClientKindTUI)
		}
		for _, capability := range protocol.RequiredCapabilities {
			if !slices.Contains(payload.Capabilities, capability) {
				return fmt.Errorf("server negotiation omitted capability %q", capability)
			}
		}
	default:
		return fmt.Errorf("unexpected protocol negotiation response %q", response.Type)
	}

	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	return nil
}

// SendMessage 发送消息
func (c *Client) SendMessage(msg *protocol.Message) error {
	if msg == nil {
		return errors.New("nil message")
	}
	c.mu.RLock()
	if c.closed || c.conn == nil {
		c.mu.RUnlock()
		return errors.New("connection closed")
	}
	send := c.send
	c.mu.RUnlock()

	c.prepareOutgoingMessage(msg)
	data, err := codec.Encode(msg)
	if err != nil {
		return err
	}

	select {
	case send <- data:
		return nil
	default:
		return errors.New("send buffer full")
	}
}

func (c *Client) prepareOutgoingMessage(msg *protocol.Message) {
	if msg.Command == nil {
		msg.Command = &protocol.CommandMeta{}
	}
	if msg.Command.RequestID == "" {
		msg.Command.RequestID = uuid.NewString()
	}
	if msg.Type != protocol.MsgBid && msg.Type != protocol.MsgPlayCards && msg.Type != protocol.MsgPass {
		return
	}
	c.mu.RLock()
	gameID, turnID := c.currentGameID, c.currentTurnID
	c.mu.RUnlock()
	if msg.Command.ExpectedGameID == "" {
		msg.Command.ExpectedGameID = gameID
	}
	if msg.Command.ExpectedTurnID == 0 {
		msg.Command.ExpectedTurnID = turnID
	}
}

// Receive 接收消息 (阻塞)
func (c *Client) Receive() (*protocol.Message, error) {
	select {
	case msg := <-c.receive:
		return msg, nil
	case <-c.done:
		return nil, errors.New("connection closed")
	}
}

// ReceiveWithTimeout 带超时接收消息
func (c *Client) ReceiveWithTimeout(timeout time.Duration) (*protocol.Message, error) {
	select {
	case msg := <-c.receive:
		return msg, nil
	case <-time.After(timeout):
		return nil, errors.New("receive timeout")
	case <-c.done:
		return nil, errors.New("connection closed")
	}
}

// Close 关闭连接
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	conn := c.conn
	c.conn = nil
	close(c.done)
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// IsConnected 是否已连接
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && c.conn != nil
}

// Identity returns one synchronized snapshot of the reconnectable identity.
func (c *Client) Identity() (playerID, playerName, reconnectToken string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PlayerID, c.PlayerName, c.ReconnectToken
}

func (c *Client) setIdentity(playerID, playerName, reconnectToken string) {
	c.mu.Lock()
	c.PlayerID = playerID
	c.PlayerName = playerName
	if reconnectToken != "" {
		c.ReconnectToken = reconnectToken
	}
	c.mu.Unlock()
}

func (c *Client) setGameContext(gameID string, turnID int64) {
	c.mu.Lock()
	c.currentGameID = gameID
	c.currentTurnID = turnID
	c.mu.Unlock()
}
