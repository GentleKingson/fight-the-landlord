package transport

import (
	"log"
	"time"

	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/logger"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	payloadconv "github.com/palemoky/fight-the-landlord/internal/protocol/convert/payload"
)

// readPump 从服务器读取消息（绑定单代 conn）
func (c *Client) readPump(conn *websocket.Conn, connDone chan struct{}) {
	defer c.handleReadExit(conn, connDone)

	c.setupPongHandler(conn)

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			c.handleReadError(err)
			return
		}

		msg, err := codec.Decode(message)
		if err != nil {
			log.Printf("消息解析错误: %v", err)
			continue
		}

		c.processMessage(msg)
	}
}

func (c *Client) handleReadExit(conn *websocket.Conn, connDone chan struct{}) {
	close(connDone)
	if r := recover(); r != nil {
		logger.LogPanic(r)
		log.Printf("[PANIC] readPump panic recovered: %v", r)
	}

	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
		c.send = nil
	}
	alreadyClosed := c.closed
	hasToken := c.ReconnectToken != ""
	canRetry := c.reconnectCount < maxReconnectAttempts
	c.mu.Unlock()

	if alreadyClosed {
		return
	}

	// 尝试重连
	if hasToken && canRetry && !c.reconnectLoop.Load() && !c.replaced.Load() {
		go c.tryReconnect()
	} else if !c.reconnectLoop.Load() {
		c.Close()
		if c.OnClose != nil {
			c.OnClose()
		}
	}
}

func (c *Client) setupPongHandler(conn *websocket.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
}

func (c *Client) handleReadError(err error) {
	if websocket.IsCloseError(err, 4001) {
		c.replaced.Store(true)
	}
	if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
		if c.OnError != nil {
			c.OnError(err)
		}
	}
}

func (c *Client) handlePendingIdentityError(msg *protocol.Message) {
	c.mu.Lock()
	pending := c.pendingIdentity
	c.mu.Unlock()
	if pending == nil {
		return
	}
	payload, err := codec.ParsePayload[protocol.ErrorPayload](msg)
	if err != nil {
		return
	}
	switch payload.Code {
	case protocol.ErrCodeIdentityInvalid:
		c.mu.Lock()
		c.pendingIdentity = nil
		c.reconnectCount = 0
		c.mu.Unlock()
		c.reconnecting.Store(false)
		c.processMessage(pending)
	case protocol.ErrCodeIdentityUnavailable:
		// The server closes this temporary socket; retry with the original credential.
		c.mu.Lock()
		c.pendingIdentity = nil
		c.mu.Unlock()
		c.reconnecting.Store(false)
	}
}

func (c *Client) processMessage(msg *protocol.Message) {
	// A fresh terminal socket receives a temporary identity before Reconnect.
	// Do not overwrite the credential that the reconnect request still needs.
	if c.reconnecting.Load() && msg.Type == protocol.MsgConnected {
		c.mu.Lock()
		c.pendingIdentity = msg
		c.mu.Unlock()
		return
	}
	if c.reconnecting.Load() && msg.Type == protocol.MsgError {
		c.handlePendingIdentityError(msg)
	}
	if msg.Type == protocol.MsgReconnected {
		c.mu.Lock()
		c.pendingIdentity = nil
		c.mu.Unlock()
	}
	isReconnected := c.handleInternalMessage(msg)

	// 回调处理
	if c.OnMessage != nil {
		c.OnMessage(msg)
	}

	// 同时发送到持久 channel
	select {
	case c.receive <- msg:
	case <-c.done:
		return
	default:
	}

	// 重连成功回调放在最后，确保消息已经发送到 channel
	if isReconnected && c.OnReconnect != nil {
		c.OnReconnect()
	}
}

func (c *Client) handleInternalMessage(msg *protocol.Message) bool {
	switch msg.Type {
	case protocol.MsgConnected:
		var payload protocol.ConnectedPayload
		if err := payloadconv.DecodePayload(msg.Type, msg.Payload, &payload); err == nil {
			c.mu.Lock()
			c.PlayerID = payload.PlayerID
			c.PlayerName = payload.PlayerName
			c.ReconnectToken = payload.ReconnectToken
			c.mu.Unlock()
		}
	case protocol.MsgReconnected:
		c.mu.Lock()
		c.reconnectCount = 0
		c.mu.Unlock()
		c.reconnecting.Store(false)
		return true
	case protocol.MsgPong:
		var payload protocol.PongPayload
		if err := payloadconv.DecodePayload(msg.Type, msg.Payload, &payload); err == nil {
			latency := time.Now().UnixMilli() - payload.ClientTimestamp
			c.mu.Lock()
			c.Latency = latency
			c.mu.Unlock()
			if c.OnLatencyUpdate != nil {
				c.OnLatencyUpdate(latency)
			}
		}
	}
	return false
}

// writePump 向服务器写入消息（绑定单代 conn 与 send）
func (c *Client) writePump(conn *websocket.Conn, send <-chan []byte, connDone <-chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		if r := recover(); r != nil {
			logger.LogPanic(r)
			log.Printf("[PANIC] writePump panic recovered: %v", r)
		}
		ticker.Stop()
		_ = conn.Close()
	}()

	for {
		select {
		case message, ok := <-send:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-connDone:
			return

		case <-c.done:
			return
		}
	}
}
