package bot

import (
	"context"
	"errors"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/observability"
)

type instrumentedEngine struct {
	next    DecisionEngine
	name    string
	metrics *observability.Metrics
}

// InstrumentEngine records bounded engine latency without changing decision
// semantics. Nil metrics leave the original engine untouched.
func InstrumentEngine(engine DecisionEngine, name string, metrics *observability.Metrics) DecisionEngine {
	if engine == nil || metrics == nil {
		return engine
	}
	return &instrumentedEngine{next: engine, name: name, metrics: metrics}
}

func (e *instrumentedEngine) DecideBid(ctx context.Context, botName string, hand []card.Card, previous *bool) bool {
	started := time.Now()
	result := e.next.DecideBid(ctx, botName, hand, previous)
	timedOut := e.name != "douzero" && errors.Is(ctx.Err(), context.DeadlineExceeded)
	e.metrics.ObserveBot(e.name, time.Since(started), timedOut)
	return result
}

func (e *instrumentedEngine) DecidePlay(ctx context.Context, botName string, gameContext GameContext) []card.Card {
	started := time.Now()
	result := e.next.DecidePlay(ctx, botName, gameContext)
	timedOut := e.name != "douzero" && errors.Is(ctx.Err(), context.DeadlineExceeded)
	e.metrics.ObserveBot(e.name, time.Since(started), timedOut)
	return result
}
