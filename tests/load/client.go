package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

var requestSequence atomic.Uint64

type wireMessage struct {
	messageType protocol.MessageType
	payload     []byte
	requestID   string
}

func (m wireMessage) asProtocolMessage() *protocol.Message {
	message := &protocol.Message{Type: m.messageType, Payload: m.payload}
	if m.requestID != "" {
		message.Command = &protocol.CommandMeta{RequestID: m.requestID}
	}
	return message
}

type physicalConnection struct {
	conn      *websocket.Conn
	events    chan wireMessage
	errors    chan error
	done      chan struct{}
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func dialPhysical(ctx context.Context, endpoint, clientVersion string, timeout time.Duration, clientIndex int) (*physicalConnection, error) {
	dialer := websocket.Dialer{HandshakeTimeout: timeout, EnableCompression: false}
	conn, response, err := dialer.DialContext(ctx, endpoint, http.Header{"User-Agent": []string{"fight-landlord-load/1"}})
	if response != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*physicalConnection, error) {
		_ = conn.Close()
		return nil, cause
	}

	requestID := nextRequestID("hello", clientIndex)
	hello, err := codec.NewMessage(protocol.MsgHello, protocol.HelloPayload{
		ProtocolVersion: protocol.ProtocolVersion,
		ClientVersion:   clientVersion,
		Capabilities:    append([]string(nil), protocol.RequiredCapabilities...),
		ClientKind:      protocol.ClientKindTUI,
	})
	if err != nil {
		return fail(fmt.Errorf("build hello: %w", err))
	}
	hello.Command = &protocol.CommandMeta{RequestID: requestID}
	frame, err := codec.Encode(hello)
	codec.PutMessage(hello)
	if err != nil {
		return fail(fmt.Errorf("encode hello: %w", err))
	}
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return fail(fmt.Errorf("write hello: %w", err))
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	frameType, frame, err := conn.ReadMessage()
	if err != nil {
		return fail(fmt.Errorf("read negotiation: %w", err))
	}
	if frameType != websocket.BinaryMessage {
		return fail(fmt.Errorf("negotiation returned frame type %d", frameType))
	}
	responseMessage, err := codec.Decode(frame)
	if err != nil {
		return fail(fmt.Errorf("decode negotiation: %w", err))
	}
	if responseMessage.Type != protocol.MsgNegotiated {
		responseType := responseMessage.Type
		codec.PutMessage(responseMessage)
		return fail(fmt.Errorf("negotiation returned %s", responseType))
	}
	if responseMessage.Command == nil || responseMessage.Command.RequestID != requestID {
		codec.PutMessage(responseMessage)
		return fail(errors.New("negotiation request_id mismatch"))
	}
	codec.PutMessage(responseMessage)
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})

	physical := &physicalConnection{
		conn:   conn,
		events: make(chan wireMessage, 32),
		errors: make(chan error, 1),
		done:   make(chan struct{}),
	}
	go physical.readLoop()
	return physical, nil
}

func (p *physicalConnection) readLoop() {
	defer close(p.done)
	defer p.conn.Close()
	for {
		frameType, frame, err := p.conn.ReadMessage()
		if err != nil {
			p.reportError(err)
			return
		}
		if frameType != websocket.BinaryMessage {
			continue
		}
		message, err := codec.Decode(frame)
		if err != nil {
			p.reportError(fmt.Errorf("decode server frame: %w", err))
			return
		}
		if !loadResponseType(message.Type) {
			codec.PutMessage(message)
			continue
		}
		event := wireMessage{messageType: message.Type, payload: append([]byte(nil), message.Payload...)}
		if message.Command != nil {
			event.requestID = message.Command.RequestID
		}
		codec.PutMessage(message)
		select {
		case p.events <- event:
		case <-p.done:
			return
		}
	}
}

func loadResponseType(messageType protocol.MessageType) bool {
	switch messageType {
	case protocol.MsgConnected,
		protocol.MsgReconnected,
		protocol.MsgPong,
		protocol.MsgRoomCreated,
		protocol.MsgRoomJoined,
		protocol.MsgRoomLeft,
		protocol.MsgMatchQueued,
		protocol.MsgMatchCancelled,
		protocol.MsgError:
		return true
	default:
		return false
	}
}

func (p *physicalConnection) reportError(err error) {
	select {
	case p.errors <- err:
	default:
	}
}

