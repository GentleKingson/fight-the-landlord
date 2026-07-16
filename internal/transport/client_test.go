package transport

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

var upgrader = websocket.Upgrader{}

type helloObservation struct {
	payload   protocol.HelloPayload
	requestID string
}

func readProtocolMessage(conn *websocket.Conn) (*protocol.Message, error) {
	frameType, frame, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if frameType != websocket.BinaryMessage {
		return nil, fmt.Errorf("unexpected websocket frame type %d", frameType)
	}
	return codec.Decode(frame)
}

func writeProtocolMessage(conn *websocket.Conn, msg *protocol.Message) error {
	frame, err := codec.Encode(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, frame)
}

func acceptHello(conn *websocket.Conn) (helloObservation, error) {
	msg, err := readProtocolMessage(conn)
	if err != nil {
		return helloObservation{}, err
	}
	defer codec.PutMessage(msg)
	if msg.Type != protocol.MsgHello {
		return helloObservation{}, fmt.Errorf("first message is %q", msg.Type)
	}
	payload, err := codec.ParsePayload[protocol.HelloPayload](msg)
	if err != nil {
		return helloObservation{}, err
	}
	requestID := ""
	if msg.Command != nil {
		requestID = msg.Command.RequestID
	}
	return helloObservation{payload: *payload, requestID: requestID}, nil
}

func writeNegotiated(conn *websocket.Conn, hello helloObservation) error {
	msg := codec.MustNewMessage(protocol.MsgNegotiated, protocol.NegotiatedPayload{
		ProtocolVersion: protocol.ProtocolVersion,
		ServerVersion:   "v1.2.3",
		Capabilities:    append([]string(nil), protocol.RequiredCapabilities...),
		ClientKind:      protocol.ClientKindTUI,
	})
	msg.Command = &protocol.CommandMeta{RequestID: hello.requestID}
	return writeProtocolMessage(conn, msg)
}

func websocketURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func TestClientConnectNegotiatesBeforeSendingCommands(t *testing.T) {
	helloSeen := make(chan helloObservation, 1)
	serverErrors := make(chan error, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer func() { _ = conn.Close() }()

		hello, err := acceptHello(conn)
		if err != nil {
			serverErrors <- err
			return
		}
		helloSeen <- hello
		if err := writeNegotiated(conn, hello); err != nil {
			serverErrors <- err
			return
		}

		msg, err := readProtocolMessage(conn)
		if err != nil {
			serverErrors <- err
			return
		}
		echo := codec.CloneMessage(msg)
		codec.PutMessage(msg)
		if err := writeProtocolMessage(conn, echo); err != nil {
			serverErrors <- err
		}
	}))
	defer s.Close()

	client := NewClient(websocketURL(s), "v1.2.3")
	require.NoError(t, client.Connect())
	defer client.Close()
	require.True(t, client.IsConnected())

	hello := <-helloSeen
	assert.Equal(t, protocol.ProtocolVersion, hello.payload.ProtocolVersion)
	assert.Equal(t, "v1.2.3", hello.payload.ClientVersion)
	assert.Equal(t, protocol.ClientKindTUI, hello.payload.ClientKind)
	assert.ElementsMatch(t, protocol.RequiredCapabilities, hello.payload.Capabilities)
	assert.NotEmpty(t, hello.requestID)

	require.NoError(t, client.SendMessage(codec.MustNewMessage(protocol.MsgPing, protocol.PingPayload{Timestamp: 123456})))
	received, err := client.ReceiveWithTimeout(time.Second)
	require.NoError(t, err)
	require.NotNil(t, received)
	assert.Equal(t, protocol.MsgPing, received.Type)
	require.NotNil(t, received.Command)
	assert.NotEmpty(t, received.Command.RequestID)

	select {
	case err := <-serverErrors:
		require.NoError(t, err)
	default:
	}
}

