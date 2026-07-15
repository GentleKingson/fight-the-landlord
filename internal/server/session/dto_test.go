package session

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func newSnapshotTestSession(t *testing.T) (*GameSession, *SessionManager) {
	t.Helper()

	clients := []*testutil.SimpleClient{
		testutil.NewSimpleClient("p1", "Player1"),
		testutil.NewSimpleClient("p2", "Player2"),
		testutil.NewSimpleClient("p3", "Player3"),
	}
	gameRoom := room.NewMockRoom("SNAPSHOT", clients[0])
	gameRoom.AddPlayerForTest(clients[1], 1, true)
	gameRoom.AddPlayerForTest(clients[2], 2, true)
	require.True(t, gameRoom.SetPlayerReadyForTest("p1", true))
	gameRoom.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})

	sessionManager := NewSessionManager()
	for _, client := range clients {
		sessionManager.CreateSession(client.ID, client.Name)
		client.SetRoom(gameRoom.Code)
		sessionManager.SetRoom(client.ID, gameRoom.Code)
	}

	gameSession := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), config.GameConfig{
		BidTimeout:         30,
		TurnTimeout:        40,
		OfflineWaitTimeout: 30,
	})
	t.Cleanup(gameSession.StopAllTimers)
	return gameSession, sessionManager
}

func gameIDNumber(t *testing.T, gameID string) int64 {
	t.Helper()
	require.True(t, strings.HasPrefix(gameID, "game-"))
	value, err := strconv.ParseInt(strings.TrimPrefix(gameID, "game-"), 10, 64)
	require.NoError(t, err)
	return value
}

func ledgerForPlayer(t *testing.T, ledger []protocol.PlayerPlayedCards, playerID string) []protocol.CardInfo {
	t.Helper()
	for _, entry := range ledger {
		if entry.PlayerID == playerID {
			return entry.Cards
		}
	}
	t.Fatalf("missing played-card ledger for %s", playerID)
	return nil
}

func TestBuildGameStateDTOBiddingIsCompleteAndHidesBottomCards(t *testing.T) {
	t.Parallel()

	gs, sessionManager := newSnapshotTestSession(t)
	sessionManager.SetOffline("p2")
	gs.Start()

	first := gs.BuildGameStateDTO("p1", sessionManager)
	require.NotNil(t, first)
	assert.Equal(t, "bidding", first.Phase)
	assert.NotEmpty(t, first.GameID)
	assert.Positive(t, first.SnapshotVersion)
	assert.Positive(t, first.TurnID)
	assert.Equal(t, gs.players[gs.currentBidder].ID, first.CurrentTurn)
	assert.Len(t, first.Hand, 17)
	assert.Empty(t, first.BottomCards, "bidding snapshots must not reveal the bottom cards")
	assert.False(t, first.BottomCardsRevealed)
	assert.Empty(t, first.LastPlayed)
	assert.Empty(t, first.LastPlayerID)
	assert.Empty(t, first.LastPlayerName)
	assert.Empty(t, first.LastHandType)
	assert.False(t, first.MustPlay)
	assert.False(t, first.CanBeat)
	assert.False(t, first.IsGrab)
	assert.Equal(t, 1, first.Multiplier)
	assert.Equal(t, baseScore, first.BaseScore)
	assert.LessOrEqual(t, first.ServerTimeMS, first.TurnDeadlineMS)
	assert.Greater(t, first.TurnDeadlineMS-first.ServerTimeMS, int64(28*time.Second/time.Millisecond))
	assert.LessOrEqual(t, first.TurnDeadlineMS-first.ServerTimeMS, int64(30*time.Second/time.Millisecond))
	require.Len(t, first.Players, 3)
	assert.True(t, first.Players[0].Ready)
	assert.True(t, first.Players[0].Online)
	assert.False(t, first.Players[1].Online)
	require.Len(t, first.PlayedCards, 3)
	for _, entry := range first.PlayedCards {
		assert.Empty(t, entry.Cards)
	}

	second := gs.BuildGameStateDTO("p1", sessionManager)
	assert.Equal(t, first.SnapshotVersion, second.SnapshotVersion, "reading an unchanged snapshot must not advance its event watermark")
	assert.Equal(t, first.GameID, second.GameID)
	assert.Equal(t, first.TurnID, second.TurnID)
	assert.Equal(t, first.TurnDeadlineMS, second.TurnDeadlineMS)
	assert.Equal(t, first.PlayedCards, second.PlayedCards)
	event := EventMetaFromGameStateDTO(second)
	require.NotNil(t, event)
	assert.Equal(t, "game:"+second.GameID, event.StreamID)
	assert.Equal(t, second.SnapshotVersion, event.EventVersion)
	assert.Equal(t, second.TurnID, event.TurnID)
	assert.Equal(t, second.ServerTimeMS, event.ServerTimeMS)
	assert.Equal(t, second.TurnDeadlineMS, event.TurnDeadlineMS)
}

