package session

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/palemoky/fight-the-landlord/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBotSubmissionsRejectOldTurnForSamePlayer(t *testing.T) {
	for _, action := range []string{"play", "pass", "bid"} {
		t.Run(action, func(t *testing.T) {
			gs := recordingGame(t)
			gs.stopTimer()
			gs.setLandlord(0)
			player := gs.players[0]
			oldTurn := gs.turnID
			// Advance a whole circuit: the original player is current again.
			if action == "bid" {
				gs.stopTimer()
				gs.state = GameStateBidding
				gs.currentBidder = 0
				gs.landlordCaller = -1
				gs.landlordCandidate = -1
				gs.notifyBidTurn()
				oldTurn = gs.turnID
				require.NoError(t, gs.HandleBid(player.ID, true))
				for range 2 {
					require.NoError(t, gs.HandleBid(gs.players[gs.currentBidder].ID, true))
				}
				require.Equal(t, player.ID, gs.players[gs.currentBidder].ID)
			} else {
				for range 3 {
					gs.handleTurnTimeout(gs.turnID, "timeout")
				}
				require.Equal(t, player.ID, gs.players[gs.currentPlayer].ID)
				if action == "pass" {
					gs.lastPlayedHand, _ = rule.ParseHand(cardsForRanks(t, "2"))
					gs.lastPlayerIdx = 2
				}
			}
			before, turn := gs.recording.sequence, gs.turnID
			submit := func(ctx context.Context, id uint64) error {
				if action == "bid" {
					return gs.HandleBotBid(ctx, player.ID, id, false)
				}
				if action == "pass" {
					return gs.HandleBotPlay(ctx, player.ID, id, nil)
				}
				return gs.HandleBotPlay(ctx, player.ID, id, convert.CardsToInfos(player.Hand[:1]))
			}
			require.ErrorIs(t, submit(context.Background(), oldTurn), apperrors.ErrNotYourTurn)
			require.Equal(t, before, gs.recording.sequence)
			require.Equal(t, turn, gs.turnID)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, submit(ctx, turn), context.Canceled)
			require.Equal(t, before, gs.recording.sequence)
			gs.StopAllTimers()
			require.Error(t, submit(context.Background(), turn))
		})
	}
}

type timedClient struct {
	*testutil.SimpleClient
	playTimes []time.Time
}

func (c *timedClient) SendMessage(msg *protocol.Message) {
	c.SimpleClient.SendMessage(msg)
	if msg.Type == protocol.MsgCardPlayed || msg.Type == protocol.MsgPlayerPass {
		c.playTimes = append(c.playTimes, time.Now())
	}
}

// Use the actual session and bots, including synchronous broadcasts while gs.mu
// is held, to catch deadlocks and verify practice/fill share the same pacing.
func TestTwoBotsAndHumanTimeout(t *testing.T) {
	for _, pass := range []bool{false, true} {
		t.Run(map[bool]string{false: "plays", true: "passes"}[pass], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				human := &timedClient{SimpleClient: testutil.NewSimpleClient("human", "Human")}
				bots := []*bot.BotClient{bot.NewBotClient(bot.NewHeuristicEngine()), bot.NewBotClient(bot.NewHeuristicEngine())}
				defer bots[0].Close()
				defer bots[1].Close()
				r := room.NewMockRoom("BOT", human)
				clients := []types.ClientInterface{human, bots[0], bots[1]}
				r.PlayerOrder = nil
				for seat, c := range clients {
					r.Players[c.GetID()] = &room.RoomPlayer{Client: c, Seat: seat}
					r.PlayerOrder = append(r.PlayerOrder, c.GetID())
				}
				gs := NewGameSession(r, nil, config.GameConfig{TurnTimeout: 60, BidTimeout: 15})
				defer gs.StopAllTimers()
				for _, b := range bots {
					b.SetSession(gs)
				}
				r.Broadcast(codec.MustNewMessage(protocol.MsgGameStart, protocol.GameStartPayload{Players: r.GetAllPlayersInfo()}))
				hands := []string{"76", "98", "JT"}
				if pass {
					hands[0] = "22"
				}
				for i, p := range gs.players {
					p.Hand = cardsForRanks(t, hands[i])
					clients[i].SendMessage(codec.MustNewMessage(protocol.MsgDealCards, protocol.DealCardsPayload{Cards: convert.CardsToInfos(p.Hand)}))
				}
				gs.state = GameStatePlaying
				gs.notifyPlayTurn()
				start := time.Now()
				gs.handleTurnTimeout(gs.turnID, "timeout")
				require.Equal(t, start, time.Now(), "human automatic operation gets no artificial wait")
				gs.mu.RLock()
				require.Equal(t, 1, gs.currentPlayer)
				gs.mu.RUnlock()
				synctest.Wait()
				time.Sleep(time.Second - time.Nanosecond)
				synctest.Wait()
				gs.mu.RLock()
				require.Len(t, human.playTimes, 1)
				gs.mu.RUnlock()
				time.Sleep(3*time.Second + time.Nanosecond)
				synctest.Wait()
				gs.mu.RLock()
				defer gs.mu.RUnlock()
				require.Len(t, human.playTimes, 3)
				require.Equal(t, 0, gs.currentPlayer)
				for i := 1; i <= 2; i++ {
					elapsed := human.playTimes[i].Sub(human.playTimes[i-1])
					require.GreaterOrEqual(t, elapsed, time.Second)
					require.LessOrEqual(t, elapsed, 2*time.Second)
				}
				require.Equal(t, 60*time.Second, gs.remainingTime)
			})
		})
	}
}

// Capture the in-process token to verify every session exit invalidates bot work.
type turnObserver struct {
	*testutil.SimpleClient
	ctx    context.Context
	turnID uint64
}

func (c *turnObserver) SendTurn(ctx context.Context, turnID uint64, _ *protocol.Message) {
	c.ctx, c.turnID = ctx, turnID
}

func TestSessionCancelsBotWork(t *testing.T) {
	for _, action := range []string{"advance", "reschedule", "stop", "game over", "offline"} {
		t.Run(action, func(t *testing.T) {
			gs := recordingGame(t)
			gs.stopTimer()
			p := gs.players[0]
			observer := &turnObserver{SimpleClient: testutil.NewSimpleClient(p.ID, p.Name)}
			gs.room.Players[p.ID].Client = observer
			gs.setLandlord(0)
			ctx, turnID := observer.ctx, observer.turnID
			require.NoError(t, ctx.Err())
			require.Equal(t, gs.turnID, turnID)
			switch action {
			case "advance":
				gs.handleTurnTimeout(turnID, "timeout")
			case "reschedule":
				gs.notifyPlayTurn()
			case "stop":
				gs.StopAllTimers()
			case "game over":
				gs.endGame(p)
			case "offline":
				gs.PlayerOffline(p.ID)
			}
			require.ErrorIs(t, ctx.Err(), context.Canceled)
		})
	}
}
