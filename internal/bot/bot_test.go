package bot

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/stretchr/testify/require"
)

type decisionEngine struct {
	play func(context.Context, GameContext) []card.Card
	bid  func(context.Context) bool
}

func (e decisionEngine) DecidePlay(ctx context.Context, _ string, g GameContext) []card.Card {
	return e.play(ctx, g)
}
func (e decisionEngine) DecideBid(ctx context.Context, _ string, _ []card.Card, _ *bool) bool {
	return e.bid(ctx)
}

type botSubmission struct {
	turn  uint64
	at    time.Time
	cards []protocol.CardInfo
	bid   bool
}

type decisionSession struct{ submissions chan botSubmission }

func (s *decisionSession) HandleBotPlay(_ context.Context, _ string, turn uint64, cards []protocol.CardInfo) error {
	s.submissions <- botSubmission{turn: turn, at: time.Now(), cards: cards}
	return nil
}
func (s *decisionSession) HandleBotBid(_ context.Context, _ string, turn uint64, bid bool) error {
	s.submissions <- botSubmission{turn: turn, at: time.Now(), bid: bid}
	return nil
}

func testBot(engine DecisionEngine) (*BotClient, *decisionSession) {
	b := NewBotClient(engine)
	s := &decisionSession{submissions: make(chan botSubmission, 10)}
	b.SetSession(s)
	b.state.hand = cards("5 4 3")
	return b, s
}

func playTurn(b *BotClient, mustPlay bool) *protocol.Message {
	return codec.MustNewMessage(protocol.MsgPlayTurn, protocol.PlayTurnPayload{PlayerID: b.id, MustPlay: mustPlay, CanBeat: mustPlay})
}

func TestPlayTargetIncludesInferenceAndPass(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inference time.Duration
		pass      bool
	}{
		{"play", 0, false}, {"pass", 0, true},
		{"some inference", 500 * time.Millisecond, false},
		{"at maximum target", 2 * time.Second, false},
		{"slow inference", 3 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				var inferenceStarted time.Time
				b, sess := testBot(decisionEngine{play: func(ctx context.Context, g GameContext) []card.Card {
					inferenceStarted = time.Now()
					if !waitForDecision(ctx, tc.inference) || tc.pass {
						return nil
					}
					return g.Hand[:1]
				}})
				defer b.Close()
				b.SendTurn(context.Background(), 42, playTurn(b, !tc.pass))
				synctest.Wait()
				require.Equal(t, start, inferenceStarted, "inference must begin before artificial waiting")
				time.Sleep(999 * time.Millisecond)
				synctest.Wait()
				require.Empty(t, sess.submissions)
				got := <-sess.submissions
				require.EqualValues(t, 42, got.turn)
				elapsed := got.at.Sub(start)
				if tc.inference >= 2*time.Second {
					require.Equal(t, tc.inference, elapsed, "slow inference must submit immediately")
				} else {
					require.GreaterOrEqual(t, elapsed, time.Second)
					require.LessOrEqual(t, elapsed, 2*time.Second)
				}
				if tc.pass {
					require.Nil(t, got.cards)
				} else {
					require.Len(t, got.cards, 1)
				}
			})
		})
	}
}

func TestCancelledTurnCannotSubmit(t *testing.T) {
	for _, phase := range []string{"waiting", "inference", "ignores cancellation"} {
		for _, action := range []string{"close", "end", "next turn", "same bot again"} {
			t.Run(phase+"/"+action, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					calls := 0
					var oldContext context.Context
					b, sess := testBot(decisionEngine{play: func(ctx context.Context, g GameContext) []card.Card {
						calls++
						if calls == 1 {
							oldContext = ctx
							switch phase {
							case "inference":
								<-ctx.Done()
							case "ignores cancellation":
								time.Sleep(4 * time.Second)
							}
						}
						return g.Hand[:1]
					}})
					defer b.Close()
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					b.SendTurn(ctx, 1, playTurn(b, true))
					synctest.Wait()
					time.Sleep(100 * time.Millisecond)
					switch action {
					case "close":
						b.Close()
					case "end":
						cancel()
					case "next turn":
						b.SendTurn(context.Background(), 2, codec.MustNewMessage(protocol.MsgPlayTurn, protocol.PlayTurnPayload{PlayerID: "other"}))
					case "same bot again":
						b.SendTurn(context.Background(), 4, playTurn(b, true))
					}
					synctest.Wait()
					require.ErrorIs(t, oldContext.Err(), context.Canceled)
					time.Sleep(5 * time.Second)
					synctest.Wait()
					if action == "same bot again" {
						require.Len(t, sess.submissions, 1)
						require.EqualValues(t, 4, (<-sess.submissions).turn)
					} else {
						require.Empty(t, sess.submissions)
					}
				})
			})
		}
	}
}

func TestBidKeepsExistingDelayAndCanCancel(t *testing.T) {
	for _, isGrab := range []bool{false, true} {
		t.Run(map[bool]string{false: "call", true: "grab"}[isGrab], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				decisions := make(chan time.Time, 1)
				b, sess := testBot(decisionEngine{bid: func(context.Context) bool { decisions <- time.Now(); return true }})
				defer b.Close()
				msg := codec.MustNewMessage(protocol.MsgBidTurn, protocol.BidTurnPayload{PlayerID: b.id, IsGrab: isGrab})
				b.SendTurn(context.Background(), 3, msg)
				synctest.Wait()
				require.Empty(t, decisions)
				got := <-sess.submissions
				decidedAt := <-decisions
				require.True(t, got.bid)
				require.GreaterOrEqual(t, decidedAt.Sub(start), 300*time.Millisecond)
				require.Less(t, decidedAt.Sub(start), 900*time.Millisecond)
				b.SendTurn(context.Background(), 4, msg)
				b.Close()
				time.Sleep(time.Second)
				synctest.Wait()
				require.Empty(t, sess.submissions)
			})
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDouZeroTimeoutAndFallbackRespectTarget(t *testing.T) {
	for _, scenario := range []string{"timeout", "invalid cards", "must play", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := NewDouZeroEngine("http://douzero.test")
				var requestContext context.Context
				e.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					requestContext = r.Context()
					if scenario == "timeout" || scenario == "cancel" {
						<-r.Context().Done()
						return nil, r.Context().Err()
					}
					body := `{"action":[17]}` // A card the bot does not hold.
					if scenario == "must play" {
						body = `{"action":[]}`
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				b, sess := testBot(e)
				defer b.Close()
				b.state.douzeroPos = DouZeroPosLandlord
				start := time.Now()
				b.SendTurn(context.Background(), 9, playTurn(b, true))
				synctest.Wait()
				if scenario == "cancel" {
					b.Close()
					synctest.Wait()
					require.ErrorIs(t, requestContext.Err(), context.Canceled)
					require.Empty(t, sess.submissions)
					return
				}
				got := <-sess.submissions
				require.Len(t, got.cards, 1, "fallback must produce a legal lead")
				require.EqualValues(t, card.Rank3, got.cards[0].Rank)
				if scenario == "timeout" {
					require.Equal(t, douzeroTimeout, got.at.Sub(start))
				} else {
					require.GreaterOrEqual(t, got.at.Sub(start), time.Second)
					require.LessOrEqual(t, got.at.Sub(start), 2*time.Second)
				}
			})
		})
	}
}
