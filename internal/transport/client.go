package transport

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10

	heartbeatInterval    = 5 * time.Second // 心跳检测间隔
	maxReconnectAttempts = 5               // 最大重连次数
	reconnectInterval    = 2 * time.Second // 重连间隔
)

// Client WebSocket 客户端
type Client struct {
	ServerURL string
	conn      *websocket.Conn
	send      chan []byte
	receive   chan *protocol.Message
	done      chan struct{}

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

	mu               sync.RWMutex
	closeOnce        sync.Once
	closed           bool
	reconnecting     atomic.Bool
	reconnectLoop    atomic.Bool
	replaced         atomic.Bool
	pendingIdentity  *protocol.Message
	reconnectCount   int
	reconnectBackoff time.Duration
}

// NewClient 创建客户端
func NewClient(serverURL string) *Client {
	return &Client{
		ServerURL:        serverURL,
		receive:          make(chan *protocol.Message, 256),
		done:             make(chan struct{}),
		reconnectBackoff: reconnectInterval,
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

	send := make(chan []byte, 256)
	connDone := make(chan struct{})

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return errors.New("connection closed")
	}
	c.conn = conn
	c.send = send
	c.mu.Unlock()

	// 启动读写协程（按连接代次隔离）
	go c.readPump(conn, connDone)
	go c.writePump(conn, send, connDone)

	return nil
}

// SendMessage 发送消息
func (c *Client) SendMessage(msg *protocol.Message) error {
	data, err := codec.Encode(msg)
	if err != nil {
		return err
	}

	c.mu.RLock()
	closed := c.closed
	conn := c.conn
	send := c.send
	c.mu.RUnlock()

	if closed || conn == nil || send == nil {
		return errors.New("connection closed")
	}

	select {
	case send <- data:
		return nil
	default:
		return errors.New("send buffer full")
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
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case msg := <-c.receive:
		return msg, nil
	case <-timer.C:
		return nil, errors.New("receive timeout")
	case <-c.done:
		return nil, errors.New("connection closed")
	}
}

// Close 关闭连接并终止重连/心跳协程
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.send = nil
	c.mu.Unlock()

	c.reconnecting.Store(false)
	c.closeOnce.Do(func() {
		close(c.done)
	})
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

// IsClosed 客户端是否已彻底关闭
func (c *Client) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// Credentials 并发安全地读取玩家身份和重连令牌
func (c *Client) Credentials() (playerID, playerName, reconnectToken string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PlayerID, c.PlayerName, c.ReconnectToken
}

// SetCredentials 并发安全地更新玩家身份和重连令牌
func (c *Client) SetCredentials(playerID, playerName, reconnectToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.PlayerID = playerID
	c.PlayerName = playerName
	c.ReconnectToken = reconnectToken
}
