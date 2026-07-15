package server

import (
	"log"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

// GetOnlineCount 获取在线人数（按需调用）
func (s *Server) GetOnlineCount() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

// Broadcast 广播消息给所有客户端
func (s *Server) Broadcast(msg *protocol.Message) {
	for _, client := range s.snapshotClients() {
		if err := client.SendMessage(msg); err != nil {
			log.Printf("广播消息 %s 给玩家 %s 失败: %v", msg.Type, client.GetID(), err)
		}
	}
}

// BroadcastToLobby 广播消息给大厅玩家（未在房间内的玩家）
func (s *Server) BroadcastToLobby(msg *protocol.Message) {
	for _, client := range s.snapshotClients() {
		if client.GetRoom() != "" {
			continue
		}
		if err := client.SendMessage(msg); err != nil {
			log.Printf("广播大厅消息 %s 给玩家 %s 失败: %v", msg.Type, client.GetID(), err)
		}
	}
}

func (s *Server) snapshotClients() []*Client {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	clients := make([]*Client, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, client)
	}
	return clients
}