func (p *physicalConnection) write(message *protocol.Message, timeout time.Duration) error {
	frame, err := codec.Encode(message)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	select {
	case <-p.done:
		return errors.New("connection closed")
	default:
	}
	_ = p.conn.SetWriteDeadline(time.Now().Add(timeout))
	err = p.conn.WriteMessage(websocket.BinaryMessage, frame)
	_ = p.conn.SetWriteDeadline(time.Time{})
	return err
}

func (p *physicalConnection) await(ctx context.Context, expected protocol.MessageType, requestID string) (wireMessage, error) {
	for {
		select {
		case event := <-p.events:
			if event.messageType == protocol.MsgError && (requestID == "" || event.requestID == "" || event.requestID == requestID) {
				return wireMessage{}, protocolResponseError(event)
			}
			if event.messageType == expected && (requestID == "" || event.requestID == requestID) {
				return event, nil
			}
		case err := <-p.errors:
			return wireMessage{}, err
		case <-p.done:
			return wireMessage{}, errors.New("connection closed")
		case <-ctx.Done():
			return wireMessage{}, ctx.Err()
		}
	}
}

func protocolResponseError(event wireMessage) error {
	payload, err := codec.ParsePayload[protocol.ErrorPayload](event.asProtocolMessage())
	if err != nil {
		return fmt.Errorf("server returned an undecodable error: %w", err)
	}
	return fmt.Errorf("server error %d for %s: %s", payload.Code, payload.CommandType, payload.Message)
}

func (p *physicalConnection) close(abrupt bool) {
	p.closeOnce.Do(func() {
		if !abrupt {
			deadline := time.Now().Add(time.Second)
			_ = p.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "load test complete"), deadline)
		}
		_ = p.conn.Close()
	})
}

func (p *physicalConnection) active() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

type loadClient struct {
	index      int
	endpoint   string
	version    string
	timeout    time.Duration
	physical   *physicalConnection
	playerID   string
	playerName string
	token      string
}

func connectLoadClient(ctx context.Context, cfg config, index int) (*loadClient, time.Duration, error) {
	started := time.Now()
	physical, err := dialPhysical(ctx, cfg.URL, cfg.ClientVersion, cfg.OperationTimeout, index)
	if err != nil {
		return nil, time.Since(started), err
	}
	event, err := physical.await(ctx, protocol.MsgConnected, "")
	if err != nil {
		physical.close(true)
		return nil, time.Since(started), fmt.Errorf("await connected: %w", err)
	}
	payload, err := codec.ParsePayload[protocol.ConnectedPayload](event.asProtocolMessage())
	if err != nil {
		physical.close(true)
		return nil, time.Since(started), fmt.Errorf("decode connected: %w", err)
	}
	if payload.PlayerID == "" || payload.ReconnectToken == "" {
		physical.close(true)
		return nil, time.Since(started), errors.New("TUI connection omitted reconnect identity")
	}
	return &loadClient{
		index:      index,
		endpoint:   cfg.URL,
		version:    cfg.ClientVersion,
		timeout:    cfg.OperationTimeout,
		physical:   physical,
		playerID:   payload.PlayerID,
		playerName: payload.PlayerName,
		token:      payload.ReconnectToken,
	}, time.Since(started), nil
}

func (c *loadClient) reconnect(ctx context.Context) (time.Duration, error) {
	started := time.Now()
	if c.physical == nil || c.playerID == "" || c.token == "" {
		return 0, errors.New("client has no reconnectable identity")
	}
	oldPhysical := c.physical
	oldPlayerID := c.playerID
	oldToken := c.token
	c.physical = nil
	oldPhysical.close(true)

	physical, err := dialPhysical(ctx, c.endpoint, c.version, c.timeout, c.index)
	if err != nil {
		return time.Since(started), fmt.Errorf("dial replacement connection: %w", err)
	}
	requestID := nextRequestID("reconnect", c.index)
	if err := physical.sendCommand(protocol.MsgReconnect, protocol.ReconnectPayload{Token: oldToken, PlayerID: oldPlayerID}, requestID, c.timeout); err != nil {
		physical.close(true)
		return time.Since(started), err
	}
	event, err := physical.await(ctx, protocol.MsgReconnected, requestID)
	if err != nil {
		physical.close(true)
		return time.Since(started), fmt.Errorf("await reconnected: %w", err)
	}
	payload, err := codec.ParsePayload[protocol.ReconnectedPayload](event.asProtocolMessage())
	if err != nil {
		physical.close(true)
		return time.Since(started), fmt.Errorf("decode reconnected: %w", err)
	}
	if payload.PlayerID != oldPlayerID {
		physical.close(true)
		return time.Since(started), fmt.Errorf("reconnect changed player ID from %s to %s", oldPlayerID, payload.PlayerID)
	}
	if payload.ReconnectToken == "" || payload.ReconnectToken == oldToken {
		physical.close(true)
		return time.Since(started), errors.New("reconnect token did not rotate")
	}
	c.physical = physical
	c.playerID = payload.PlayerID
	c.playerName = payload.PlayerName
	c.token = payload.ReconnectToken
	return time.Since(started), nil
}

