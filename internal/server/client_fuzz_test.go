package server

import (
	"testing"

	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

// FuzzClientIncomingFrame exercises the complete post-handshake WebSocket
// frame boundary, including protobuf decoding, command metadata validation,
// error encoding, and binary-frame enforcement.
func FuzzClientIncomingFrame(f *testing.F) {
	f.Add(uint8(websocket.BinaryMessage), []byte{})
	f.Add(uint8(websocket.TextMessage), []byte("not protobuf"))
	reconnect := codec.MustNewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{})
	reconnect.Command = &protocol.CommandMeta{RequestID: "fuzz-reconnect"}
	encoded, err := codec.Encode(reconnect)
	if err != nil {
		f.Fatalf("encode reconnect seed: %v", err)
	}
	f.Add(uint8(websocket.BinaryMessage), encoded)

	f.Fuzz(func(t *testing.T, frameType uint8, frame []byte) {
		if len(frame) > maxMessageSize {
			return
		}
		server := &Server{
			commandCache: newCommandCache(32, defaultCommandCacheTTL),
		}
		client := &Client{
			ID:     "fuzz-player",
			Name:   "Fuzz Player",
			server: server,
			send:   make(chan []byte, 32),
			done:   make(chan struct{}),
		}
		client.handleIncomingFrame(int(frameType), frame)
	})
}
