package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
)

var publicTestRequestSequence atomic.Uint64

const publicTestActionDelay = 200 * time.Millisecond

type wireEvent struct {
	messageType protocol.MessageType
	payload     []byte
	event       *protocol.EventMeta
	command     *protocol.CommandMeta
}

func eventFromMessage(message *protocol.Message) wireEvent {
	event := wireEvent{
		messageType: message.Type,
		payload:     append([]byte(nil), message.Payload...),
	}
	if message.Event != nil {
		copy := *message.Event
		event.event = &copy
	}
	if message.Command != nil {
		copy := *message.Command
		event.command = &copy
	}
	return event
}

func (e wireEvent) message() *protocol.Message {
	return &protocol.Message{
		Type:    e.messageType,
		Payload: e.payload,
		Event:   e.event,
		Command: e.command,
	}
}

type incomingEvent struct {
	connection *physicalConnection
	event      wireEvent
	err        error
}

type physicalConnection struct {
	conn      *websocket.Conn
	inbox     chan<- incomingEvent
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func dialNegotiatedConnection(ctx context.Context, cfg config, inbox chan<- incomingEvent, index int) (*physicalConnection, error) {
	dialer := websocket.Dialer{HandshakeTimeout: cfg.OperationTimeout, EnableCompression: false}
	conn, response, err := dialer.DialContext(ctx, cfg.URL, http.Header{
		"User-Agent": []string{"fight-landlord-public-test/1"},
	})
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

	hello, err := codec.NewMessage(protocol.MsgHello, protocol.HelloPayload{
		ProtocolVersion: protocol.ProtocolVersion,
		ClientVersion:   cfg.ClientVersion,
		Capabilities:    append([]string(nil), protocol.RequiredCapabilities...),
		ClientKind:      protocol.ClientKindTUI,
	})
	if err != nil {
		return fail(fmt.Errorf("build hello: %w", err))
	}
	hello.Command = &protocol.CommandMeta{RequestID: nextRequestID("hello", index)}
	frame, err := codec.Encode(hello)
	codec.PutMessage(hello)
	if err != nil {
		return fail(fmt.Errorf("encode hello: %w", err))
	}
	_ = conn.SetWriteDeadline(time.Now().Add(cfg.OperationTimeout))
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return fail(fmt.Errorf("write hello: %w", err))
	}
	message, err := readProtocolMessage(conn, cfg.OperationTimeout)
	if err != nil {
		return fail(fmt.Errorf("read negotiation: %w", err))
	}
	defer codec.PutMessage(message)
	if message.Type != protocol.MsgNegotiated {
		return fail(fmt.Errorf("negotiation returned %s", message.Type))
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	return &physicalConnection{conn: conn, inbox: inbox}, nil
}

func readProtocolMessage(conn *websocket.Conn, timeout time.Duration) (*protocol.Message, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		frameType, frame, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		if frameType != websocket.BinaryMessage {
			continue
		}
		message, err := codec.Decode(frame)
		if err != nil {
			return nil, fmt.Errorf("decode server frame: %w", err)
		}
		return message, nil
	}
}

func (p *physicalConnection) start() {
	go p.readLoop()
}

func (p *physicalConnection) readLoop() {
	for {
		frameType, frame, err := p.conn.ReadMessage()
		if err != nil {
			p.inbox <- incomingEvent{connection: p, err: err}
			return
		}
		if frameType != websocket.BinaryMessage {
			continue
		}
		message, err := codec.Decode(frame)
		if err != nil {
			p.inbox <- incomingEvent{connection: p, err: fmt.Errorf("decode server frame: %w", err)}
			return
		}
		event := eventFromMessage(message)
		codec.PutMessage(message)
		p.inbox <- incomingEvent{connection: p, event: event}
	}
}

func (p *physicalConnection) write(message *protocol.Message, timeout time.Duration) error {
	frame, err := codec.Encode(message)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(timeout))
	err = p.conn.WriteMessage(websocket.BinaryMessage, frame)
	_ = p.conn.SetWriteDeadline(time.Time{})
	return err
}

