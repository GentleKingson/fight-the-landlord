package types

import (
	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

// ServerInterface 定义服务器接口（用于打破循环依赖）
type ServerInterface interface {
	IsMaintenanceMode() bool
	GetOnlineCount() int
	BroadcastToLobby(msg *protocol.Message)
	GetClientByID(id string) ClientInterface
	RegisterClient(id string, client ClientInterface)
	UnregisterClient(id string, client ClientInterface) bool
	RebindClient(temporaryID, playerID, playerName, roomCode string, client ClientInterface) (ClientInterface, error)
}

// ClientInterface 定义客户端接口
type ClientInterface interface {
	GetID() string
	GetName() string
	GetRoom() string
	SetRoom(code string)
	SendMessage(msg *protocol.Message) error
	Close()
	IsBot() bool
}

// RoomConditionalSender is an optional capability for atomically ordering a
// room-scoped delivery with room identity changes on a physical connection.
// A false result with a nil error means the expected room no longer matched.
type RoomConditionalSender interface {
	SendMessageIfRoom(expectedRoom string, msg *protocol.Message) (sent bool, err error)
}

// IdentityConditionalSender is the stronger producer capability used for
// asynchronous deliveries. It validates both the logical player and room on
// the physical connection under one lock, so reconnect identity rebinding
// cannot redirect a stale player's private or lifecycle message.
type IdentityConditionalSender interface {
	SendMessageIfIdentity(expectedPlayerID, expectedRoom string, msg *protocol.Message) (sent bool, err error)
}

// IdentityConditionalRoomBinder atomically changes a physical connection's
// room only while both its logical player and current room still match. It is
// the membership counterpart to IdentityConditionalSender.
type IdentityConditionalRoomBinder interface {
	CompareAndSetRoom(expectedPlayerID, expectedRoom, newRoom string) bool
}

// CompareAndSetRoom uses the atomic production capability when available.
// The fallback preserves compatibility for bots and lightweight test clients
// whose identities are immutable by construction.
func CompareAndSetRoom(client ClientInterface, expectedPlayerID, expectedRoom, newRoom string) bool {
	if client == nil {
		return false
	}
	if binder, ok := client.(IdentityConditionalRoomBinder); ok {
		return binder.CompareAndSetRoom(expectedPlayerID, expectedRoom, newRoom)
	}
	if client.GetID() != expectedPlayerID || client.GetRoom() != expectedRoom {
		return false
	}
	client.SetRoom(newRoom)
	return true
}

// SendMessageIfIdentity uses the strongest atomic capability exposed by the
// client. The fallback keeps lightweight test and bot clients compatible;
// production WebSocket clients implement IdentityConditionalSender.
func SendMessageIfIdentity(client ClientInterface, expectedPlayerID, expectedRoom string, msg *protocol.Message) (bool, error) {
	if client == nil {
		return false, nil
	}
	if sender, ok := client.(IdentityConditionalSender); ok {
		return sender.SendMessageIfIdentity(expectedPlayerID, expectedRoom, msg)
	}
	if client.GetID() != expectedPlayerID {
		return false, nil
	}
	if sender, ok := client.(RoomConditionalSender); ok {
		return sender.SendMessageIfRoom(expectedRoom, msg)
	}
	if client.GetRoom() != expectedRoom {
		return false, nil
	}
	return true, client.SendMessage(msg)
}

// ChatLimiter 聊天速率限制器接口
type ChatLimiter interface {
	AllowChat(clientID string) (allowed bool, reason string)
	ClearRateLimit(clientID string)
}
