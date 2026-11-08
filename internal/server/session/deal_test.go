package session

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/stretchr/testify/require"
)

type dealBotProbe struct {
	*testutil.SimpleClient
	turns int
}

func (b *dealBotProbe) SendTurn(_ context.Context, _ uint64, msg *protocol.Message) {
	b.turns++
	b.SendMessage(msg)
}

func newDealingGame(t *testing.T) (*GameSession, *dealBotProbe) {
	t.Helper()
	b := &dealBotProbe{SimpleClient: testutil.NewSimpleClient("a", "A")}
	r := room.NewMockRoom("DEAL", b)
	for i, id := range []string{"b", "c"} {
		r.Players[id] = &room.RoomPlayer{Client: testutil.NewSimpleClient(id, id), Seat: i + 1}
	}
	r.PlayerOrder = []string{"a", "b", "c"}
	gs := NewGameSession(r, nil, config.GameConfig{BidTimeout: 15, TurnTimeout: 30})
	gs.Start()
	return gs, b
}

func TestDealDeadlineRedealAndStaleCallbacks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gs, b := newDealingGame(t)
		defer gs.StopAllTimers()
		var oldDeal, oldToken string
		var oldTurn uint64
		for range 6 {
			require.Equal(t, GameStateDealing, gs.state)
			require.Equal(t, RoomStateDealing, gs.room.GetState())
			require.Nil(t, gs.turnTimer)
			require.Empty(t, gs.bidToken)
			require.Error(t, gs.HandleBid(gs.players[gs.currentBidder].ID, true))
			require.Error(t, gs.HandleBidTurn(gs.players[gs.currentBidder].ID, true, oldToken))
			require.Error(t, gs.HandleBotBid(context.Background(), "a", gs.turnID, true))
			botTurns := b.turns
			gs.Start() // A duplicate start must not restart the stage.
			id, end := gs.dealID, gs.dealEndAt
			if oldDeal != "" {
				gs.finishDeal(oldDeal)
				gs.handleTurnTimeout(oldTurn, "timeout")
			}
			require.Equal(t, id, gs.dealID)
			advanceDealTime(t, gs, DealDuration-time.Millisecond)
			require.Equal(t, GameStateDealing, gs.state)
			require.Equal(t, botTurns, b.turns)
			require.Nil(t, gs.turnTimer)
			advanceDealTime(t, gs, time.Millisecond)
			require.Equal(t, GameStateBidding, gs.state)
			require.Equal(t, end, gs.timerStartTime)
			require.Equal(t, 15*time.Second, gs.remainingTime)
			require.Equal(t, botTurns+1, b.turns)
			timer, turn := gs.turnTimer, gs.turnID
			gs.finishDeal(id)
			require.Same(t, timer, gs.turnTimer)
			require.Equal(t, turn, gs.turnID)
			require.Equal(t, botTurns+1, b.turns)
			require.Error(t, gs.HandleBidTurn(gs.players[gs.currentBidder].ID, true, oldToken))
			oldDeal, oldToken, oldTurn = id, gs.bidToken, gs.turnID
			for range 3 {
				require.NoError(t, gs.HandleBidTurn(gs.players[gs.currentBidder].ID, false, gs.bidToken))
			}
			require.NotEqual(t, id, gs.dealID)
		}
	})
}

func TestDealReconnectKeepsDeadlineAndHidesBottomCards(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gs, b := newDealingGame(t)
		defer gs.StopAllTimers()
		advanceDealTime(t, gs, 2*time.Second)
		start, end, timer := gs.dealStartAt, gs.dealEndAt, gs.dealTimer
		gs.PlayerOffline("a")
		gs.PlayerOnline("a")
		sm := &SessionManager{sessions: map[string]*PlayerSession{}}
		dto := gs.BuildGameStateDTO("a", sm)
		require.Equal(t, "dealing", dto.Phase)
		require.Empty(t, dto.BottomCards)
		require.Empty(t, dto.CurrentTurn)
		require.Equal(t, gs.dealID, dto.DealID)
		require.Equal(t, start.UnixMilli(), dto.DealStartAt)
		require.Equal(t, end.UnixMilli(), dto.DealEndAt)
		require.Equal(t, int64(1870), dto.DealEndAt-dto.ServerTime)
		gs.ResendTurnTo(b)
		require.Zero(t, b.turns)
		require.Same(t, timer, gs.dealTimer)
		advanceDealTime(t, gs, 1870*time.Millisecond)
		require.Equal(t, GameStateBidding, gs.state)
		require.Equal(t, end, gs.timerStartTime)
		require.Len(t, b.Messages, 2)
		payload, err := codec.ParsePayload[protocol.DealCardsPayload](b.Messages[0])
		require.NoError(t, err)
		require.Equal(t, dto.DealID, payload.DealID)
		require.Equal(t, dto.DealStartAt, payload.DealStartAt)
	})
}

func TestDealCancelledOnCleanupAndRoomEnd(t *testing.T) {
	for _, action := range []string{"cleanup", "game_end", "room_end"} {
		t.Run(action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				gs, b := newDealingGame(t)
				defer gs.StopAllTimers()
				id := gs.dealID
				switch action {
				case "cleanup":
					gs.StopAllTimers()
				case "game_end":
					gs.mu.Lock()
					gs.endGame(gs.players[0])
					gs.mu.Unlock()
				case "room_end":
					gs.room.SetState(RoomStateEnded)
				}
				advanceDealTime(t, gs, DealDuration)
				gs.finishDeal(id)
				require.Zero(t, b.turns)
				require.Nil(t, gs.turnTimer)
				if action != "room_end" {
					require.Nil(t, gs.dealTimer)
				}
			})
		})
	}
}

// Sleep alone does not establish happens-before for reads preceding a timer callback.
func advanceDealTime(t *testing.T, gs *GameSession, duration time.Duration) {
	t.Helper()
	gs.mu.RLock()
	id := gs.dealID
	gs.mu.RUnlock()
	require.NotEmpty(t, id)
	time.Sleep(duration)
	synctest.Wait()
}

func TestHumanBidTokenCannotBeReusedWhenSamePlayerGrabs(t *testing.T) {
	gs := classicBidGame(t, 0)
	player, old := gs.players[0].ID, gs.bidToken
	require.NoError(t, gs.HandleBidTurn(player, true, old))
	for range 2 {
		require.NoError(t, gs.HandleBidTurn(gs.players[gs.currentBidder].ID, true, gs.bidToken))
	}
	require.Equal(t, player, gs.players[gs.currentBidder].ID)
	turn := gs.turnID
	require.Error(t, gs.HandleBidTurn(player, true, old))
	require.Equal(t, turn, gs.turnID)
	require.NoError(t, gs.HandleBidTurn(player, true, gs.bidToken))
	require.Equal(t, GameStatePlaying, gs.state)
}
