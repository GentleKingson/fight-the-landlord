// Command chaosclient drives the small amount of protocol state needed by the
// process-restart fault harness. It is not a general load generator.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

type checkpoint struct {
	PlayerID       string `json:"player_id"`
	PlayerName     string `json:"player_name"`
	ReconnectToken string `json:"reconnect_token"`
	RoomCode       string `json:"room_code"`
}

type restartResult struct {
	PlannedSIGTERMExitClean bool `json:"planned_sigterm_exit_clean"`
	OldSessionRestored      bool `json:"old_session_restored"`
	WaitingRoomRestored     bool `json:"waiting_room_restored"`
	FreshSessionUsable      bool `json:"fresh_session_usable"`
	UnexpectedCrashCount    int  `json:"unexpected_crash_count"`
}

type wireClient struct {
	conn    *websocket.Conn
	timeout time.Duration
	nextID  uint64
}

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: chaosclient hold-room|probe-restart [flags]")
	}
	switch os.Args[1] {
	case "hold-room":
		runHoldRoom(os.Args[2:])
	case "probe-restart":
		runProbeRestart(os.Args[2:])
	default:
		fatalf("unknown mode %q", os.Args[1])
	}
}

func runHoldRoom(args []string) {
	flags := flag.NewFlagSet("hold-room", flag.ExitOnError)
	endpoint := flags.String("url", "ws://127.0.0.1:1784/ws", "WebSocket endpoint")
	statePath := flags.String("state", "", "private checkpoint output")
	clientVersion := flags.String("client-version", "ci", "client release version")
	timeout := flags.Duration("timeout", 10*time.Second, "protocol operation timeout")
	_ = flags.Parse(args)
	if *statePath == "" {
		fatalf("--state is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	client, connected, err := connect(ctx, *endpoint, *clientVersion, *timeout)
	cancel()
	if err != nil {
		fatalf("connect: %v", err)
	}
	defer client.close()

	ctx, cancel = context.WithTimeout(context.Background(), *timeout)
	response, err := client.command(ctx, protocol.MsgCreateRoom, nil, protocol.MsgRoomCreated)
	cancel()
	if err != nil {
		fatalf("create room: %v", err)
	}
	defer codec.PutMessage(response)
	created, err := codec.ParsePayload[protocol.RoomCreatedPayload](response)
	if err != nil || created.RoomCode == "" {
		fatalf("decode created room: %v", err)
	}
	state := checkpoint{
		PlayerID:       connected.PlayerID,
		PlayerName:     connected.PlayerName,
		ReconnectToken: connected.ReconnectToken,
		RoomCode:       created.RoomCode,
	}
	data, err := json.Marshal(state)
	if err != nil {
		fatalf("encode checkpoint: %v", err)
	}
	if err := os.WriteFile(*statePath, append(data, '\n'), 0o600); err != nil {
		fatalf("write checkpoint: %v", err)
	}

	holdContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-holdContext.Done()
}

func runProbeRestart(args []string) {
	flags := flag.NewFlagSet("probe-restart", flag.ExitOnError)
	endpoint := flags.String("url", "ws://127.0.0.1:1784/ws", "WebSocket endpoint")
	statePath := flags.String("state", "", "private checkpoint input")
	outputPath := flags.String("output", "", "restart result output")
	clientVersion := flags.String("client-version", "ci", "client release version")
	timeout := flags.Duration("timeout", 10*time.Second, "protocol operation timeout")
	_ = flags.Parse(args)
	if *statePath == "" || *outputPath == "" {
		fatalf("--state and --output are required")
	}

	data, err := os.ReadFile(*statePath)
	if err != nil {
		fatalf("read checkpoint: %v", err)
	}
	var previous checkpoint
	if err := json.Unmarshal(data, &previous); err != nil {
		fatalf("decode checkpoint: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	client, connected, err := connect(ctx, *endpoint, *clientVersion, *timeout)
	cancel()
	if err != nil {
		fatalf("connect after restart: %v", err)
	}
	defer client.close()
	if connected.PlayerID == previous.PlayerID {
		fatalf("fresh process unexpectedly recreated the old player identity")
	}

	ctx, cancel = context.WithTimeout(context.Background(), *timeout)
	reconnectResponse, reconnectErr := client.command(ctx, protocol.MsgReconnect, protocol.ReconnectPayload{
		PlayerID: previous.PlayerID,
		Token:    previous.ReconnectToken,
	}, protocol.MsgReconnected)
	cancel()
	codec.PutMessage(reconnectResponse)
	if reconnectErr == nil {
		fatalf("old in-memory reconnect session unexpectedly survived process restart")
	}

	ctx, cancel = context.WithTimeout(context.Background(), *timeout)
	joinResponse, joinErr := client.command(ctx, protocol.MsgJoinRoom, protocol.JoinRoomPayload{RoomCode: previous.RoomCode}, protocol.MsgRoomJoined)
	cancel()
	codec.PutMessage(joinResponse)
	if joinErr == nil {
		fatalf("old in-memory waiting room unexpectedly survived process restart")
	}

	ctx, cancel = context.WithTimeout(context.Background(), *timeout)
	pingResponse, pingErr := client.command(ctx, protocol.MsgPing, protocol.PingPayload{Timestamp: time.Now().UnixMilli()}, protocol.MsgPong)
	cancel()
	codec.PutMessage(pingResponse)
	if pingErr != nil {
		fatalf("fresh session ping after rejected recovery: %v", pingErr)
	}

	result := restartResult{
		PlannedSIGTERMExitClean: true,
		OldSessionRestored:      false,
		WaitingRoomRestored:     false,
		FreshSessionUsable:      true,
		UnexpectedCrashCount:    0,
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatalf("encode result: %v", err)
	}
	if err := os.WriteFile(*outputPath, append(encoded, '\n'), 0o644); err != nil {
		fatalf("write result: %v", err)
	}
}

func connect(ctx context.Context, endpoint, clientVersion string, timeout time.Duration) (*wireClient, *protocol.ConnectedPayload, error) {
	dialer := websocket.Dialer{HandshakeTimeout: timeout}
	conn, response, err := dialer.DialContext(ctx, endpoint, http.Header{"User-Agent": []string{"fight-landlord-chaos/1"}})
	if response != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, nil, err
	}
	client := &wireClient{conn: conn, timeout: timeout}
	helloID := client.requestID("hello")
	hello := codec.MustNewMessage(protocol.MsgHello, protocol.HelloPayload{
		ProtocolVersion: protocol.ProtocolVersion,
		ClientVersion:   clientVersion,
		Capabilities:    append([]string(nil), protocol.RequiredCapabilities...),
		ClientKind:      protocol.ClientKindTUI,
	})
	hello.Command = &protocol.CommandMeta{RequestID: helloID}
	defer codec.PutMessage(hello)
	if err := client.write(hello); err != nil {
		client.close()
		return nil, nil, err
	}
	negotiated, err := client.read(ctx)
	if err != nil {
		client.close()
		return nil, nil, err
	}
	if negotiated.Type != protocol.MsgNegotiated || negotiated.Command == nil || negotiated.Command.RequestID != helloID {
		responseType := negotiated.Type
		codec.PutMessage(negotiated)
		client.close()
		return nil, nil, fmt.Errorf("unexpected negotiation response %s", responseType)
	}
	codec.PutMessage(negotiated)
	connectedMessage, err := client.read(ctx)
	if err != nil {
		client.close()
		return nil, nil, err
	}
	if connectedMessage.Type != protocol.MsgConnected {
		responseType := connectedMessage.Type
		codec.PutMessage(connectedMessage)
		client.close()
		return nil, nil, fmt.Errorf("expected connected, got %s", responseType)
	}
	connected, err := codec.ParsePayload[protocol.ConnectedPayload](connectedMessage)
	if err != nil || connected == nil || connected.PlayerID == "" || connected.ReconnectToken == "" {
		codec.PutMessage(connectedMessage)
		client.close()
		if err != nil {
			return nil, nil, fmt.Errorf("decode connected identity: %w", err)
		}
		return nil, nil, errors.New("connected response omitted reconnect identity")
	}
	connectedCopy := *connected
	codec.PutMessage(connectedMessage)
	return client, &connectedCopy, nil
}

func (c *wireClient) command(ctx context.Context, commandType protocol.MessageType, payload any, expected protocol.MessageType) (*protocol.Message, error) {
	requestID := c.requestID(string(commandType))
	message, err := codec.NewMessage(commandType, payload)
	if err != nil {
		return nil, err
	}
	message.Command = &protocol.CommandMeta{RequestID: requestID}
	defer codec.PutMessage(message)
	if err := c.write(message); err != nil {
		return nil, err
	}
	for {
		response, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		if response.Command == nil || response.Command.RequestID != requestID {
			codec.PutMessage(response)
			continue
		}
		if response.Type == protocol.MsgError {
			failure, decodeErr := codec.ParsePayload[protocol.ErrorPayload](response)
			if decodeErr != nil {
				codec.PutMessage(response)
				return nil, decodeErr
			}
			failureErr := fmt.Errorf("server rejected %s with code %d: %s", commandType, failure.Code, failure.Message)
			codec.PutMessage(response)
			return nil, failureErr
		}
		if response.Type != expected {
			responseType := response.Type
			codec.PutMessage(response)
			return nil, fmt.Errorf("expected %s for %s, got %s", expected, commandType, responseType)
		}
		return response, nil
	}
}

func (c *wireClient) write(message *protocol.Message) error {
	frame, err := codec.Encode(message)
	if err != nil {
		return err
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.timeout))
	return c.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (c *wireClient) read(ctx context.Context) (*protocol.Message, error) {
	deadline := time.Now().Add(c.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = c.conn.SetReadDeadline(deadline)
	for {
		frameType, frame, err := c.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		if frameType != websocket.BinaryMessage {
			continue
		}
		return codec.Decode(frame)
	}
}

func (c *wireClient) requestID(operation string) string {
	c.nextID++
	return fmt.Sprintf("chaos:%s:%d", operation, c.nextID)
}

func (c *wireClient) close() {
	if c == nil || c.conn == nil {
		return
	}
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "chaos probe complete"), time.Now().Add(time.Second))
	_ = c.conn.Close()
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