func (p *physicalConnection) close(abrupt bool) {
	p.closeOnce.Do(func() {
		if !abrupt {
			deadline := time.Now().Add(time.Second)
			_ = p.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "public test complete"), deadline)
		}
		_ = p.conn.Close()
	})
}

type clientAction struct {
	kind   protocol.MessageType
	gameID string
	turnID int64
}

type clientState struct {
	hand        []card.Card
	lastPlayed  rule.ParsedHand
	passCount   int
	gameID      string
	turnID      int64
	currentTurn string
	mustPlay    bool
	canBeat     bool
	isGrab      bool
}

type gameClient struct {
	index int
	cfg   config
	run   *runState
	room  *gameRoom

	ctx     context.Context
	cancel  context.CancelFunc
	inbox   chan incomingEvent
	actions chan clientAction
	wg      sync.WaitGroup

	connectionMu sync.Mutex
	connection   *physicalConnection

	identityMu sync.RWMutex
	playerID   string
	playerName string
	token      string

	stateMu sync.RWMutex
	state   clientState

	pendingMu sync.Mutex
	pending   map[string]chan wireEvent

	settlementMu sync.Mutex
	settlements  map[string]struct{}
}

func connectGameClient(parent context.Context, cfg config, run *runState, index int) (*gameClient, time.Duration, error) {
	started := time.Now()
	ctx, cancel := context.WithCancel(parent)
	client := &gameClient{
		index:       index,
		cfg:         cfg,
		run:         run,
		ctx:         ctx,
		cancel:      cancel,
		inbox:       make(chan incomingEvent, 256),
		actions:     make(chan clientAction, 16),
		pending:     make(map[string]chan wireEvent),
		settlements: make(map[string]struct{}),
	}
	physical, err := dialNegotiatedConnection(ctx, cfg, client.inbox, index)
	if err != nil {
		cancel()
		return nil, time.Since(started), err
	}
	connected, err := awaitSynchronousEvent(physical.conn, cfg.OperationTimeout, protocol.MsgConnected, "")
	if err != nil {
		physical.close(true)
		cancel()
		return nil, time.Since(started), fmt.Errorf("await connected: %w", err)
	}
	payload, err := codec.ParsePayload[protocol.ConnectedPayload](connected.message())
	if err != nil || payload.PlayerID == "" || payload.ReconnectToken == "" {
		physical.close(true)
		cancel()
		if err != nil {
			return nil, time.Since(started), fmt.Errorf("decode connected: %w", err)
		}
		return nil, time.Since(started), errors.New("connected response omitted reconnect identity")
	}
	client.playerID = payload.PlayerID
	client.playerName = payload.PlayerName
	client.token = payload.ReconnectToken
	_ = physical.conn.SetReadDeadline(time.Time{})
	_ = physical.conn.SetWriteDeadline(time.Time{})
	client.connection = physical
	client.wg.Add(2)
	go client.eventLoop()
	go client.actionLoop()
	physical.start()
	return client, time.Since(started), nil
}

func awaitSynchronousEvent(conn *websocket.Conn, timeout time.Duration, expected protocol.MessageType, requestID string) (wireEvent, error) {
	for {
		message, err := readProtocolMessage(conn, timeout)
		if err != nil {
			return wireEvent{}, err
		}
		event := eventFromMessage(message)
		codec.PutMessage(message)
		if event.messageType == protocol.MsgError && (requestID == "" || event.command == nil || event.command.RequestID == requestID) {
			return wireEvent{}, protocolEventError(event)
		}
		if event.messageType == expected && (requestID == "" || event.command != nil && event.command.RequestID == requestID) {
			return event, nil
		}
	}
}

