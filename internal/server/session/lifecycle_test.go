package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestStartGame_DealCards(t *testing.T) {
	t.Parallel()

	// Setup room with 3 players
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.AddPlayerForTest(testutil.NewSimpleClient("p2", "Player2"), 1, false)
	r.AddPlayerForTest(testutil.NewSimpleClient("p3", "Player3"), 2, false)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})

	// Create game session
	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})

	// Start game
	gs.Start()

	// Verify state
	assert.Equal(t, GameStateBidding, gs.state)
	assert.Equal(t, room.RoomStateBidding, r.State())

	// Verify each player has 17 cards
	for i, p := range gs.players {
		assert.Len(t, p.Hand, 17, "Player %d should have 17 cards", i)
	}

	// Verify bottom cards (3 cards)
	assert.Len(t, gs.bottomCards, 3)

	// Verify total cards = 54
	totalCards := len(gs.bottomCards)
	for _, p := range gs.players {
		totalCards += len(p.Hand)
	}
	assert.Equal(t, 54, totalCards)
}

func TestStartGame_CardsAreSorted(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.AddPlayerForTest(testutil.NewSimpleClient("p2", "Player2"), 1, false)
	r.AddPlayerForTest(testutil.NewSimpleClient("p3", "Player3"), 2, false)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})

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
	r.AddPlayerForTest(testutil.NewSimpleClient("p2", "Player2"), 1, false)
	r.AddPlayerForTest(testutil.NewSimpleClient("p3", "Player3"), 2, false)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})

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
	r.AddPlayerForTest(testutil.NewSimpleClient("p2", "Player2"), 1, false)
	r.AddPlayerForTest(testutil.NewSimpleClient("p3", "Player3"), 2, false)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})
	for _, player := range r.SnapshotPlayers() {
		player.Client.SetRoom(r.Code)
		require.True(t, r.SetPlayerReadyForTest(player.ID, true))
	}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// Set winner
	winner := gs.players[0]
	winner.IsLandlord = true

	// End game
	gs.mu.Lock()
	gs.endGame(winner)
	work := gs.takePendingWorkLocked()
	gs.mu.Unlock()
	gs.dispatchPendingWork(work)

	// Verify state
	assert.Equal(t, GameStateEnded, gs.state)
	assert.Equal(t, room.RoomStateWaiting, r.State())
	for _, player := range r.SnapshotPlayers() {
		assert.False(t, player.Ready)
		assert.False(t, player.IsLandlord)
		if player.Client != nil {
			assert.Equal(t, r.Code, player.Client.GetRoom())
		}
	}
}

func TestEndedRoomCanReadyUpIntoFreshSession(t *testing.T) {
	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient("p1", "Player1"),
		testutil.NewSimpleClient("p2", "Player2"),
		testutil.NewSimpleClient("p3", "Player3"),
	}
	r := room.NewMockRoom("REPLAY", clients[0])
	r.AddPlayerForTest(clients[1], 1, true)
	r.AddPlayerForTest(clients[2], 2, true)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})
	for _, client := range clients {
		client.SetRoom(r.Code)
	}

	gameConfig := config.GameConfig{TurnTimeout: 30, BidTimeout: 15}
	oldSession := NewGameSession(r, storage.NewLeaderboardManager(nil), gameConfig)
	oldSession.Start()
	oldSession.mu.Lock()
	oldSession.endGame(oldSession.players[0])
	work := oldSession.takePendingWorkLocked()
	oldSession.mu.Unlock()
	oldSession.dispatchPendingWork(work)
	t.Cleanup(oldSession.StopAllTimers)

	manager := room.NewRoomManager(nil, gameConfig)
	manager.AddRoomForTest(r)
	var replacement *GameSession
	manager.SetOnGameStart(func(gameRoom *room.Room, players []room.PlayerSnapshot) {
		replacement = NewGameSessionWithPlayers(gameRoom, players, storage.NewLeaderboardManager(nil), gameConfig)
		replacement.Start()
	})

	for _, client := range clients {
		require.NoError(t, manager.SetPlayerReady(client, true))
	}
	require.NotNil(t, replacement)
	t.Cleanup(replacement.StopAllTimers)
	assert.NotSame(t, oldSession, replacement)
	assert.Equal(t, GameStateBidding, replacement.state)
	assert.Equal(t, room.RoomStateBidding, r.State())
	for _, client := range clients {
		assert.Equal(t, r.Code, client.GetRoom())
	}
}

func TestNewGameSession_Initialization(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.AddPlayerForTest(testutil.NewSimpleClient("p2", "Player2"), 1, false)
	r.AddPlayerForTest(testutil.NewSimpleClient("p3", "Player3"), 2, false)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})

	// Create session
	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})

	// Verify initialization
	assert.Equal(t, GameStateInit, gs.state)
	assert.Len(t, gs.players, 3)
	assert.Equal(t, -1, gs.landlordCaller)
	assert.Equal(t, -1, gs.landlordCandidate)
	assert.Equal(t, 1, gs.bidMultiplier)

	// Verify players are in correct seats
	roomPlayers := r.SnapshotPlayers()
	require.Len(t, roomPlayers, len(gs.players))
	for i, p := range gs.players {
		assert.Equal(t, i, p.Seat)
		assert.Equal(t, roomPlayers[i].ID, p.ID)
	}
}

func TestGameSession_PlayerOfflineHandling(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.AddPlayerForTest(testutil.NewSimpleClient("p2", "Player2"), 1, false)
	r.AddPlayerForTest(testutil.NewSimpleClient("p3", "Player3"), 2, false)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})

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