func (p *physicalConnection) sendCommand(messageType protocol.MessageType, payload any, requestID string, timeout time.Duration) error {
	message, err := codec.NewMessage(messageType, payload)
	if err != nil {
		return err
	}
	defer codec.PutMessage(message)
	message.Command = &protocol.CommandMeta{RequestID: requestID}
	return p.write(message, timeout)
}

func (c *loadClient) command(ctx context.Context, commandType, responseType protocol.MessageType, payload any) (wireMessage, time.Duration, error) {
	started := time.Now()
	if c.physical == nil || !c.physical.active() {
		return wireMessage{}, 0, errors.New("client connection is not active")
	}
	requestID := nextRequestID(string(commandType), c.index)
	if err := c.physical.sendCommand(commandType, payload, requestID, c.timeout); err != nil {
		return wireMessage{}, time.Since(started), err
	}
	event, err := c.physical.await(ctx, responseType, requestID)
	return event, time.Since(started), err
}

func (c *loadClient) createRoom(ctx context.Context) (string, time.Duration, error) {
	event, latency, err := c.command(ctx, protocol.MsgCreateRoom, protocol.MsgRoomCreated, nil)
	if err != nil {
		return "", latency, err
	}
	payload, err := codec.ParsePayload[protocol.RoomCreatedPayload](event.asProtocolMessage())
	if err != nil {
		return "", latency, err
	}
	if payload.RoomCode == "" {
		return "", latency, errors.New("server returned an empty room code")
	}
	return payload.RoomCode, latency, nil
}

func (c *loadClient) joinRoom(ctx context.Context, roomCode string) (time.Duration, error) {
	_, latency, err := c.command(ctx, protocol.MsgJoinRoom, protocol.MsgRoomJoined, protocol.JoinRoomPayload{RoomCode: roomCode})
	return latency, err
}

func (c *loadClient) leaveRoom(ctx context.Context) (time.Duration, error) {
	_, latency, err := c.command(ctx, protocol.MsgLeaveRoom, protocol.MsgRoomLeft, nil)
	return latency, err
}

func (c *loadClient) ping(ctx context.Context) (time.Duration, error) {
	_, latency, err := c.command(ctx, protocol.MsgPing, protocol.MsgPong, protocol.PingPayload{Timestamp: time.Now().UnixMilli()})
	return latency, err
}

func (c *loadClient) queueMatch(ctx context.Context) (time.Duration, error) {
	_, latency, err := c.command(ctx, protocol.MsgQuickMatch, protocol.MsgMatchQueued, nil)
	return latency, err
}

func (c *loadClient) cancelMatch(ctx context.Context) (time.Duration, error) {
	_, latency, err := c.command(ctx, protocol.MsgCancelMatch, protocol.MsgMatchCancelled, nil)
	return latency, err
}

func (c *loadClient) awaitMatchTimeout(ctx context.Context) (time.Duration, error) {
	started := time.Now()
	event, err := c.physical.await(ctx, protocol.MsgMatchCancelled, "")
	if err != nil {
		return time.Since(started), err
	}
	payload, err := codec.ParsePayload[protocol.MatchCancelledPayload](event.asProtocolMessage())
	if err != nil {
		return time.Since(started), err
	}
	if payload.Reason != "timeout" {
		return time.Since(started), fmt.Errorf("match canceled for %q instead of timeout", payload.Reason)
	}
	return time.Since(started), nil
}

func (c *loadClient) close() {
	if c != nil && c.physical != nil {
		c.physical.close(false)
	}
}

func nextRequestID(operation string, clientIndex int) string {
	return fmt.Sprintf("load:%s:%d:%d", operation, clientIndex, requestSequence.Add(1))
}