func protocolEventError(event wireEvent) error {
	payload, err := codec.ParsePayload[protocol.ErrorPayload](event.message())
	if err != nil {
		return fmt.Errorf("server returned undecodable error: %w", err)
	}
	return &serverCommandError{Code: payload.Code, Command: payload.CommandType, Message: payload.Message}
}

type serverCommandError struct {
	Code    int
	Command protocol.MessageType
	Message string
}

func (e *serverCommandError) Error() string {
	return fmt.Sprintf("server error %d for %s: %s", e.Code, e.Command, e.Message)
}

func (c *gameClient) eventLoop() {
	defer c.wg.Done()
	for {
		select {
		case incoming := <-c.inbox:
			if !c.isCurrentConnection(incoming.connection) {
				continue
			}
			if incoming.err != nil {
				c.run.recordError("client %d connection failed: %v", c.index, incoming.err)
				if gameID := c.currentGameID(); gameID != "" && c.room != nil {
					c.room.failGame(gameID)
				}
				continue
			}
			c.handleEvent(incoming.event)
			c.resolvePending(incoming.event)
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *gameClient) isCurrentConnection(connection *physicalConnection) bool {
	c.connectionMu.Lock()
	defer c.connectionMu.Unlock()
	return c.connection == connection
}

func (c *gameClient) handleEvent(event wireEvent) {
	switch event.messageType {
	case protocol.MsgGameStart:
		c.handleGameStart(event)
	case protocol.MsgDealCards:
		c.handleDealCards(event)
	case protocol.MsgBidTurn:
		c.handleBidTurn(event)
	case protocol.MsgPlayTurn:
		c.handlePlayTurn(event)
	case protocol.MsgCardPlayed:
		c.handleCardPlayed(event)
	case protocol.MsgPlayerPass:
		c.handlePlayerPass(event)
	case protocol.MsgGameOver:
		c.handleGameOver(event)
	}
}

func (c *gameClient) handleGameStart(event wireEvent) {
	if event.event == nil || event.event.GameID == "" {
		c.run.recordError("client %d received game_start without game metadata", c.index)
		return
	}
	c.stateMu.Lock()
	c.state = clientState{gameID: event.event.GameID, turnID: event.event.TurnID}
	c.stateMu.Unlock()
	if c.room != nil {
		c.room.startGame(event.event.GameID)
	}
}

func (c *gameClient) handleDealCards(event wireEvent) {
	payload, err := codec.ParsePayload[protocol.DealCardsPayload](event.message())
	if err != nil {
		c.run.recordError("client %d could not decode deal: %v", c.index, err)
		return
	}
	c.stateMu.Lock()
	c.state.hand = convert.InfosToCards(payload.Cards)
	if event.event != nil {
		c.state.gameID = event.event.GameID
		c.state.turnID = event.event.TurnID
	}
	c.stateMu.Unlock()
}

func (c *gameClient) handleBidTurn(event wireEvent) {
	payload, err := codec.ParsePayload[protocol.BidTurnPayload](event.message())
	if err != nil || event.event == nil || event.event.GameID == "" || event.event.TurnID <= 0 {
		c.run.recordError("client %d received invalid bid turn", c.index)
		return
	}
	c.stateMu.Lock()
	c.state.gameID = event.event.GameID
	c.state.turnID = event.event.TurnID
	c.state.currentTurn = payload.PlayerID
	c.state.isGrab = payload.IsGrab
	c.stateMu.Unlock()
	if payload.PlayerID == c.id() {
		c.queueAction(clientAction{kind: protocol.MsgBid, gameID: event.event.GameID, turnID: event.event.TurnID})
	}
}

func (c *gameClient) handlePlayTurn(event wireEvent) {
	payload, err := codec.ParsePayload[protocol.PlayTurnPayload](event.message())
	if err != nil || event.event == nil || event.event.GameID == "" || event.event.TurnID <= 0 {
		c.run.recordError("client %d received invalid play turn", c.index)
		return
	}
	c.stateMu.Lock()
	c.state.gameID = event.event.GameID
	c.state.turnID = event.event.TurnID
	c.state.currentTurn = payload.PlayerID
	c.state.mustPlay = payload.MustPlay
	c.state.canBeat = payload.CanBeat
	c.stateMu.Unlock()
	if payload.PlayerID == c.id() {
		c.queueAction(clientAction{kind: protocol.MsgPlayCards, gameID: event.event.GameID, turnID: event.event.TurnID})
	}
}

func (c *gameClient) handleCardPlayed(event wireEvent) {
	payload, err := codec.ParsePayload[protocol.CardPlayedPayload](event.message())
	if err != nil {
		c.run.recordError("client %d could not decode card play: %v", c.index, err)
		return
	}
	played := convert.InfosToCards(payload.Cards)
	parsed, err := rule.ParseHand(played)
	if err != nil {
		c.run.recordError("client %d observed an illegal server play", c.index)
		return
	}
	c.stateMu.Lock()
	if payload.PlayerID == c.id() {
		c.state.hand = removeCards(c.state.hand, played)
	}
	c.state.lastPlayed = parsed
	c.state.passCount = 0
	if event.event != nil {
		c.state.gameID = event.event.GameID
		c.state.turnID = event.event.TurnID
	}
	c.stateMu.Unlock()
}

func (c *gameClient) handlePlayerPass(event wireEvent) {
	c.stateMu.Lock()
	c.state.passCount++
	if c.state.passCount >= 2 {
		c.state.lastPlayed = rule.ParsedHand{}
		c.state.passCount = 0
	}
	if event.event != nil {
		c.state.gameID = event.event.GameID
		c.state.turnID = event.event.TurnID
	}
	c.stateMu.Unlock()
}

func (c *gameClient) handleGameOver(event wireEvent) {
	if event.event == nil || event.event.GameID == "" {
		c.run.recordError("client %d received game_over without game metadata", c.index)
		return
	}
	c.settlementMu.Lock()
	_, duplicate := c.settlements[event.event.GameID]
	c.settlements[event.event.GameID] = struct{}{}
	c.settlementMu.Unlock()
	if duplicate {
		c.run.recordDuplicateSettlement()
		return
	}
	c.stateMu.Lock()
	c.state.currentTurn = ""
	c.state.turnID = event.event.TurnID
	c.stateMu.Unlock()
	if c.room == nil {
		return
	}
	rematch := c.room.finishGame(event.event.GameID)
	if rematch {
		c.queueAction(clientAction{kind: protocol.MsgReady, gameID: event.event.GameID})
	}
}

func removeCards(hand, played []card.Card) []card.Card {
	remaining := slices.Clone(hand)
	for _, current := range played {
		for index, candidate := range remaining {
			if candidate.Suit == current.Suit && candidate.Rank == current.Rank && candidate.Color == current.Color {
				remaining = append(remaining[:index], remaining[index+1:]...)
				break
			}
		}
	}
	return remaining
}

func (c *gameClient) queueAction(action clientAction) {
	select {
	case c.actions <- action:
	case <-c.ctx.Done():
	}
}

func (c *gameClient) actionLoop() {
	defer c.wg.Done()
	for {
		select {
		case action := <-c.actions:
			c.handleAction(action)
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *gameClient) handleAction(action clientAction) {
	if action.kind == protocol.MsgReady {
		defer c.room.readyDone(action.gameID)
		// GameOver is delivered before the room is reopened for ready-up.
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-timer.C:
		case <-c.ctx.Done():
			timer.Stop()
			return
		}
		_, latency, err := c.command(c.ctx, protocol.MsgReady, protocol.MsgPlayerReady, nil, "", 0)
		c.run.recordLatency(latency)
		if err != nil {
			c.run.recordError("client %d rematch ready failed: %v", c.index, err)
			c.room.failGame(action.gameID)
		}
		return
	}
	pace := time.NewTimer(publicTestActionDelay)
	select {
	case <-pace.C:
	case <-c.ctx.Done():
		pace.Stop()
		return
	}

	if c.run.shouldDisconnect() {
		latency, err := c.reconnect(c.ctx)
		c.run.recordReconnect(latency, err)
		if err != nil {
			c.run.recordError("client %d reconnect failed: %v", c.index, err)
			c.room.failGame(action.gameID)
			return
		}
	}

	snapshot := c.stateSnapshot()
	if snapshot.gameID != action.gameID || snapshot.turnID != action.turnID || snapshot.currentTurn != c.id() {
		return
	}

	var commandType, responseType protocol.MessageType
	var payload any
	switch action.kind {
	case protocol.MsgBid:
		commandType = protocol.MsgBid
		responseType = protocol.MsgBidResult
		payload = protocol.BidPayload{Bid: !snapshot.isGrab}
	case protocol.MsgPlayCards:
		cards := legalCards(snapshot)
		if len(cards) == 0 {
			if snapshot.mustPlay {
				c.run.recordError("client %d could not produce a mandatory legal play", c.index)
				c.room.failGame(action.gameID)
				return
			}
			commandType = protocol.MsgPass
			responseType = protocol.MsgPlayerPass
		} else {
			commandType = protocol.MsgPlayCards
			responseType = protocol.MsgCardPlayed
			payload = protocol.PlayCardsPayload{Cards: convert.CardsToInfos(cards)}
		}
	default:
		return
	}

	_, latency, err := c.command(c.ctx, commandType, responseType, payload, action.gameID, action.turnID)
	c.run.recordLatency(latency)
	if err != nil {
		c.run.recordError("client %d %s failed for game %s turn %d: %v", c.index, commandType, action.gameID, action.turnID, err)
		c.room.failGame(action.gameID)
	}
}

func legalCards(state clientState) []card.Card {
	if len(state.hand) == 0 {
		return nil
	}
	if state.mustPlay || state.lastPlayed.IsEmpty() {
		return rule.FindSmallestBeatingCards(state.hand, rule.ParsedHand{})
	}
	return rule.FindSmallestBeatingCards(state.hand, state.lastPlayed)
}

func (c *gameClient) stateSnapshot() clientState {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	copy := c.state
	copy.hand = slices.Clone(c.state.hand)
	return copy
}

func (c *gameClient) currentGameID() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.gameID
}

func (c *gameClient) command(
	ctx context.Context,
	commandType, responseType protocol.MessageType,
	payload any,
	expectedGameID string,
	expectedTurnID int64,
) (wireEvent, time.Duration, error) {
	started := time.Now()
	requestID := nextRequestID(string(commandType), c.index)
	response := make(chan wireEvent, 1)
	c.pendingMu.Lock()
	c.pending[requestID] = response
	c.pendingMu.Unlock()
	defer c.removePending(requestID)

	message, err := codec.NewMessage(commandType, payload)
	if err != nil {
		return wireEvent{}, time.Since(started), err
	}
	message.Command = &protocol.CommandMeta{
		RequestID:      requestID,
		ExpectedGameID: expectedGameID,
		ExpectedTurnID: expectedTurnID,
	}
	err = c.write(message)
	codec.PutMessage(message)
	if err != nil {
		return wireEvent{}, time.Since(started), err
	}

	timer := time.NewTimer(c.cfg.OperationTimeout)
	defer timer.Stop()
	select {
	case event := <-response:
		if event.messageType == protocol.MsgError {
			return wireEvent{}, time.Since(started), protocolEventError(event)
		}
		if event.messageType != responseType {
			return wireEvent{}, time.Since(started), fmt.Errorf("expected %s, got %s", responseType, event.messageType)
		}
		return event, time.Since(started), nil
	case <-timer.C:
		return wireEvent{}, time.Since(started), fmt.Errorf("timed out waiting for %s", responseType)
	case <-ctx.Done():
		return wireEvent{}, time.Since(started), ctx.Err()
	}
}

func (c *gameClient) write(message *protocol.Message) error {
	c.connectionMu.Lock()
	defer c.connectionMu.Unlock()
	if c.connection == nil {
		return errors.New("connection is unavailable")
	}
	return c.connection.write(message, c.cfg.OperationTimeout)
}

func (c *gameClient) resolvePending(event wireEvent) {
	if event.command == nil || event.command.RequestID == "" || event.messageType == protocol.MsgCommandAck {
		return
	}
	c.pendingMu.Lock()
	waiter := c.pending[event.command.RequestID]
	c.pendingMu.Unlock()
	if waiter == nil {
		return
	}
	select {
	case waiter <- event:
	default:
	}
}

func (c *gameClient) removePending(requestID string) {
	c.pendingMu.Lock()
	delete(c.pending, requestID)
	c.pendingMu.Unlock()
}

func (c *gameClient) reconnect(ctx context.Context) (time.Duration, error) {
	started := time.Now()
	c.connectionMu.Lock()
	defer c.connectionMu.Unlock()
	if c.connection == nil {
		return time.Since(started), errors.New("client is not connected")
	}
	oldConnection := c.connection
	c.connection = nil
	oldConnection.close(true)

	playerID, token := c.identity()
	physical, err := dialNegotiatedConnection(ctx, c.cfg, c.inbox, c.index)
	if err != nil {
		return time.Since(started), fmt.Errorf("dial replacement: %w", err)
	}
	fail := func(cause error) (time.Duration, error) {
		physical.close(true)
		return time.Since(started), cause
	}
	if _, err := awaitSynchronousEvent(physical.conn, c.cfg.OperationTimeout, protocol.MsgConnected, ""); err != nil {
		return fail(fmt.Errorf("await replacement connected: %w", err))
	}
	requestID := nextRequestID("reconnect", c.index)
	message, err := codec.NewMessage(protocol.MsgReconnect, protocol.ReconnectPayload{Token: token, PlayerID: playerID})
	if err != nil {
		return fail(err)
	}
	message.Command = &protocol.CommandMeta{RequestID: requestID}
	err = physical.write(message, c.cfg.OperationTimeout)
	codec.PutMessage(message)
	if err != nil {
		return fail(fmt.Errorf("write reconnect: %w", err))
	}
	event, err := awaitSynchronousEvent(physical.conn, c.cfg.OperationTimeout, protocol.MsgReconnected, requestID)
	if err != nil {
		return fail(fmt.Errorf("await reconnected: %w", err))
	}
	payload, err := codec.ParsePayload[protocol.ReconnectedPayload](event.message())
	if err != nil {
		return fail(fmt.Errorf("decode reconnected: %w", err))
	}
	if payload.PlayerID != playerID || payload.ReconnectToken == "" || payload.ReconnectToken == token {
		return fail(errors.New("reconnect identity or rotated token was invalid"))
	}
	c.identityMu.Lock()
	c.playerID = payload.PlayerID
	c.playerName = payload.PlayerName
	c.token = payload.ReconnectToken
	c.identityMu.Unlock()
	c.applyGameState(payload.GameState)
	_ = physical.conn.SetReadDeadline(time.Time{})
	_ = physical.conn.SetWriteDeadline(time.Time{})
	c.connection = physical
	physical.start()
	return time.Since(started), nil
}

func (c *gameClient) applyGameState(snapshot *protocol.GameStateDTO) {
	if snapshot == nil {
		return
	}
	state := clientState{
		hand:        convert.InfosToCards(snapshot.Hand),
		gameID:      snapshot.GameID,
		turnID:      snapshot.TurnID,
		currentTurn: snapshot.CurrentTurn,
		mustPlay:    snapshot.MustPlay,
		canBeat:     snapshot.CanBeat,
		isGrab:      snapshot.IsGrab,
	}
	if len(snapshot.LastPlayed) > 0 {
		if parsed, err := rule.ParseHand(convert.InfosToCards(snapshot.LastPlayed)); err == nil {
			state.lastPlayed = parsed
		}
	}
	c.stateMu.Lock()
	c.state = state
	c.stateMu.Unlock()
}

func (c *gameClient) identity() (string, string) {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.playerID, c.token
}

func (c *gameClient) id() string {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.playerID
}

func (c *gameClient) name() string {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.playerName
}

func (c *gameClient) createRoom(ctx context.Context) (string, time.Duration, error) {
	event, latency, err := c.command(ctx, protocol.MsgCreateRoom, protocol.MsgRoomCreated, nil, "", 0)
	if err != nil {
		return "", latency, err
	}
	payload, err := codec.ParsePayload[protocol.RoomCreatedPayload](event.message())
	if err != nil || payload.RoomCode == "" {
		if err != nil {
			return "", latency, err
		}
		return "", latency, errors.New("server returned an empty room code")
	}
	return payload.RoomCode, latency, nil
}

func (c *gameClient) joinRoom(ctx context.Context, roomCode string) (time.Duration, error) {
	_, latency, err := c.command(ctx, protocol.MsgJoinRoom, protocol.MsgRoomJoined, protocol.JoinRoomPayload{RoomCode: roomCode}, "", 0)
	return latency, err
}

func (c *gameClient) ready(ctx context.Context) (time.Duration, error) {
	_, latency, err := c.command(ctx, protocol.MsgReady, protocol.MsgPlayerReady, nil, "", 0)
	return latency, err
}

func (c *gameClient) leaveRoom(ctx context.Context) (time.Duration, error) {
	_, latency, err := c.command(ctx, protocol.MsgLeaveRoom, protocol.MsgRoomLeft, nil, "", 0)
	return latency, err
}

func (c *gameClient) stats(ctx context.Context) (protocol.StatsResultPayload, time.Duration, error) {
	event, latency, err := c.command(ctx, protocol.MsgGetStats, protocol.MsgStatsResult, nil, "", 0)
	if err != nil {
		return protocol.StatsResultPayload{}, latency, err
	}
	payload, err := codec.ParsePayload[protocol.StatsResultPayload](event.message())
	if err != nil {
		return protocol.StatsResultPayload{}, latency, err
	}
	return *payload, latency, nil
}

func (c *gameClient) leaderboard(ctx context.Context) (protocol.LeaderboardResultPayload, time.Duration, error) {
	event, latency, err := c.command(ctx, protocol.MsgGetLeaderboard, protocol.MsgLeaderboardResult, protocol.GetLeaderboardPayload{
		Type: "total", Limit: 50,
	}, "", 0)
	if err != nil {
		return protocol.LeaderboardResultPayload{}, latency, err
	}
	payload, err := codec.ParsePayload[protocol.LeaderboardResultPayload](event.message())
	if err != nil {
		return protocol.LeaderboardResultPayload{}, latency, err
	}
	return *payload, latency, nil
}

func (c *gameClient) roomList(ctx context.Context) (protocol.RoomListResultPayload, time.Duration, error) {
	event, latency, err := c.command(ctx, protocol.MsgGetRoomList, protocol.MsgRoomListResult, nil, "", 0)
	if err != nil {
		return protocol.RoomListResultPayload{}, latency, err
	}
	payload, err := codec.ParsePayload[protocol.RoomListResultPayload](event.message())
	if err != nil {
		return protocol.RoomListResultPayload{}, latency, err
	}
	return *payload, latency, nil
}

func (c *gameClient) close() {
	c.connectionMu.Lock()
	physical := c.connection
	c.connection = nil
	c.connectionMu.Unlock()
	if physical != nil {
		physical.close(false)
	}
	c.cancel()
	c.wg.Wait()
}

func nextRequestID(operation string, clientIndex int) string {
	return fmt.Sprintf("public:%s:%d:%d", operation, clientIndex, publicTestRequestSequence.Add(1))
}