func TestGameAndTurnIdentifiersAdvanceMonotonically(t *testing.T) {
	t.Parallel()

	gs, sessionManager := newSnapshotTestSession(t)
	gs.Start()
	first := gs.BuildGameStateDTO("p1", sessionManager)

	currentBidder := gs.players[gs.currentBidder]
	require.NoError(t, gs.HandleBid(currentBidder.ID, false))
	second := gs.BuildGameStateDTO("p1", sessionManager)
	assert.Equal(t, first.GameID, second.GameID)
	assert.Greater(t, second.TurnID, first.TurnID)
	assert.Greater(t, second.SnapshotVersion, first.SnapshotVersion)

	for range 2 {
		currentBidder = gs.players[gs.currentBidder]
		require.NoError(t, gs.HandleBid(currentBidder.ID, false))
	}
	third := gs.BuildGameStateDTO("p1", sessionManager)
	assert.Greater(t, gameIDNumber(t, third.GameID), gameIDNumber(t, first.GameID))
	assert.Greater(t, third.TurnID, second.TurnID)
	assert.Greater(t, third.SnapshotVersion, second.SnapshotVersion)
	assert.Equal(t, "bidding", third.Phase)
	assert.Empty(t, third.BottomCards)
}

func TestBuildGameStateDTOPlayingHasAuthoritativeTurnAndLedger(t *testing.T) {
	t.Parallel()

	gs, sessionManager := newSnapshotTestSession(t)
	gs.Start()

	caller := gs.players[gs.currentBidder]
	require.NoError(t, gs.HandleBid(caller.ID, true))
	grabSnapshot := gs.BuildGameStateDTO(caller.ID, sessionManager)
	assert.True(t, grabSnapshot.IsGrab)
	assert.Equal(t, 1, grabSnapshot.Multiplier)

	grabber := gs.players[gs.currentBidder]
	require.NoError(t, gs.HandleBid(grabber.ID, true))
	for range 2 {
		bidder := gs.players[gs.currentBidder]
		require.NoError(t, gs.HandleBid(bidder.ID, false))
	}
	require.Equal(t, GameStatePlaying, gs.state)

	player := gs.players[gs.currentPlayer]
	playedCard := player.Hand[len(player.Hand)-1]
	playedTurnID := gs.turnID
	require.NoError(t, gs.HandlePlayCards(player.ID, []protocol.CardInfo{convert.CardToInfo(playedCard)}))

	snapshot := gs.BuildGameStateDTO("p1", sessionManager)
	assert.Equal(t, "playing", snapshot.Phase)
	assert.True(t, snapshot.BottomCardsRevealed)
	assert.Equal(t, convert.CardsToInfos(gs.bottomCards), snapshot.BottomCards)
	assert.Equal(t, player.ID, snapshot.LastPlayerID)
	assert.Equal(t, player.Name, snapshot.LastPlayerName)
	assert.Equal(t, rule.Single.String(), snapshot.LastHandType)
	assert.Equal(t, []protocol.CardInfo{convert.CardToInfo(playedCard)}, snapshot.LastPlayed)
	assert.False(t, snapshot.MustPlay)
	assert.False(t, snapshot.IsGrab)
	assert.Equal(t, 2, snapshot.Multiplier)
	assert.Equal(t, baseScore, snapshot.BaseScore)
	require.Len(t, snapshot.PlayedCards, 3)
	assert.Equal(t, snapshot.LastPlayed, ledgerForPlayer(t, snapshot.PlayedCards, player.ID))
	for _, other := range gs.players {
		if other.ID != player.ID {
			assert.Empty(t, ledgerForPlayer(t, snapshot.PlayedCards, other.ID))
		}
	}

	current := gs.players[gs.currentPlayer]
	expectedCanBeat := rule.FindSmallestBeatingCards(current.Hand, gs.lastPlayedHand) != nil
	assert.Equal(t, expectedCanBeat, snapshot.CanBeat)
	assert.Equal(t, current.ID, snapshot.CurrentTurn)
	assert.Greater(t, snapshot.TurnDeadlineMS, snapshot.ServerTimeMS)

	gs.handlePlayTimeout(playedTurnID)
	afterStaleTimeout := gs.BuildGameStateDTO("p1", sessionManager)
	assert.Equal(t, snapshot.TurnID, afterStaleTimeout.TurnID)
	assert.Equal(t, snapshot.CurrentTurn, afterStaleTimeout.CurrentTurn)
	assert.Equal(t, snapshot.PlayedCards, afterStaleTimeout.PlayedCards)

	passer := gs.players[gs.currentPlayer]
	require.NoError(t, gs.HandlePass(passer.ID))
	afterPass := gs.BuildGameStateDTO("p1", sessionManager)
	assert.Equal(t, snapshot.LastPlayerID, afterPass.LastPlayerID, "a pass must not replace the last effective play")
	assert.Equal(t, snapshot.LastPlayed, afterPass.LastPlayed)
	assert.Equal(t, snapshot.PlayedCards, afterPass.PlayedCards)

	err := gs.HandlePlayCards(player.ID, []protocol.CardInfo{convert.CardToInfo(playedCard)})
	assert.ErrorIs(t, err, apperrors.ErrNotYourTurn)
	repeated := gs.BuildGameStateDTO("p1", sessionManager)
	assert.Equal(t, snapshot.PlayedCards, repeated.PlayedCards, "repeated snapshots and rejected commands must not duplicate played cards")
}

