package handler

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestHandlerDispatchesPing(t *testing.T) {
	client := testutil.NewSimpleClient("ping-player", "Ping Player")
	handler := NewHandler(HandlerDeps{})
	message := codec.MustNewMessage(protocol.MsgPing, protocol.PingPayload{Timestamp: 42})
	defer codec.PutMessage(message)

	handler.Handle(client, message)
	require.Len(t, client.Messages, 1)
	require.Equal(t, protocol.MsgPong, client.Messages[0].Type)
	payload, err := codec.ParsePayload[protocol.PongPayload](client.Messages[0])
	require.NoError(t, err)
	require.EqualValues(t, 42, payload.ClientTimestamp)
}
