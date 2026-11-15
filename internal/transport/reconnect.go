package transport

import (
	"errors"
	"log"
	"time"

	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/logger"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

// Reconnect 手动发送重连请求
func (c *Client) Reconnect() error {
	playerID, _, token := c.Credentials()
	if token == "" || playerID == "" {
		return errors.New("no reconnect token")
	}
	return c.SendMessage(codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		Token:    token,
		PlayerID: playerID,
	}))
}

// StartHeartbeat 启动心跳检测
func (c *Client) StartHeartbeat() {
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if c.IsConnected() {
					_ = c.Ping()
				}
			case <-c.done:
				return
			}
		}
	}()
}

func (c *Client) sleepOrDone(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-c.done:
		return false
	}
}

func reconnectBackoffDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = reconnectInterval
	}
	backoff := base
	for i := 1; i < attempt; i++ {
		backoff *= 2
		if backoff > 30*time.Second {
			return 30 * time.Second
		}
	}
	return backoff
}

func (c *Client) nextReconnectAttempt() (attempt int, stop, exhausted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, true, false
	}
	if c.reconnectCount >= maxReconnectAttempts {
		return 0, false, true
	}
	c.reconnectCount++
	return c.reconnectCount, false, false
}

func (c *Client) dialReconnectAttempt() (done bool) {
	dialer := websocket.Dialer{
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: false,
	}
	conn, resp, err := dialer.Dial(c.ServerURL, nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return false
	}

	send := make(chan []byte, 256)
	connDone := make(chan struct{})

	// 安装新连接代次（保留 c.receive 与 c.done 不变）
	c.mu.Lock()
	if c.closed || c.replaced.Load() {
		c.mu.Unlock()
		_ = conn.Close()
		c.reconnecting.Store(false)
		return true
	}
	c.conn = conn
	c.send = send
	c.mu.Unlock()

	// 启动读写协程（绑定此代 conn）
	go c.readPump(conn, connDone)
	go c.writePump(conn, send, connDone)

	// 发送重连请求
	if !c.sleepOrDone(100 * time.Millisecond) {
		_ = conn.Close()
		c.reconnecting.Store(false)
		return true
	}
	if err := c.Reconnect(); err != nil {
		_ = conn.Close()
		return false
	}
	return true
}

// tryReconnect 尝试重连
func (c *Client) tryReconnect() {
	if !c.reconnectLoop.CompareAndSwap(false, true) {
		return
	}
	defer func() {
		c.reconnectLoop.Store(false)
		if r := recover(); r != nil {
			logger.LogPanic(r)
			log.Printf("[PANIC] tryReconnect panic recovered: %v", r)
			c.reconnecting.Store(false)
		}
	}()

	if c.IsClosed() {
		return
	}
	c.reconnecting.Store(true)

	for {
		attempt, stop, exhausted := c.nextReconnectAttempt()
		if stop {
			c.reconnecting.Store(false)
			return
		}
		if exhausted {
			break
		}

		if c.OnReconnecting != nil {
			c.OnReconnecting(attempt, maxReconnectAttempts)
		}

		if !c.sleepOrDone(reconnectBackoffDelay(c.reconnectBackoff, attempt)) {
			c.reconnecting.Store(false)
			return
		}
		if c.IsClosed() || c.replaced.Load() {
			c.reconnecting.Store(false)
			return
		}
		if c.dialReconnectAttempt() {
			return
		}
	}

	// 重连失败
	c.reconnecting.Store(false)
	if c.IsClosed() {
		return
	}
	c.Close()
	if c.OnClose != nil {
		c.OnClose()
	}
}

// GetLatency 获取当前延迟（毫秒）
func (c *Client) GetLatency() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Latency
}

// IsReconnecting 是否正在重连
func (c *Client) IsReconnecting() bool {
	return c.reconnecting.Load()
}