func TestBuildGameStateDTOEndedClearsTurnAndKeepsFinalPlay(t *testing.T) {
	t.Parallel()

	gs, sessionManager := newSnapshotTestSession(t)
	gs.Start()
	caller := gs.players[gs.currentBidder]
	require.NoError(t, gs.HandleBid(caller.ID, true))
	for range 2 {
		bidder := gs.players[gs.currentBidder]
		require.NoError(t, gs.HandleBid(bidder.ID, false))
	}
	require.Equal(t, GameStatePlaying, gs.state)

	winner := gs.players[gs.currentPlayer]
	winningCard := card.Card{Suit: card.Spade, Rank: card.Rank3, Color: card.Black}
	gs.mu.Lock()
	winner.Hand = []card.Card{winningCard}
	gs.lastPlayerIdx = gs.currentPlayer
	gs.mu.Unlock()
	require.NoError(t, gs.HandlePlayCards(winner.ID, []protocol.CardInfo{convert.CardToInfo(winningCard)}))

	snapshot := gs.BuildGameStateDTO(winner.ID, sessionManager)
	repeatedSnapshot := gs.BuildGameStateDTO(winner.ID, sessionManager)
	assert.Equal(t, "ended", snapshot.Phase)
	assert.Equal(t, snapshot.SnapshotVersion, repeatedSnapshot.SnapshotVersion)
	assert.Equal(t, room.RoomStateWaiting, gs.room.State())
	winnerRecipient, ok := gs.room.PrivateRecipient(winner.ID)
	require.True(t, ok)
	assert.Equal(t, gs.room.Code, winnerRecipient.GetRoom())
	winnerClient, ok := winnerRecipient.(*testutil.SimpleClient)
	require.True(t, ok)
	require.NotEmpty(t, winnerClient.Messages)
	gameOverMessage := winnerClient.Messages[len(winnerClient.Messages)-1]
	assert.Equal(t, protocol.MsgGameOver, gameOverMessage.Type)
	require.NotNil(t, gameOverMessage.Event)
	assert.Greater(t, snapshot.SnapshotVersion, gameOverMessage.Event.EventVersion)
	assert.Empty(t, snapshot.CurrentTurn)
	assert.Zero(t, snapshot.TurnDeadlineMS)
	assert.False(t, snapshot.MustPlay)
	assert.False(t, snapshot.CanBeat)
	assert.False(t, snapshot.IsGrab)
	assert.True(t, snapshot.BottomCardsRevealed)
	assert.Len(t, snapshot.BottomCards, 3)
	assert.Empty(t, snapshot.Hand)
	assert.Equal(t, winner.ID, snapshot.LastPlayerID)
	assert.Equal(t, []protocol.CardInfo{convert.CardToInfo(winningCard)}, ledgerForPlayer(t, snapshot.PlayedCards, winner.ID))
	assert.Equal(t, 2, snapshot.Multiplier, "landlord spring multiplier must be settled in ended snapshots")
}

