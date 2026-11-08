package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/stretchr/testify/require"
)

func recordingGame(t *testing.T) *GameSession {
	t.Helper()
	r := room.NewMockRoom("123456", testutil.NewSimpleClient("private-p1", "Private Name"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "P2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "P3"), Seat: 2}
	r.PlayerOrder = []string{"private-p1", "p2", "p3"}
	gs := NewGameSession(r, nil, config.GameConfig{TurnTimeout: 600, BidTimeout: 600, CollectTrainingData: true, TrainingDataDir: t.TempDir()})
	gs.Start()
	gs.finishDeal(gs.dealID)
	t.Cleanup(gs.StopAllTimers)
	return gs
}

func readRecords(t *testing.T, gs *GameSession) []map[string]any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(gs.gameConfig.TrainingDataDir, "*", "*.jsonl"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	raw, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-p1")
	require.NotContains(t, string(raw), "Private Name")
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	rows := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row))
		require.EqualValues(t, i, row["seq"])
		rows = append(rows, row)
	}
	return rows
}

func TestRecordingCompletedGameAndDecisionSources(t *testing.T) {
	gs := recordingGame(t)
	require.NoError(t, gs.HandleBid(gs.players[gs.currentBidder].ID, true))
	for range 2 {
		require.NoError(t, gs.HandleBid(gs.players[gs.currentBidder].ID, false))
	}
	// Invalid requests never enter the dataset.
	before := gs.recording.sequence
	require.Error(t, gs.HandlePass(gs.players[gs.currentPlayer].ID))
	require.Equal(t, before, gs.recording.sequence)
	// A timeout must be distinguishable from a human click and stale timers ignored.
	gs.handleTurnTimeout(gs.turnID-1, "timeout")
	require.Equal(t, before, gs.recording.sequence)
	gs.handlePlayTimeout()
	for turn := 0; gs.state != GameStateEnded && turn < 200; turn++ {
		p := gs.players[gs.currentPlayer]
		cards := rule.FindSmallestBeatingCards(p.Hand, gs.lastPlayedHand)
		if cards == nil {
			require.NoError(t, gs.HandlePass(p.ID))
		} else {
			require.NoError(t, gs.HandlePlayCards(p.ID, convert.CardsToInfos(cards)))
		}
	}
	require.Equal(t, GameStateEnded, gs.state)
	rows := readRecords(t, gs)
	require.Equal(t, "deal", rows[0]["type"])
	require.Equal(t, "completed", rows[len(rows)-1]["status"])
	human, timeout := 0, 0
	for _, row := range rows {
		if row["source"] == "human" {
			human++
		}
		if row["source"] == "timeout" {
			timeout++
		}
	}
	require.Positive(t, human)
	require.Equal(t, 1, timeout)
	require.Len(t, rows[len(rows)-1]["rewards"], 3)
}

func TestRecordingRedealAbortAndDisabled(t *testing.T) {
	gs := recordingGame(t)
	for range 3 {
		require.NoError(t, gs.HandleBid(gs.players[gs.currentBidder].ID, false))
	}
	rows := readRecords(t, gs)
	require.Equal(t, "redeal", rows[len(rows)-1]["status"])
	gs.StopAllTimers()
	paths, err := filepath.Glob(filepath.Join(gs.gameConfig.TrainingDataDir, "*", "*.jsonl"))
	require.NoError(t, err)
	require.Len(t, paths, 2)
	gs.gameConfig.CollectTrainingData = false
	gs.Start()
	gs.finishDeal(gs.dealID)
	require.Nil(t, gs.recording)
}

func TestTrainingCardEncoding(t *testing.T) {
	require.Equal(t, []int{17, 20, 30}, trainingCards([]card.Card{{Rank: card.Rank2}, {Rank: card.RankBlackJoker}, {Rank: card.RankRedJoker}}))
}
