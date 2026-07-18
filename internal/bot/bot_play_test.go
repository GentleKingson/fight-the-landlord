package bot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/observability"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

func TestBotPlayTurnSubmitsValidatedActionOnceWithCapturedContext(t *testing.T) {
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card {
		return faultCards(card.Rank4)
	}}
	session := newBotPlaySession("game-1", 7)
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3, card.Rank4))

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 7, true, true))
	botClient.handlePlayTurn(playTurnMessage(botClient.id, 7, true, true))

	submission := session.submissions()
	assert.Equal(t, 1, engine.decisionCount())
	assert.Equal(t, 1, submission.plays)
	assert.Zero(t, submission.passes)
	assert.Equal(t, "game-1", submission.gameID)
	assert.EqualValues(t, 7, submission.turnID)
	require.Len(t, submission.played, 1)
	assert.Equal(t, int(card.Rank4), submission.played[0].Rank)
}

func TestBotPlayTurnUsesOneRuleFallbackForInvalidDecision(t *testing.T) {
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card {
		return faultCards(card.RankRedJoker)
	}}
	session := newBotPlaySession("game-1", 8)
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3, card.Rank4))

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 8, true, true))

	submission := session.submissions()
	assert.Equal(t, 1, submission.plays, "the invalid external action must never be submitted")
	assert.Zero(t, submission.passes)
	require.Len(t, submission.played, 1)
	assert.Equal(t, int(card.Rank3), submission.played[0].Rank)
	assert.Equal(t, []invalidActionReason{invalidActionNotOwned}, engine.recordedReasons())
}

func TestBotPlayTurnDoesNotRetryRejectedFallbackForInvalidDecision(t *testing.T) {
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card {
		return faultCards(card.RankRedJoker)
	}}
	session := newBotPlaySession("game-1", 8)
	session.rejections = []error{errors.New("fallback rejected")}
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3, card.Rank4))

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 8, true, true))

	submission := session.submissions()
	assert.Equal(t, 1, submission.plays)
	assert.Zero(t, submission.passes)
	assert.Equal(t, []invalidActionReason{
		invalidActionNotOwned,
		invalidActionSubmitRejected,
	}, engine.recordedReasons())
}