func TestTurnDeadlinePausesAndResumesWithoutChangingTurnID(t *testing.T) {
	t.Parallel()

	gs, sessionManager := newSnapshotTestSession(t)
	gs.Start()
	currentPlayerID := gs.players[gs.currentBidder].ID
	active := gs.BuildGameStateDTO(currentPlayerID, sessionManager)
	require.Positive(t, active.TurnDeadlineMS)

	sessionManager.SetOffline(currentPlayerID)
	gs.PlayerOffline(currentPlayerID)
	paused := gs.BuildGameStateDTO(currentPlayerID, sessionManager)
	assert.Equal(t, active.TurnID, paused.TurnID)
	assert.Zero(t, paused.TurnDeadlineMS)

	sessionManager.SetOnline(currentPlayerID)
	gs.PlayerOnline(currentPlayerID)
	resumed := gs.BuildGameStateDTO(currentPlayerID, sessionManager)
	assert.Equal(t, active.TurnID, resumed.TurnID)
	assert.Greater(t, resumed.TurnDeadlineMS, resumed.ServerTimeMS)
	assert.Greater(t, resumed.SnapshotVersion, paused.SnapshotVersion)
}

type turnObservingClient struct {
	*testutil.SimpleClient
	onTurn func(*protocol.Message)
}

func (client *turnObservingClient) SendMessage(message *protocol.Message) error {
	if message.Type == protocol.MsgBidTurn || message.Type == protocol.MsgPlayTurn {
		client.onTurn(message)
	}
	return client.SimpleClient.SendMessage(message)
}

func TestTurnDeadlineExistsBeforeTurnBroadcast(t *testing.T) {
	t.Parallel()

	clients := []*turnObservingClient{
		{SimpleClient: testutil.NewSimpleClient("p1", "Player1")},
		{SimpleClient: testutil.NewSimpleClient("p2", "Player2")},
		{SimpleClient: testutil.NewSimpleClient("p3", "Player3")},
	}
	gameRoom := room.NewMockRoom("DEADLINE", clients[0])
	gameRoom.AddPlayerForTest(clients[1], 1, false)
	gameRoom.AddPlayerForTest(clients[2], 2, false)
	gameRoom.SetPlayerOrderForTest([]string{"p1", "p2", "p3"})
	gs := NewGameSession(gameRoom, storage.NewLeaderboardManager(nil), config.GameConfig{BidTimeout: 30, TurnTimeout: 40})
	t.Cleanup(gs.StopAllTimers)

	observations := 0
	for _, client := range clients {
		client.onTurn = func(message *protocol.Message) {
			gs.timerMu.Lock()
			deadline := gs.turnDeadline
			gs.timerMu.Unlock()
			assert.False(t, deadline.IsZero())
			assert.Positive(t, gs.turnID)
			require.NotNil(t, message.Event)
			assert.Equal(t, gs.gameID, message.Event.GameID)
			assert.Equal(t, gs.turnID, message.Event.TurnID)
			assert.Equal(t, deadline.UnixMilli(), message.Event.TurnDeadlineMS)
			assert.LessOrEqual(t, message.Event.ServerTimeMS, message.Event.TurnDeadlineMS)
			assert.Positive(t, message.Event.EventVersion)
			observations++
		}
	}

	gs.Start()
	assert.Equal(t, 3, observations)
}