func TestClientConnectReturnsProtocolRejection(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		hello, err := acceptHello(conn)
		if err != nil {
			return
		}
		msg := codec.MustNewMessage(protocol.MsgProtocolRejected, protocol.ProtocolRejectedPayload{
			RequestID:                hello.requestID,
			Reason:                   "client version too old",
			SupportedProtocolVersion: protocol.ProtocolVersion,
			MinClientVersion:         "v2.0.0",
		})
		msg.Command = &protocol.CommandMeta{RequestID: hello.requestID}
		_ = writeProtocolMessage(conn, msg)
	}))
	defer s.Close()

	client := NewClient(websocketURL(s), "v1.0.0")
	err := client.Connect()
	require.Error(t, err)
	var rejected *ProtocolRejectedError
	require.True(t, errors.As(err, &rejected))
	assert.Equal(t, "client version too old", rejected.Reason)
	assert.Equal(t, "v2.0.0", rejected.MinClientVersion)
	assert.False(t, client.IsConnected())
}

func TestClientAddsAuthoritativeGameContextToActions(t *testing.T) {
	actionSeen := make(chan *protocol.Message, 1)
	serverErrors := make(chan error, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer func() { _ = conn.Close() }()
		hello, err := acceptHello(conn)
		if err != nil {
			serverErrors <- err
			return
		}
		if err := writeNegotiated(conn, hello); err != nil {
			serverErrors <- err
			return
		}
		turn := codec.MustNewMessage(protocol.MsgPlayTurn, protocol.PlayTurnPayload{PlayerID: "player-1"})
		turn.Event = &protocol.EventMeta{StreamID: "game:game-1", EventVersion: 8, GameID: "game-1", TurnID: 42}
		if err := writeProtocolMessage(conn, turn); err != nil {
			serverErrors <- err
			return
		}
		action, err := readProtocolMessage(conn)
		if err != nil {
			serverErrors <- err
			return
		}
		actionSeen <- codec.CloneMessage(action)
		codec.PutMessage(action)
	}))
	defer s.Close()

	client := NewClient(websocketURL(s), "ci")
	require.NoError(t, client.Connect())
	defer client.Close()
	turn, err := client.ReceiveWithTimeout(time.Second)
	require.NoError(t, err)
	assert.Equal(t, protocol.MsgPlayTurn, turn.Type)
	require.NoError(t, client.Pass())

	select {
	case action := <-actionSeen:
		assert.Equal(t, protocol.MsgPass, action.Type)
		require.NotNil(t, action.Command)
		assert.NotEmpty(t, action.Command.RequestID)
		assert.Equal(t, "game-1", action.Command.ExpectedGameID)
		assert.EqualValues(t, 42, action.Command.ExpectedTurnID)
	case err := <-serverErrors:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for game action")
	}
}

