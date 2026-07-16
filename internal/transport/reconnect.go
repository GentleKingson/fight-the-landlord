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
	playerID, _, reconnectToken := c.Identity()
	if reconnectToken == "" || playerID == "" {
		return errors.New("no reconnect token")
	}
	return c.SendMessage(codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
		Token:    reconnectToken,
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

// tryReconnect 尝试重连
func (c *Client) tryReconnect(playerID, reconnectToken string) {
	defer func() {
		if r := recover(); r != nil {
			logger.LogPanic(r)
			log.Printf("[PANIC] tryReconnect panic recovered: %v", r)
			c.reconnecting.Store(false)
		}
	}()

	// 指数退避重连策略
	backoff := reconnectInterval

	for {
		c.mu.Lock()
		if c.closed || c.reconnectCount >= maxReconnectAttempts {
			c.mu.Unlock()
			break
		}
		c.reconnectCount++
		attempt := c.reconnectCount
		c.mu.Unlock()
		// 通过回调通知 UI 正在重连
		if c.OnReconnecting != nil {
			c.OnReconnecting(attempt, maxReconnectAttempts)
		}

		time.Sleep(backoff)

		// 计算下一次退避时间 (最大 30 秒)
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}

		// 创建新连接
		dialer := websocket.Dialer{
			HandshakeTimeout:  10 * time.Second,
			EnableCompression: false,
		}

		conn, resp, err := dialer.Dial(c.ServerURL, nil)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			continue
		}
		if err := c.negotiate(conn); err != nil {
			_ = conn.Close()
			continue
		}

		// Send the restore command before the pumps can observe and publish the
		// server's provisional Connected identity. Both values come from the
		// disconnected connection and cannot be replaced by mutable fields.
		reconnect := codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{
			Token:    reconnectToken,
			PlayerID: playerID,
		})
		c.prepareOutgoingMessage(reconnect)
		data, err := codec.Encode(reconnect)
		if err != nil {
			_ = conn.Close()
			continue
		}
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
			_ = conn.Close()
			continue
		}
		_ = conn.SetWriteDeadline(time.Time{})

		// 重置状态
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			_ = conn.Close()
			return
		}
		c.beginReconnectAttemptLocked(reconnect.Command.RequestID)
		c.conn = conn
		c.send = make(chan []byte, 256)
		send := c.send
		c.mu.Unlock()

		// 启动读写协程
		go c.readPump(conn)
		go c.writePump(conn, send)

		// 重连成功（通过 MsgReconnected 消息通知 UI）
		return
	}

	// 重连失败
	c.reconnecting.Store(false)
	c.Close()
	c.notifyClosed()
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

func (c *Client) beginReconnectAttemptLocked(requestID string) {
	c.reconnectRequestID = requestID
	c.provisionalConnected = nil
}

func (c *Client) clearReconnectAttemptLocked() {
	c.reconnectRequestID = ""
	c.provisionalConnected = nil
}

// stageProvisionalConnected keeps the new physical connection's identity out
// of the UI until the restore command either succeeds or is explicitly
// rejected.
func (c *Client) stageProvisionalConnected(msg *protocol.Message) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.reconnecting.Load() {
		return false
	}
	c.provisionalConnected = codec.CloneMessage(msg)
	return true
}

// resolveReconnectError returns the provisional Connected message only when
// the Error is correlated to the in-flight restore command. The caller then
// publishes that identity before the rejection so the client remains usable
// as the newly-created session instead of retaining an expired credential.
func (c *Client) resolveReconnectError(msg *protocol.Message) *protocol.Message {
	if msg == nil || msg.Command == nil || msg.Command.RequestID == "" {
		return nil
	}
	requestID := msg.Command.RequestID
	payload, err := codec.ParsePayload[protocol.ErrorPayload](msg)
	if err != nil || (payload.RequestID != "" && payload.RequestID != requestID) {
		return nil
	}
	if payload.CommandType != "" && payload.CommandType != protocol.MsgReconnect {
		return nil
	}

	c.mu.Lock()
	if !c.reconnecting.Load() || c.reconnectRequestID != requestID {
		c.mu.Unlock()
		return nil
	}
	provisional := c.provisionalConnected
	if provisional != nil {
		c.clearReconnectAttemptLocked()
		c.reconnectCount = 0
		c.reconnecting.Store(false)
		c.mu.Unlock()
		return provisional
	}
	// Connected must precede the reconnect response on an ordered WebSocket.
	// If it did not, treat the physical connection as unusable and let
	// handleReadExit retry without losing the captured restore identity.
	conn := c.conn
	c.clearReconnectAttemptLocked()
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	return nil
}