func TestAuthoritativeGameEventsAdvanceMonotonically(t *testing.T) {
	gs, sessionManager := newSnapshotTestSession(t)
	gs.Start()

	clientRecipient, ok := gs.room.PrivateRecipient("p1")
	require.True(t, ok)
	client, ok := clientRecipient.(*testutil.SimpleClient)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(client.Messages), 3)
	assert.Equal(t, protocol.MsgGameStart, client.Messages[0].Type)
	assert.Equal(t, protocol.MsgDealCards, client.Messages[1].Type)
	assert.Equal(t, protocol.MsgBidTurn, client.Messages[2].Type)
	for _, message := range client.Messages[:3] {
		require.NotNil(t, message.Event)
		assert.Equal(t, gs.gameID, message.Event.GameID)
	}
	assertStrictlyIncreasingEventVersions(t, client.Messages)
	firstSnapshot := gs.BuildGameStateDTO("p1", sessionManager)
	require.NotEmpty(t, client.Messages)
	assert.Equal(t, client.Messages[len(client.Messages)-1].Event.EventVersion, firstSnapshot.SnapshotVersion)

	currentBidder := gs.players[gs.currentBidder]
	require.NoError(t, gs.HandleBid(currentBidder.ID, false))
	assertStrictlyIncreasingEventVersions(t, client.Messages)
	secondSnapshot := gs.BuildGameStateDTO("p1", sessionManager)
	assert.Greater(t, secondSnapshot.SnapshotVersion, firstSnapshot.SnapshotVersion)
	assert.Equal(t, client.Messages[len(client.Messages)-1].Event.EventVersion, secondSnapshot.SnapshotVersion)

	beforeRejected := secondSnapshot.SnapshotVersion
	wrongPlayer := gs.players[(gs.currentBidder+1)%len(gs.players)]
	assert.ErrorIs(t, gs.HandleBid(wrongPlayer.ID, false), apperrors.ErrNotYourTurn)
	assert.Equal(t, beforeRejected, gs.BuildGameStateDTO("p1", sessionManager).SnapshotVersion)

	caller := gs.players[gs.currentBidder]
	require.NoError(t, gs.HandleBid(caller.ID, true))
	for range 2 {
		bidder := gs.players[gs.currentBidder]
		require.NoError(t, gs.HandleBid(bidder.ID, false))
	}
	require.Equal(t, GameStatePlaying, gs.state)

	var nonLandlordClient *testutil.SimpleClient
	for _, player := range gs.players {
		if !player.IsLandlord {
			recipient, ok := gs.room.PrivateRecipient(player.ID)
			require.True(t, ok)
			nonLandlordClient, ok = recipient.(*testutil.SimpleClient)
			require.True(t, ok)
			break
		}
	}
	require.NotNil(t, nonLandlordClient)
	assertContainsEventVersionGap(t, nonLandlordClient.Messages)

	lead := gs.players[gs.currentPlayer]
	leadCard := lead.Hand[len(lead.Hand)-1]
	require.NoError(t, gs.HandlePlayCards(lead.ID, []protocol.CardInfo{convert.CardToInfo(leadCard)}))
	passer := gs.players[gs.currentPlayer]
	require.NoError(t, gs.HandlePass(passer.ID))

	assertStrictlyIncreasingEventVersions(t, client.Messages)
	finalSnapshot := gs.BuildGameStateDTO("p1", sessionManager)
	assert.Equal(t, client.Messages[len(client.Messages)-1].Event.EventVersion, finalSnapshot.SnapshotVersion)
	messageTypes := make(map[protocol.MessageType]bool)
	for _, message := range client.Messages {
		messageTypes[message.Type] = true
	}
	for _, messageType := range []protocol.MessageType{
		protocol.MsgGameStart,
		protocol.MsgDealCards,
		protocol.MsgBidTurn,
		protocol.MsgBidResult,
		protocol.MsgLandlord,
		protocol.MsgPlayTurn,
		protocol.MsgCardPlayed,
		protocol.MsgPlayerPass,
	} {
		assert.True(t, messageTypes[messageType], "missing authoritative event %s", messageType)
	}
}

func assertStrictlyIncreasingEventVersions(t *testing.T, messages []*protocol.Message) {
	t.Helper()
	var previous int64
	for _, message := range messages {
		require.NotNil(t, message.Event, "authoritative session message %s has no event metadata", message.Type)
		assert.Equal(t, "game:"+message.Event.GameID, message.Event.StreamID)
		assert.Greater(t, message.Event.EventVersion, previous)
		previous = message.Event.EventVersion
	}
	assert.Positive(t, previous)
}

func assertContainsEventVersionGap(t *testing.T, messages []*protocol.Message) {
	t.Helper()
	var previous int64
	for _, message := range messages {
		require.NotNil(t, message.Event)
		if previous > 0 && message.Event.EventVersion > previous+1 {
			return
		}
		previous = message.Event.EventVersion
	}
	t.Fatal("expected a version gap for a private event sent to another player")
}
