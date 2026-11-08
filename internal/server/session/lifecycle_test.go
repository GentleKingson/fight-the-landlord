package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

// assertRoundCards 验证底牌归属以及整副牌的完整性，不依赖随机结果或牌力。
func assertRoundCards(t *testing.T, gs *GameSession, landlordAssigned bool) {
	t.Helper()
	require.Len(t, gs.players, 3)
	require.Len(t, gs.bottomCards, 3)
	allCards := make([]card.Card, 0, 54)
	landlordCount := 0
	for i, p := range gs.players {
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

	// Setup room with 3 players
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	// Create game session
	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})

	// Start game
	t.Cleanup(gs.StopAllTimers)
	gs.Start()

	// Verify state
	assert.Equal(t, GameStateBidding, gs.state)
	assert.Equal(t, room.RoomStateBidding, r.State)

	assertRoundCards(t, gs, false)
}

func TestStartGame_CardsAreSorted(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// Verify hands are sorted (descending by rank)
	for _, p := range gs.players {
		for i := 0; i < len(p.Hand)-1; i++ {
			assert.GreaterOrEqual(t, p.Hand[i].Rank, p.Hand[i+1].Rank,
				"Cards should be sorted in descending order")
		}
	}
}

func TestStartGame_BidderSelected(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// Verify a bidder was selected (0, 1, or 2)
	assert.GreaterOrEqual(t, gs.currentBidder, 0)
	assert.Less(t, gs.currentBidder, 3)
}

func TestEndGame_WinnerAnnounced(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// Set winner
	winner := gs.players[0]
	winner.IsLandlord = true

	// End game
	gs.endGame(winner)

	// Verify state
	assert.Equal(t, GameStateEnded, gs.state)
	assert.Equal(t, room.RoomStateEnded, r.State)
}

func TestNewGameSession_Initialization(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	// Create session
	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})

	// Verify initialization
	assert.Equal(t, GameStateInit, gs.state)
	assert.Len(t, gs.players, 3)
	assert.Equal(t, -1, gs.landlordCaller)
	assert.Equal(t, -1, gs.landlordCandidate)
	assert.Equal(t, 1, gs.bidMultiplier)

	// Verify players are in correct seats
	for i, p := range gs.players {
		assert.Equal(t, i, p.Seat)
		assert.Equal(t, r.PlayerOrder[i], p.ID)
	}
}

func TestGameSession_PlayerOfflineHandling(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})

	// Mark player as offline
	gs.mu.Lock()
	gs.players[0].IsOffline = true
	gs.mu.Unlock()

	// Verify offline status
	gs.mu.RLock()
	assert.True(t, gs.players[0].IsOffline)
	gs.mu.RUnlock()
}
