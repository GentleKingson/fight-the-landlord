package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
)

// TestPlayConservesCards exercises full deal/bid/play transitions repeatedly.
// Cards may move from a hand to the public ledger, but the authoritative total
// must never change.
func TestPlayConservesCards(t *testing.T) {
	for iteration := 0; iteration < 32; iteration++ {
		game, sessions := newSnapshotTestSession(t)
		game.Start()

		caller := game.players[game.currentBidder]
		require.NoError(t, game.HandleBid(caller.ID, true))
		for game.state == GameStateBidding {
			bidder := game.players[game.currentBidder]
			require.NoError(t, game.HandleBid(bidder.ID, false))
		}

		before := authoritativeCardCount(game)
		current := game.players[game.currentPlayer]
		play := rule.FindSmallestBeatingCards(current.Hand, rule.ParsedHand{})
		require.NotEmpty(t, play)
		require.NoError(t, game.HandlePlayCards(current.ID, convert.CardsToInfos(play)))
		require.Equal(t, before, authoritativeCardCount(game))

		game.StopAllTimers()
		require.NoError(t, sessions.Close())
	}
}

func authoritativeCardCount(game *GameSession) int {
	game.mu.RLock()
	defer game.mu.RUnlock()
	total := 0
	for _, player := range game.players {
		total += len(player.Hand)
	}
	for _, played := range game.playedCards {
		total += len(played)
	}
	return total
}

// TestCredentialRestorePreservesSerializableSnapshotState checks only the
// authoritative DTO prepared for wire serialization. Handler selection and
// reconnect publication ordering are covered by the server/handler tests.
func TestCredentialRestorePreservesSerializableSnapshotState(t *testing.T) {
	game, sessions := newSnapshotTestSession(t)
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	game.Start()

	settlement := &protocol.GameSettlementDTO{
		WinnerID:         "p1",
		WinnerName:       "Player1",
		WinnerIsLandlord: true,
		Multiplier:       4,
		Scores: []protocol.PlayerScore{
			{PlayerID: "p1", PlayerName: "Player1", IsLandlord: true, Score: 8},
			{PlayerID: "p2", PlayerName: "Player2", Score: -4},
			{PlayerID: "p3", PlayerName: "Player3", Score: -4},
		},
	}
	game.EndWithSettlementForTest(settlement)
	before := game.BuildGameStateDTO("p1", sessions)
	require.NotNil(t, before)
	require.NotNil(t, before.Settlement)

	original := sessions.GetSession("p1")
	require.NotNil(t, original)
	original.mu.RLock()
	token := original.ReconnectToken
	original.mu.RUnlock()
	sessions.SetOffline("p1")
	sessions.MustCreateSession("temporary", "Temporary")
	restored, err := sessions.RestoreSession(token, "p1", "temporary")
	require.NoError(t, err)
	require.NotNil(t, restored)

	after := game.BuildGameStateDTO(restored.PlayerID, sessions)
	require.NotNil(t, after)
	assert.Equal(t, before.GameID, after.GameID)
	assert.GreaterOrEqual(t, after.TurnID, before.TurnID)
	assert.GreaterOrEqual(t, after.SnapshotVersion, before.SnapshotVersion)
	assert.Equal(t, before.Settlement, after.Settlement)
}