func TestBotPlayTurnDoesNotRetryRejectedDouZeroFallback(t *testing.T) {
	var serviceRequests atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		serviceRequests.Add(1)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"action":[30]}`))
	}))
	defer service.Close()

	metrics := observability.NewMetrics(true)
	douzero := NewDouZeroEngine(service.URL)
	douzero.SetMetrics(metrics)
	engine := InstrumentEngine(douzero, "douzero", metrics)
	session := newBotPlaySession("game-1", 8)
	session.rejections = []error{errors.New("fallback rejected")}
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3, card.Rank4))
	botClient.state.mu.Lock()
	botClient.state.douzeroPos = DouZeroPosLandlord
	botClient.state.mu.Unlock()

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 8, true, true))

	submission := session.submissions()
	assert.EqualValues(t, 1, serviceRequests.Load())
	assert.Equal(t, 1, submission.plays)
	assert.Zero(t, submission.passes)
	assertInvalidActionMetrics(t, metrics, map[invalidActionReason]float64{
		invalidActionNotOwned:       1,
		invalidActionSubmitRejected: 1,
	})
}

func TestBotPlayTurnDoesNotSubmitResultAfterTurnChanges(t *testing.T) {
	decisionStarted := make(chan struct{})
	releaseDecision := make(chan struct{})
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card {
		close(decisionStarted)
		<-releaseDecision
		return faultCards(card.Rank3)
	}}
	session := newBotPlaySession("game-1", 9)
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3, card.Rank4))

	done := make(chan struct{})
	go func() {
		defer close(done)
		botClient.handlePlayTurn(playTurnMessage(botClient.id, 9, true, true))
	}()
	<-decisionStarted
	session.setTurn("game-1", 10)
	close(releaseDecision)
	<-done

	submission := session.submissions()
	assert.Zero(t, submission.plays)
	assert.Zero(t, submission.passes)
	assert.Equal(t, []invalidActionReason{invalidActionStaleTurn}, engine.recordedReasons())
}

func TestBotPlayTurnSkipsAlreadyStaleEventBeforeCallingEngine(t *testing.T) {
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card {
		return faultCards(card.Rank3)
	}}
	session := newBotPlaySession("game-2", 1)
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3))

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 99, true, true))

	submission := session.submissions()
	assert.Zero(t, engine.decisionCount())
	assert.Zero(t, submission.plays)
	assert.Zero(t, submission.passes)
	assert.Equal(t, []invalidActionReason{invalidActionStaleTurn}, engine.recordedReasons())
}

func TestBotPlayTurnRetriesRejectedSubmitOnceWithRuleFallback(t *testing.T) {
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card {
		return faultCards(card.Rank4)
	}}
	session := newBotPlaySession("game-1", 11)
	session.rejections = []error{errors.New("submission rejected"), nil}
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3, card.Rank4))

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 11, true, true))

	submission := session.submissions()
	assert.Equal(t, 2, submission.plays)
	assert.Zero(t, submission.passes)
	assert.Equal(t, 1, engine.decisionCount())
	require.Len(t, submission.played, 1)
	assert.Equal(t, int(card.Rank3), submission.played[0].Rank)
	assert.Equal(t, []invalidActionReason{invalidActionSubmitRejected}, engine.recordedReasons())
}

func TestBotPlayTurnDoesNotLoopWhenFallbackSubmitIsRejected(t *testing.T) {
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card {
		return faultCards(card.Rank4)
	}}
	session := newBotPlaySession("game-1", 11)
	session.rejections = []error{errors.New("submission rejected"), errors.New("fallback rejected")}
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3, card.Rank4))

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 11, true, true))

	submission := session.submissions()
	assert.Equal(t, 2, submission.plays)
	assert.Zero(t, submission.passes)
	assert.Equal(t, 1, engine.decisionCount())
	assert.Equal(t, []invalidActionReason{invalidActionSubmitRejected}, engine.recordedReasons())
}

func TestBotPlayTurnNeverRetriesStaleSubmit(t *testing.T) {
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card {
		return faultCards(card.Rank4)
	}}
	session := newBotPlaySession("game-1", 11)
	session.rejections = []error{apperrors.ErrStaleTurn}
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3, card.Rank4))

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 11, true, true))

	submission := session.submissions()
	assert.Equal(t, 1, submission.plays)
	assert.Zero(t, submission.passes)
	assert.Equal(t, []invalidActionReason{invalidActionStaleTurn}, engine.recordedReasons())
}

func TestBotPlayTurnUsesEngineAgainOnLaterTurn(t *testing.T) {
	decision := 0
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card {
		decision++
		if decision == 1 {
			return faultCards(card.RankRedJoker)
		}
		return faultCards(card.Rank4)
	}}
	session := newBotPlaySession("game-1", 11)
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3, card.Rank4))

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 11, true, true))
	session.setTurn("game-1", 12)
	botClient.handlePlayTurn(playTurnMessage(botClient.id, 12, true, true))

	submission := session.submissions()
	assert.Equal(t, 2, engine.decisionCount())
	assert.Equal(t, 2, submission.plays)
	assert.Zero(t, submission.passes)
	assert.EqualValues(t, 12, submission.turnID)
	require.Len(t, submission.played, 1)
	assert.Equal(t, int(card.Rank4), submission.played[0].Rank)
	assert.Equal(t, []invalidActionReason{invalidActionNotOwned}, engine.recordedReasons())
}

func TestBotPlayTurnSubmitsPassAtCapturedContext(t *testing.T) {
	engine := &scriptedPlayEngine{play: func(GameContext) []card.Card { return nil }}
	session := newBotPlaySession("game-1", 12)
	botClient := newPlayTestBot(engine, session, faultCards(card.Rank3))

	botClient.handlePlayTurn(playTurnMessage(botClient.id, 12, false, false))

	submission := session.submissions()
	assert.Zero(t, submission.plays)
	assert.Equal(t, 1, submission.passes)
	assert.Equal(t, "game-1", submission.gameID)
	assert.EqualValues(t, 12, submission.turnID)
}

func TestInstrumentedDouZeroForwardsTurnAndSubmitReasons(t *testing.T) {
	metrics := observability.NewMetrics(true)
	douzero := NewDouZeroEngine("http://unused.invalid")
	douzero.SetMetrics(metrics)
	engine := InstrumentEngine(douzero, "douzero", metrics)

	recordInvalidAction(engine, invalidActionStaleTurn)
	recordInvalidAction(engine, invalidActionSubmitRejected)

	assertInvalidActionMetrics(t, metrics, map[invalidActionReason]float64{
		invalidActionStaleTurn:      1,
		invalidActionSubmitRejected: 1,
	})
}

func newPlayTestBot(engine DecisionEngine, session SessionInterface, hand []card.Card) *BotClient {
	botClient := NewBotClient(engine)
	botClient.playDelay = nil
	botClient.SetSession(session)
	botClient.state.mu.Lock()
	botClient.state.hand = append([]card.Card(nil), hand...)
	botClient.state.mu.Unlock()
	return botClient
}

func playTurnMessage(playerID string, turnID int64, mustPlay, canBeat bool) *protocol.Message {
	message := codec.MustNewMessage(protocol.MsgPlayTurn, protocol.PlayTurnPayload{
		PlayerID: playerID,
		MustPlay: mustPlay,
		CanBeat:  canBeat,
	})
	message.Event = &protocol.EventMeta{GameID: "game-1", TurnID: turnID}
	return message
}

type scriptedPlayEngine struct {
	mu        sync.Mutex
	play      func(GameContext) []card.Card
	decisions int
	reasons   []invalidActionReason
}

func (e *scriptedPlayEngine) DecideBid(context.Context, string, []card.Card, *bool) bool {
	return false
}

func (e *scriptedPlayEngine) DecidePlay(_ context.Context, _ string, gameContext GameContext) []card.Card {
	e.mu.Lock()
	e.decisions++
	e.mu.Unlock()
	return e.play(gameContext)
}

func (e *scriptedPlayEngine) RecordInvalidAction(reason invalidActionReason) {
	e.mu.Lock()
	e.reasons = append(e.reasons, reason)
	e.mu.Unlock()
}

func (e *scriptedPlayEngine) decisionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.decisions
}

func (e *scriptedPlayEngine) recordedReasons() []invalidActionReason {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]invalidActionReason(nil), e.reasons...)
}

type botPlaySession struct {
	mu         sync.Mutex
	gameID     string
	turnID     int64
	playerID   string
	rejections []error
	plays      int
	passes     int
	gotGame    string
	gotTurn    int64
	played     []protocol.CardInfo
}

type botPlaySubmissions struct {
	plays  int
	passes int
	gameID string
	turnID int64
	played []protocol.CardInfo
}

func newBotPlaySession(gameID string, turnID int64) *botPlaySession {
	return &botPlaySession{gameID: gameID, turnID: turnID}
}

func (s *botPlaySession) HandleBid(string, bool) error { return nil }

func (s *botPlaySession) IsCurrentPlayTurn(playerID, gameID string, turnID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playerID == "" {
		s.playerID = playerID
	}
	return s.playerID == playerID && s.gameID == gameID && s.turnID == turnID
}

func (s *botPlaySession) HandlePlayCardsAt(_ string, cards []protocol.CardInfo, gameID string, turnID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plays++
	s.gotGame = gameID
	s.gotTurn = turnID
	s.played = append([]protocol.CardInfo(nil), cards...)
	return s.nextRejectionLocked()
}

func (s *botPlaySession) HandlePassAt(_, gameID string, turnID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passes++
	s.gotGame = gameID
	s.gotTurn = turnID
	return s.nextRejectionLocked()
}

func (s *botPlaySession) nextRejectionLocked() error {
	if len(s.rejections) == 0 {
		return nil
	}
	rejection := s.rejections[0]
	s.rejections = s.rejections[1:]
	return rejection
}

func (s *botPlaySession) setTurn(gameID string, turnID int64) {
	s.mu.Lock()
	s.gameID = gameID
	s.turnID = turnID
	s.mu.Unlock()
}

func (s *botPlaySession) submissions() botPlaySubmissions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return botPlaySubmissions{
		plays:  s.plays,
		passes: s.passes,
		gameID: s.gotGame,
		turnID: s.gotTurn,
		played: append([]protocol.CardInfo(nil), s.played...),
	}
}