func TestReconnectUsesCapturedIdentityAfterHello(t *testing.T) {
	const (
		originalID    = "player-original"
		originalToken = "token-original"
		rotatedToken  = "token-rotated"
	)
	var connections atomic.Int32
	hellos := make(chan helloObservation, 2)
	reconnectSeen := make(chan *protocol.Message, 1)
	closeFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	serverErrors := make(chan error, 2)

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionNumber := connections.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer func() { _ = conn.Close() }()
		hello, err := acceptHello(conn)
		if err != nil {
			serverErrors <- err
			return
		}
		hellos <- hello
		if err := writeNegotiated(conn, hello); err != nil {
			serverErrors <- err
			return
		}

		if connectionNumber == 1 {
			if err := writeProtocolMessage(conn, codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
				PlayerID: originalID, PlayerName: "Original", ReconnectToken: originalToken,
			})); err != nil {
				serverErrors <- err
				return
			}
			<-closeFirst
			return
		}

		if err := writeProtocolMessage(conn, codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
			PlayerID: "player-provisional", PlayerName: "Provisional", ReconnectToken: "token-provisional",
		})); err != nil {
			serverErrors <- err
			return
		}
		reconnect, err := readProtocolMessage(conn)
		if err != nil {
			serverErrors <- err
			return
		}
		reconnectSeen <- codec.CloneMessage(reconnect)
		codec.PutMessage(reconnect)
		if err := writeProtocolMessage(conn, codec.MustNewMessage(protocol.MsgReconnected, protocol.ReconnectedPayload{
			PlayerID: originalID, PlayerName: "Original", ReconnectToken: rotatedToken,
		})); err != nil {
			serverErrors <- err
			return
		}
		<-releaseSecond
	}))
	defer s.Close()

	client := NewClient(websocketURL(s), "ci")
	require.NoError(t, client.Connect())
	defer client.Close()
	initial, err := client.ReceiveWithTimeout(time.Second)
	require.NoError(t, err)
	assert.Equal(t, protocol.MsgConnected, initial.Type)
	playerID, _, token := client.Identity()
	assert.Equal(t, originalID, playerID)
	assert.Equal(t, originalToken, token)

	close(closeFirst)
	select {
	case reconnect := <-reconnectSeen:
		assert.Equal(t, protocol.MsgReconnect, reconnect.Type)
		require.NotNil(t, reconnect.Command)
		assert.NotEmpty(t, reconnect.Command.RequestID)
		payload, parseErr := codec.ParsePayload[protocol.ReconnectPayload](reconnect)
		require.NoError(t, parseErr)
		assert.Equal(t, originalID, payload.PlayerID)
		assert.Equal(t, originalToken, payload.Token)
	case err := <-serverErrors:
		require.NoError(t, err)
	case <-time.After(6 * time.Second):
		t.Fatal("timed out waiting for reconnect command")
	}

	reconnected, err := client.ReceiveWithTimeout(time.Second)
	require.NoError(t, err)
	assert.Equal(t, protocol.MsgReconnected, reconnected.Type, "provisional Connected must be suppressed")
	playerID, _, token = client.Identity()
	assert.Equal(t, originalID, playerID)
	assert.Equal(t, rotatedToken, token)
	assert.Len(t, hellos, 2)
	close(releaseSecond)
}

