package server

import (
	"log"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// GetOnlineCount 获取在线人数（按需调用）
func (s *Server) GetOnlineCount() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

// BroadcastToLobbyFrom publishes a chat-like command result to its sender and
// the same payload as an uncorrelated event to every other lobby client.
func (s *Server) BroadcastToLobbyFrom(sender types.ClientInterface, msg *protocol.Message) {
	for _, client := range s.snapshotClients() {
		if client.GetRoom() != "" {
			continue
		}
		var err error
		if sender != nil && client == sender {
			err = client.SendCommandResult(msg)
		} else {
			err = client.SendMessage(msg)
		}
		if err != nil {
			log.Printf("广播大厅消息 %s 给玩家 %s 失败: %v", msg.Type, client.GetID(), err)
		}
	}
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
