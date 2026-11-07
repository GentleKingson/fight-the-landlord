package session

import (
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestForgedColorCannotChangeHandOrTrainingRecord(t *testing.T) {
	gs := recordingGame(t)
	gs.mu.Lock()
	gs.stopTimer()
	gs.setLandlord(0)
	gs.mu.Unlock()
	p := gs.players[0]
	original := append([]card.Card(nil), p.Hand...)
	before, turn := gs.recording.sequence, gs.turnID
	info := convert.CardToInfo(p.Hand[0])
	info.Color = 1 - info.Color
	require.ErrorIs(t, gs.HandlePlayCards(p.ID, []protocol.CardInfo{info}), apperrors.ErrInvalidCards)
	require.Equal(t, original, p.Hand)
	require.Equal(t, before, gs.recording.sequence)
	require.Equal(t, turn, gs.turnID)
	require.NoError(t, gs.HandlePlayCards(p.ID, convert.CardsToInfos(original[:1])))
	require.Equal(t, original[1:], p.Hand)
	require.Equal(t, before+1, gs.recording.sequence)
	for turn := 0; gs.state != GameStateEnded && turn < 300; turn++ {
		gs.handlePlayTimeout()
	}
	require.Equal(t, GameStateEnded, gs.state)
	rows := readRecords(t, gs)
	require.Equal(t, "completed", rows[len(rows)-1]["status"])
}

func TestRejectedLeaveKeepsRedealAndRecordingUsable(t *testing.T) {
	cfg := config.GameConfig{BidTimeout: 600, TurnTimeout: 600, CollectTrainingData: true, TrainingDataDir: t.TempDir()}
	rm := room.NewRoomManager(nil, cfg)
	clients := []*testutil.SimpleClient{testutil.NewSimpleClient("a", "A"), testutil.NewSimpleClient("b", "B"), testutil.NewSimpleClient("c", "C")}
	r, err := rm.CreateRoom(clients[0])
	require.NoError(t, err)
	for _, c := range clients[1:] {
		_, err = rm.JoinRoom(c, r.Code)
		require.NoError(t, err)
	}
	gs := NewGameSession(r, nil, cfg)
	gs.Start()
	t.Cleanup(gs.StopAllTimers)
	require.ErrorIs(t, rm.LeaveRoom(clients[gs.currentBidder]), apperrors.ErrGameStarted)
	for range 3 {
		gs.handleTurnTimeout(gs.turnID, "timeout")
	}
	require.Equal(t, 1, gs.redealCount)
	for _, p := range gs.players {
		require.Len(t, p.Hand, 17)
	}
	rows := readRecords(t, gs)
	require.Equal(t, "redeal", rows[len(rows)-1]["status"])
}

// PlayTurn and the reconnect turn must recognize every legal response shape,
// including ones the deliberately simple timeout policy does not generate.
func TestTurnAllowsComplexRepliesBeforeAndAfterReconnect(t *testing.T) {
	cases := []struct{ name, lead, reply string }{
		{"straight", "34567", "45678"}, {"pair straight", "334455", "445566"},
		{"plane", "333444", "444555"}, {"plane singles", "33344489", "4445559T"},
		{"plane pairs", "3334448899", "44455599TT"},
		{"four with two", "333389", "44449T"}, {"four with pairs", "33338899", "444499TT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := recordingGame(t)
			gs.mu.Lock()
			gs.stopTimer()
			gs.gameConfig.CollectTrainingData = false
			gs.setLandlord(0)
			gs.lastPlayedHand, _ = rule.ParseHand(cardsForRanks(t, tc.lead))
			gs.lastPlayerIdx = 1
			gs.players[0].Hand = cardsForRanks(t, tc.reply)
			gs.notifyPlayTurn()
			gs.mu.Unlock()
			client := gs.room.Players[gs.players[0].ID].Client.(*testutil.SimpleClient)
			assertCanReply(t, client)
			gs.ResendTurnTo(client)
			assertCanReply(t, client)
			require.NoError(t, gs.HandlePlayCards(client.ID, convert.CardsToInfos(gs.players[0].Hand)))
		})
	}
}

func assertCanReply(t *testing.T, client *testutil.SimpleClient) {
	t.Helper()
	messages := client.SentMessages()
	msg := messages[len(messages)-1]
	require.Equal(t, protocol.MsgPlayTurn, msg.Type)
	payload, err := codec.ParsePayload[protocol.PlayTurnPayload](msg)
	require.NoError(t, err)
	require.True(t, payload.CanBeat)
	require.False(t, payload.MustPlay)
}

func cardsForRanks(t *testing.T, ranks string) []card.Card {
	t.Helper()
	counts := map[card.Rank]int{}
	cards := make([]card.Card, 0, len(ranks))
	for _, char := range ranks {
		rank, err := card.RankFromChar(char)
		require.NoError(t, err)
		suit := card.Suit(counts[rank])
		color := card.Black
		if suit == card.Heart || suit == card.Diamond {
			color = card.Red
		}
		cards = append(cards, card.Card{Rank: rank, Suit: suit, Color: color})
		counts[rank]++
	}
	return cards
}
