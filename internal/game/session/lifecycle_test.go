package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
)

// assertRoundCards 验证底牌归属以及整副牌的完整性，不依赖随机结果或牌力。
func assertRoundCards(t *testing.T, gs *GameSession, landlordAssigned bool) {
	t.Helper()
	require.Len(t, gs.players, 3)
	require.Len(t, gs.bottomCards, 3)
	allCards := make([]card.Card, 0, 54)
	landlordCount := 0
	for i, p := range gs.players {
		assert.Equal(t, i, p.Seat)
		assert.Equal(t, gs.room.PlayerOrder[i], p.ID)
		wantCards := 17
		if p.IsLandlord {
			landlordCount++
			wantCards = 20
			assert.Equal(t, i, gs.currentPlayer, "地主先出牌")
			for _, c := range gs.bottomCards {
				assert.Contains(t, p.Hand, c, "底牌必须归地主")
			}
		}
		require.Len(t, p.Hand, wantCards)
		allCards = append(allCards, p.Hand...)
	}
	if landlordAssigned {
		assert.Equal(t, 1, landlordCount)
	} else {
		assert.Zero(t, landlordCount)
		assert.GreaterOrEqual(t, gs.currentBidder, 0)
		assert.Less(t, gs.currentBidder, 3)
		allCards = append(allCards, gs.bottomCards...)
	}
	require.Len(t, allCards, 54)
	assert.ElementsMatch(t, []card.Card(card.NewDeck()), allCards, "54 张牌不重不漏")
}

func TestStartGame_DealCards(t *testing.T) {
	t.Parallel()

	gs := newTestGame(t)

	// Start game
	gs.Start()
	gs.finishDeal(gs.dealID)

	// Verify state
	assert.Equal(t, GameStateBidding, gs.state)
	assert.Equal(t, room.RoomStateBidding, gs.room.State)

	assertRoundCards(t, gs, false)
}

func TestStartGame_CardsAreSorted(t *testing.T) {
	t.Parallel()

	gs := newTestGame(t)
	gs.Start()
	gs.finishDeal(gs.dealID)

	// Verify hands are sorted (descending by rank)
	for _, p := range gs.players {
		for i := 0; i < len(p.Hand)-1; i++ {
			assert.GreaterOrEqual(t, p.Hand[i].Rank, p.Hand[i+1].Rank,
				"Cards should be sorted in descending order")
		}
	}
}

func TestEndGame_WinnerAnnounced(t *testing.T) {
	t.Parallel()

	gs := newTestGame(t)
	gs.Start()
	gs.finishDeal(gs.dealID)

	// Set winner
	winner := gs.players[0]
	winner.IsLandlord = true

	// End game
	gs.endGame(winner)

	// Verify state
	assert.Equal(t, GameStateEnded, gs.state)
	assert.Equal(t, room.RoomStateEnded, gs.room.State)
}
