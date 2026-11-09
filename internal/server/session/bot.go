package session

import (
	"context"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

// broadcastBotTurn binds bot work to this exact turn before any goroutine starts.
// This is an in-process interface only; human clients receive the existing message.
// The caller holds gs.mu. SendTurn must only capture state and launch work, not wait.
func (gs *GameSession) broadcastBotTurn(msg *protocol.Message) {
	if gs.inTransition {
		gs.effect(func() { gs.broadcastBotTurn(msg) })
		return
	}
	gs.cancelBotTurn()
	ctx, cancel := context.WithCancel(context.Background())
	gs.botTurnCancel = cancel
	for _, player := range gs.room.Players {
		if bot, ok := player.Client.(interface {
			SendTurn(context.Context, uint64, *protocol.Message)
		}); ok {
			bot.SendTurn(ctx, gs.turnID, msg)
		} else {
			player.Client.SendMessage(msg)
		}
	}
}

func (gs *GameSession) cancelBotTurn() {
	if gs.botTurnCancel != nil {
		gs.botTurnCancel()
		gs.botTurnCancel = nil
	}
}

// HandleBotBid rejects stale decisions even when the same bot is current again.
func (gs *GameSession) HandleBotBid(ctx context.Context, playerID string, turnID uint64, bid bool) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if turnID != gs.turnID {
		return apperrors.ErrNotYourTurn
	}
	return gs.handleBidLocked(playerID, bid, "")
}

// HandleBotPlay checks the original turn and applies play/pass in one critical section.
func (gs *GameSession) HandleBotPlay(ctx context.Context, playerID string, turnID uint64, cards []protocol.CardInfo) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if turnID != gs.turnID {
		return apperrors.ErrNotYourTurn
	}
	if cards == nil {
		return gs.handlePassLocked(playerID, "")
	}
	return gs.handlePlayCardsLocked(playerID, cards, "")
}
