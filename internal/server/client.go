package server

import (
	"log"
	"math/rand/v2"
	"sync"
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

	server *Server
	conn   *websocket.Conn
	send   chan []byte

	mu     sync.RWMutex
	closed bool
}

// NewClient 创建新客户端
func NewClient(s *Server, conn *websocket.Conn) *Client {
	return &Client{
		ID:     uuid.New().String(),
		Name:   GenerateNickname(),
		server: s,
		conn:   conn,
		send:   make(chan []byte, 256),
	}
}

// ReadPump 从 WebSocket 读取消息
func (c *Client) ReadPump() {
	defer func() {
		c.handleDisconnect()
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("读取错误: %v", err)
			}
			break
		}

		// 消息速率限制检查
		allowed, warning := c.server.messageLimiter.AllowMessage(c.ID)
		if !allowed {
			log.Printf("⚠️ 客户端 %s (IP: %s) 消息过于频繁", c.Name, c.IP)
			c.SendMessage(codec.NewErrorMessageWithText(protocol.ErrCodeRateLimit, "消息发送过于频繁"))
			// 如果警告次数过多，断开连接
			if c.server.messageLimiter.GetWarningCount(c.ID) > 5 {
				log.Printf("🚫 客户端 %s 因多次超速被断开连接", c.Name)
				break
			}
			continue
		}
		if warning {
			c.SendMessage(codec.NewErrorMessageWithText(protocol.ErrCodeRateLimit, "请求过于频繁，请放慢速度"))
		}

		// 解析消息
		msg, err := codec.Decode(message)
		if err != nil {
			log.Printf("消息解析错误: %v", err)
			c.SendMessage(codec.NewErrorMessage(protocol.ErrCodeInvalidMsg))
			continue
		}

		// 交给处理器处理，处理完后归还到池
		c.server.handler.Handle(c, msg)
		codec.PutMessage(msg)
	}
}

// WritePump 向 WebSocket 写入消息
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// 通道已关闭
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}
			_, _ = w.Write(message)

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
func (c *Client) SendMessage(msg *protocol.Message) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return
	}
	c.mu.RUnlock()

	data, err := codec.Encode(msg)
	if err != nil {
		log.Printf("消息编码错误: %v", err)
		return
	}

	select {
	case c.send <- data:
	default:
		// 发送缓冲区已满，关闭连接
		log.Printf("客户端 %s 发送缓冲区已满", c.ID)
		c.Close()
	}
}

// handleDisconnect 处理断开连接
func (c *Client) handleDisconnect() {
	// 标记会话为离线状态
	c.server.sessionManager.SetOffline(c.ID)

	// 如果在房间中，通知房间玩家掉线（但不移除）
	if c.RoomID != "" {
		c.server.roomManager.NotifyPlayerOffline(c)
	}

	// 如果在匹配队列中，移除
	c.server.matcher.RemoveFromQueue(c)

	// 从服务器注销连接（但保留会话）
	c.server.unregisterClient(c)
}

// Close 关闭客户端连接
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		c.closed = true
		close(c.send)
	}
}

// SetRoom 设置客户端所在房间
func (c *Client) SetRoom(roomID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.RoomID = roomID
}

// GetRoom 获取客户端所在房间
func (c *Client) GetRoom() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.RoomID
}

// Interface implementations for types.ClientInterface
func (c *Client) GetID() string   { return c.ID }
func (c *Client) GetName() string { return c.Name }
func (c *Client) IsBot() bool     { return false }
