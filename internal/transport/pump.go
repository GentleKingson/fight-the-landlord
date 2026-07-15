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

// readPump 从服务器读取消息
func (c *Client) readPump(conn *websocket.Conn) {
	defer c.handleReadExit(conn)

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

func (c *Client) handleReadExit(conn *websocket.Conn) {
	if r := recover(); r != nil {
		logger.LogPanic(r)
		log.Printf("[PANIC] readPump panic recovered: %v", r)
	}
	_ = conn.Close()

	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}
	c.conn = nil
	closed := c.closed
	playerID := c.PlayerID
	reconnectToken := c.ReconnectToken
	wasReconnecting := c.reconnecting.Load()
	if wasReconnecting {
		c.clearReconnectAttemptLocked()
	}
	c.mu.Unlock()
	if closed {
		return
	}
	// 尝试重连
	if reconnectToken != "" {
		if wasReconnecting || c.reconnecting.CompareAndSwap(false, true) {
			go c.tryReconnect(playerID, reconnectToken)
			return
		}
	}
	c.Close()
	c.notifyClosed()
}

func (c *Client) setupPongHandler(conn *websocket.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
}

func (c *Client) handleReadError(err error) {
	if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
		if c.OnError != nil {
			c.OnError(err)
		}
	}
}

func (c *Client) processMessage(msg *protocol.Message) {
	isReconnected, suppress := c.handleInternalMessage(msg)
	if suppress {
		codec.PutMessage(msg)
		return
	}

	// 回调处理
	if c.OnMessage != nil {
		c.OnMessage(msg)
	}

	// 同时发送到 channel
	select {
	case c.receive <- msg:
	default:
	}

	// 重连成功回调放在最后，确保消息已经发送到 channel
	if isReconnected && c.OnReconnect != nil {
		c.OnReconnect()
	}
}

func (c *Client) handleInternalMessage(msg *protocol.Message) (bool, bool) {
	if msg.Event != nil && msg.Event.GameID != "" {
		c.setGameContext(msg.Event.GameID, msg.Event.TurnID)
	}
	switch msg.Type {
	case protocol.MsgConnected:
		var payload protocol.ConnectedPayload
		if err := payloadconv.DecodePayload(msg.Type, msg.Payload, &payload); err != nil {
			break
		}
		// Every restored physical connection first receives a provisional
		// identity. It must not replace or leak ahead of the identity being
		// restored by MsgReconnected.
		if c.stageProvisionalConnected(msg) {
			return false, true
		}
		c.setIdentity(payload.PlayerID, payload.PlayerName, payload.ReconnectToken)
		c.setGameContext("", 0)
	case protocol.MsgReconnected:
		var payload protocol.ReconnectedPayload
		if err := payloadconv.DecodePayload(msg.Type, msg.Payload, &payload); err == nil {
			c.setIdentity(payload.PlayerID, payload.PlayerName, payload.ReconnectToken)
			if payload.GameState != nil {
				c.setGameContext(payload.GameState.GameID, payload.GameState.TurnID)
			} else {
				c.setGameContext("", 0)
			}
		}
		c.mu.Lock()
		c.clearReconnectAttemptLocked()
		c.reconnectCount = 0
		c.reconnecting.Store(false)
		c.mu.Unlock()
		return true, false
	case protocol.MsgError:
		if provisional := c.resolveReconnectError(msg); provisional != nil {
			c.processMessage(provisional)
			// The old identity could not be restored, but the physical
			// connection is usable with the provisional session. Notify the UI
			// so it leaves its reconnecting state after the Error is delivered.
			return true, false
		}
	case protocol.MsgRoomLeft:
		c.setGameContext("", 0)
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
	return false, false
}

// writePump 向服务器写入消息
func (c *Client) writePump(conn *websocket.Conn, send <-chan []byte) {
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

		case <-c.done:
			return
		}
	}
}

func (c *Client) notifyClosed() {
	c.closeNotifyOnce.Do(func() {
		if c.OnClose != nil {
			c.OnClose()
		}
	})
}
