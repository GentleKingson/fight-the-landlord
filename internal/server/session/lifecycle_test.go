package session

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
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

func TestRetireBeforeStartPreventsSessionActivation(t *testing.T) {
	t.Parallel()

	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient("p1", "Player1"),
		testutil.NewSimpleClient("p2", "Player2"),
		testutil.NewSimpleClient("p3", "Player3"),
	}
	r := room.NewMockRoom("RETIRED", clients[0])
	r.AddPlayerForTest(clients[1], 1, true)
	r.AddPlayerForTest(clients[2], 2, true)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})
	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})

	gs.Retire()
	gs.Retire()
	gs.Start()

	assert.Equal(t, GameStateInit, gs.state)
	assert.Equal(t, room.RoomStateWaiting, r.State())
	gs.timerMu.Lock()
	assert.Nil(t, gs.turnTimer)
	gs.timerMu.Unlock()
	for _, client := range clients {
		assert.Empty(t, client.SentMessages())
	}
}

func TestRetiredSessionRejectsLateActionsAndTimerCallbacks(t *testing.T) {
	t.Parallel()

	t.Run("bid timer", func(t *testing.T) {
		gs := newRetirementTestSession(t, "RETIRED-BID")
		gs.Start()
		playerID := gs.players[gs.currentBidder].ID
		turnID := gs.turnID
		version := gs.snapshotVersion
		gs.Retire()

		assert.ErrorIs(t, gs.handleBid(playerID, false, turnID), apperrors.ErrGameNotStart)
		assert.Equal(t, version, gs.snapshotVersion)
	})

	t.Run("play timer", func(t *testing.T) {
		gs := newRetirementTestSession(t, "RETIRED-PLAY")
		playingCard := card.Card{Suit: card.Spade, Rank: card.Rank3, Color: card.Black}
		gs.mu.Lock()
		gs.state = GameStatePlaying
		gs.currentPlayer = 0
		gs.lastPlayerIdx = 0
		gs.turnID = 17
		gs.players[0].Hand = []card.Card{playingCard}
		version := gs.snapshotVersion
		gs.mu.Unlock()
		gs.Retire()

		assert.ErrorIs(t, gs.handlePlayCards("p1", []protocol.CardInfo{{Suit: int(playingCard.Suit), Rank: int(playingCard.Rank), Color: int(playingCard.Color)}}, 17), apperrors.ErrGameNotStart)
		gs.mu.RLock()
		assert.Equal(t, version, gs.snapshotVersion)
		assert.Equal(t, []card.Card{playingCard}, gs.players[0].Hand)
		gs.mu.RUnlock()
	})

	t.Run("late pass", func(t *testing.T) {
		gs := newRetirementTestSession(t, "RETIRED-PASS")
		gs.mu.Lock()
		gs.state = GameStatePlaying
		gs.currentPlayer = 0
		gs.lastPlayerIdx = 1
		gs.turnID = 23
		gs.lastPlayedHand = rule.ParsedHand{Type: rule.Single, KeyRank: card.Rank4, Length: 1}
		version := gs.snapshotVersion
		gs.mu.Unlock()
		gs.Retire()

		assert.ErrorIs(t, gs.handlePass("p1", 23), apperrors.ErrGameNotStart)
		gs.mu.RLock()
		assert.Equal(t, version, gs.snapshotVersion)
		assert.Equal(t, 0, gs.currentPlayer)
		gs.mu.RUnlock()
	})
}

func TestRetirePreventsStalePresenceCallbacksFromRearmingTimers(t *testing.T) {
	t.Parallel()

	t.Run("offline after retirement", func(t *testing.T) {
		gs := newRetirementTestSession(t, "RETIRED-OFFLINE")
		gs.Start()
		player := gs.players[gs.currentBidder]
		gs.Retire()

		gs.PlayerOffline(player.ID)

		assert.False(t, player.IsOffline)
		gs.timerMu.Lock()
		assert.Nil(t, gs.turnTimer)
		assert.Nil(t, gs.offlineWaitTimer)
		gs.timerMu.Unlock()
	})

	t.Run("online after retirement", func(t *testing.T) {
		gs := newRetirementTestSession(t, "RETIRED-ONLINE")
		gs.Start()
		player := gs.players[gs.currentBidder]
		gs.PlayerOffline(player.ID)
		gs.Retire()

		gs.PlayerOnline(player.ID)

		assert.True(t, player.IsOffline)
		gs.timerMu.Lock()
		assert.Nil(t, gs.turnTimer)
		assert.Nil(t, gs.offlineWaitTimer)
		gs.timerMu.Unlock()
	})
}

func TestRetireConcurrentWithPlayerOfflineLeavesNoTimer(t *testing.T) {
	for iteration := range 64 {
		gs := newRetirementTestSession(t, "RETIRED-RACE")
		gs.Start()
		playerID := gs.players[gs.currentBidder].ID
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			gs.Retire()
		}()
		go func() {
			defer workers.Done()
			<-start
			gs.PlayerOffline(playerID)
		}()
		close(start)
		workers.Wait()

		gs.PlayerOffline(playerID)
		gs.timerMu.Lock()
		assert.Nil(t, gs.turnTimer, "iteration %d", iteration)
		assert.Nil(t, gs.offlineWaitTimer, "iteration %d", iteration)
		gs.timerMu.Unlock()
	}
}

func newRetirementTestSession(t *testing.T, code string) *GameSession {
	t.Helper()
	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient("p1", "Player1"),
		testutil.NewSimpleClient("p2", "Player2"),
		testutil.NewSimpleClient("p3", "Player3"),
	}
	r := room.NewMockRoom(code, clients[0])
	r.AddPlayerForTest(clients[1], 1, true)
	r.AddPlayerForTest(clients[2], 2, true)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})
	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15, OfflineWaitTimeout: 30})
	t.Cleanup(gs.StopAllTimers)
	return gs
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

func TestStartGamePausesWhenInitialBidderDisconnectedBeforeRegistration(t *testing.T) {
	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient("p1", "Player1"),
		testutil.NewSimpleClient("p2", "Player2"),
		testutil.NewSimpleClient("p3", "Player3"),
	}
	r := room.NewMockRoom("INITIAL-OFFLINE", clients[0])
	r.AddPlayerForTest(clients[1], 1, true)
	r.AddPlayerForTest(clients[2], 2, true)
	r.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})
	for _, client := range clients {
		require.True(t, r.DetachClient(client.GetID(), client))
	}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{
		TurnTimeout:        3600,
		BidTimeout:         3600,
		OfflineWaitTimeout: 3600,
	})
	t.Cleanup(gs.StopAllTimers)
	gs.Start()

	gs.timerMu.Lock()
	defer gs.timerMu.Unlock()
	assert.Nil(t, gs.turnTimer)
	assert.NotNil(t, gs.offlineWaitTimer)
	assert.True(t, gs.turnDeadline.IsZero())
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
