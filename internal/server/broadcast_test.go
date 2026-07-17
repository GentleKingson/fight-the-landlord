package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

func TestBroadcastToLobbyFromSkipsRoomsAndUsesSenderResultPath(t *testing.T) {
	server := &Server{clients: make(map[string]*Client)}
	sender := bufferedTestClient(server, "sender", "")
	peer := bufferedTestClient(server, "peer", "")
	inRoom := bufferedTestClient(server, "room-player", "ROOM01")
	server.clients[sender.ID] = sender
	server.clients[peer.ID] = peer
	server.clients[inRoom.ID] = inRoom

	message := codec.MustNewMessage(protocol.MsgWarning, protocol.WarningPayload{Code: 1, Message: "notice"})
	defer codec.PutMessage(message)
	server.BroadcastToLobbyFrom(sender, message)

	assert.Len(t, sender.send, 1)
	assert.Len(t, peer.send, 1)
	assert.Empty(t, inRoom.send)
}

func bufferedTestClient(server *Server, playerID, roomID string) *Client {
	return &Client{
		ID:     playerID,
		Name:   playerID,
		RoomID: roomID,
		server: server,
		send:   make(chan []byte, 2),
		done:   make(chan struct{}),
	}
}