func TestReconnectRetriesDroppedRestoreThenAdoptsProvisionalIdentityOnExpiredToken(t *testing.T) {
	const (
		originalID     = "player-original"
		originalToken  = "token-expired"
		fallbackID     = "player-fallback"
		fallbackToken  = "token-fallback"
		discardedID    = "player-discarded"
		discardedToken = "token-discarded"
	)
	type reconnectObservation struct {
		connection int32
		message    *protocol.Message
	}

	var connections atomic.Int32
	reconnects := make(chan reconnectObservation, 2)
	closeFirst := make(chan struct{})
	releaseThird := make(chan struct{})
	serverErrors := make(chan error, 4)

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionNumber := connections.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer func() { _ = conn.Close() }()
		hello, err := acceptHello(conn)
		if err != nil {
			serverErrors <- err
			return
		}
		if err := writeNegotiated(conn, hello); err != nil {
			serverErrors <- err
			return
		}

		switch connectionNumber {
		case 1:
			if err := writeProtocolMessage(conn, codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
				PlayerID: originalID, PlayerName: "Original", ReconnectToken: originalToken,
			})); err != nil {
				serverErrors <- err
				return
			}
			<-closeFirst
		case 2:
			if err := writeProtocolMessage(conn, codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
				PlayerID: discardedID, PlayerName: "Discarded", ReconnectToken: discardedToken,
			})); err != nil {
				serverErrors <- err
				return
			}
			reconnect, err := readProtocolMessage(conn)
			if err != nil {
				serverErrors <- err
				return
			}
			reconnects <- reconnectObservation{connection: connectionNumber, message: codec.CloneMessage(reconnect)}
			codec.PutMessage(reconnect)
			// Drop the physical connection before either Reconnected or Error.
			// The client must retry with the original captured credential.
		case 3:
			if err := writeProtocolMessage(conn, codec.MustNewMessage(protocol.MsgConnected, protocol.ConnectedPayload{
				PlayerID: fallbackID, PlayerName: "Fallback", ReconnectToken: fallbackToken,
			})); err != nil {
				serverErrors <- err
				return
			}
			reconnect, err := readProtocolMessage(conn)
			if err != nil {
				serverErrors <- err
				return
			}
			reconnects <- reconnectObservation{connection: connectionNumber, message: codec.CloneMessage(reconnect)}
			if reconnect.Command == nil {
				codec.PutMessage(reconnect)
				serverErrors <- errors.New("reconnect command is missing request metadata")
				return
			}
			requestID := reconnect.Command.RequestID
			codec.PutMessage(reconnect)
			if err := writeProtocolMessage(conn, codec.NewCorrelatedCommandErrorMessage(
				protocol.ErrCodeReconnectExpired,
				protocol.ErrorMessages[protocol.ErrCodeReconnectExpired],
				requestID,
				protocol.MsgReconnect,
			)); err != nil {
				serverErrors <- err
				return
			}
			<-releaseThird
		default:
			serverErrors <- fmt.Errorf("unexpected connection %d", connectionNumber)
		}
	}))
	defer s.Close()

	client := NewClient(websocketURL(s), "ci")
	reconnectSettled := make(chan struct{}, 1)
	client.OnReconnect = func() {
		select {
		case reconnectSettled <- struct{}{}:
		default:
		}
	}
	release := func() {
		select {
		case <-releaseThird:
		default:
			close(releaseThird)
		}
	}
	defer release()
	defer client.Close()
	require.NoError(t, client.Connect())
	initial, err := client.ReceiveWithTimeout(time.Second)
	require.NoError(t, err)
	assert.Equal(t, protocol.MsgConnected, initial.Type)

	close(closeFirst)
	for wantConnection := int32(2); wantConnection <= 3; wantConnection++ {
		select {
		case observation := <-reconnects:
			assert.Equal(t, wantConnection, observation.connection)
			require.Equal(t, protocol.MsgReconnect, observation.message.Type)
			payload, parseErr := codec.ParsePayload[protocol.ReconnectPayload](observation.message)
			require.NoError(t, parseErr)
			assert.Equal(t, originalID, payload.PlayerID)
			assert.Equal(t, originalToken, payload.Token)
		case serverErr := <-serverErrors:
			require.NoError(t, serverErr)
		case <-time.After(6 * time.Second):
			t.Fatalf("timed out waiting for reconnect on connection %d", wantConnection)
		}
	}

	connected, err := client.ReceiveWithTimeout(time.Second)
	require.NoError(t, err)
	require.Equal(t, protocol.MsgConnected, connected.Type, "fallback identity must be published before the rejection")
	connectedPayload, err := codec.ParsePayload[protocol.ConnectedPayload](connected)
	require.NoError(t, err)
	assert.Equal(t, fallbackID, connectedPayload.PlayerID)
	assert.Equal(t, fallbackToken, connectedPayload.ReconnectToken)

	rejection, err := client.ReceiveWithTimeout(time.Second)
	require.NoError(t, err)
	require.Equal(t, protocol.MsgError, rejection.Type)
	rejectionPayload, err := codec.ParsePayload[protocol.ErrorPayload](rejection)
	require.NoError(t, err)
	assert.Equal(t, protocol.ErrCodeReconnectExpired, rejectionPayload.Code)
	assert.Equal(t, protocol.MsgReconnect, rejectionPayload.CommandType)
	require.NotNil(t, rejection.Command)
	assert.Equal(t, rejectionPayload.RequestID, rejection.Command.RequestID)

	playerID, playerName, reconnectToken := client.Identity()
	assert.Equal(t, fallbackID, playerID)
	assert.Equal(t, "Fallback", playerName)
	assert.Equal(t, fallbackToken, reconnectToken)
	assert.False(t, client.IsReconnecting())
	assert.True(t, client.IsConnected())
	assert.EqualValues(t, 3, connections.Load())
	select {
	case <-reconnectSettled:
	case <-time.After(time.Second):
		t.Fatal("reconnect completion callback was not delivered after adopting fallback identity")
	}
	release()
}
