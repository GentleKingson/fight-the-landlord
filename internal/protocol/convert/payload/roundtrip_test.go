package payload

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

func TestEncodePayload_NilPayload(t *testing.T) {
	t.Parallel()

	data, err := EncodePayload(protocol.MsgPing, nil)
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestDecodePayload_EmptyData(t *testing.T) {
	t.Parallel()

	var target protocol.PingPayload
	err := DecodePayload(protocol.MsgPing, []byte{}, &target)
	require.NoError(t, err)
}

func TestPayloadRoundTrip_ClientRequests(t *testing.T) {
	t.Parallel()

	assertRoundTrip(t, "Ping", protocol.MsgPing, protocol.PingPayload{Timestamp: 1234567890})

	assertRoundTrip(t, "JoinRoom", protocol.MsgJoinRoom, protocol.JoinRoomPayload{RoomCode: "123456"})

	assertRoundTrip(t, "Bid", protocol.MsgBid, protocol.BidPayload{Bid: true})

	assertRoundTrip(t, "PlayCards", protocol.MsgPlayCards, protocol.PlayCardsPayload{Cards: []protocol.CardInfo{{Suit: 0, Rank: 3, Color: 0}}})

	assertRoundTrip(t, "GetLeaderboard", protocol.MsgGetLeaderboard, protocol.GetLeaderboardPayload{Type: "total", Offset: 0, Limit: 10})
}

func TestPayloadRoundTrip_ServerResponses(t *testing.T) {
	t.Parallel()

	assertRoundTrip(t, "Connected", protocol.MsgConnected, protocol.ConnectedPayload{
		PlayerID:   "p1",
		PlayerName: "Player1",
	})

	assertRoundTrip(t, "Pong", protocol.MsgPong, protocol.PongPayload{
		ClientTimestamp: 1234567890,
		ServerTimestamp: 1234567891,
	})

	assertRoundTrip(t, "OnlineCount", protocol.MsgOnlineCount, protocol.OnlineCountPayload{Count: 42})

	t.Run("StartGame empty payload", func(t *testing.T) {
		t.Parallel()
		data, err := EncodePayload(protocol.MsgStartGame, nil)
		require.NoError(t, err)
		assert.Nil(t, data)
	})

	assertRoundTrip(t, "RoomCreated", protocol.MsgRoomCreated, protocol.RoomCreatedPayload{
		RoomCode: "123456",
		OwnerID:  "p1",
		Player: protocol.PlayerInfo{
			ID:     "p1",
			Name:   "Player1",
			Seat:   0,
			Online: true,
		},
	})

	assertRoundTrip(t, "RoomJoined", protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
		RoomCode: "123456",
		OwnerID:  "p1",
		Player: protocol.PlayerInfo{
			ID:     "p2",
			Name:   "Player2",
			Seat:   1,
			Online: true,
		},
		Players: []protocol.PlayerInfo{
			{ID: "p1", Name: "Player1", Seat: 0, Online: true},
			{ID: "p2", Name: "Player2", Seat: 1, Online: true},
		},
	})

	assertRoundTrip(t, "PlayerLeft", protocol.MsgPlayerLeft, protocol.PlayerLeftPayload{
		PlayerID:   "p1",
		PlayerName: "Player1",
		OwnerID:    "p2",
	})

	assertRoundTrip(t, "Error", protocol.MsgError, protocol.ErrorPayload{
		Code:        404,
		Message:     "Not found",
		RequestType: protocol.MsgJoinRoom,
	})
}

func TestPayloadRoundTrip_GameMessages(t *testing.T) {
	t.Parallel()

	assertRoundTrip(t, "GameStart", protocol.MsgGameStart, protocol.GameStartPayload{
		Players: []protocol.PlayerInfo{
			{ID: "p1", Name: "Player1", Seat: 0},
			{ID: "p2", Name: "Player2", Seat: 1, IsBot: true},
			{ID: "p3", Name: "Player3", Seat: 2},
		},
	})

	assertRoundTrip(t, "DealCards", protocol.MsgDealCards, protocol.DealCardsPayload{
		Cards: []protocol.CardInfo{
			{Suit: 0, Rank: 3, Color: 0},
			{Suit: 1, Rank: 14, Color: 1},
		},
		BottomCards: []protocol.CardInfo{
			{Suit: 2, Rank: 5, Color: 0},
		},
	})

	assertRoundTrip(t, "BidTurn", protocol.MsgBidTurn, protocol.BidTurnPayload{
		PlayerID:   "p1",
		Timeout:    30,
		IsGrab:     true,
		Multiplier: 2,
	})

	assertRoundTrip(t, "BidResult", protocol.MsgBidResult, protocol.BidResultPayload{
		PlayerID:   "p1",
		PlayerName: "Player1",
		Bid:        true,
		IsGrab:     true,
		Multiplier: 4,
	})

	assertRoundTrip(t, "Landlord", protocol.MsgLandlord, protocol.LandlordPayload{
		PlayerID:    "p1",
		PlayerName:  "Player1",
		BottomCards: []protocol.CardInfo{{Suit: 0, Rank: 3, Color: 0}},
		Multiplier:  3,
	})

	assertRoundTrip(t, "PlayTurn", protocol.MsgPlayTurn, protocol.PlayTurnPayload{
		PlayerID: "p1",
		Timeout:  30,
		MustPlay: true,
		CanBeat:  false,
	})

	assertRoundTrip(t, "CardPlayed", protocol.MsgCardPlayed, protocol.CardPlayedPayload{
		PlayerID:   "p1",
		PlayerName: "Player1",
		Cards:      []protocol.CardInfo{{Suit: 0, Rank: 3, Color: 0}},
		CardsLeft:  16,
		HandType:   "Single",
	})

	assertRoundTrip(t, "PlayerPass", protocol.MsgPlayerPass, protocol.PlayerPassPayload{
		PlayerID:   "p1",
		PlayerName: "Player1",
	})

	assertRoundTrip(t, "GameOver", protocol.MsgGameOver, protocol.GameOverPayload{
		WinnerID:   "p1",
		WinnerName: "Player1",
		IsLandlord: true,
		PlayerHands: []protocol.PlayerHand{
			{PlayerID: "p1", PlayerName: "Player1", Cards: []protocol.CardInfo{}},
		},
		Multiplier: 8,
		Scores: []protocol.PlayerScore{
			{PlayerID: "p1", PlayerName: "Player1", IsLandlord: true, Score: 16},
			{PlayerID: "p2", PlayerName: "Player2", IsLandlord: false, Score: -8},
		},
	})
}

func TestPayloadRoundTrip_RoomMessages(t *testing.T) {
	t.Parallel()

	assertRoundTrip(t, "RoomJoined", protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
		RoomCode: "123456",
		Player:   protocol.PlayerInfo{ID: "p1", Name: "Player1"},
		Players: []protocol.PlayerInfo{
			{ID: "p1", Name: "Player1"},
			{ID: "p2", Name: "Player2"},
		},
	})

	assertRoundTrip(t, "PlayerJoined", protocol.MsgPlayerJoined, protocol.PlayerJoinedPayload{
		Player: protocol.PlayerInfo{ID: "p2", Name: "Player2", Seat: 1},
	})

	assertRoundTrip(t, "PlayerLeft", protocol.MsgPlayerLeft, protocol.PlayerLeftPayload{
		PlayerID:   "p1",
		PlayerName: "Player1",
	})

	assertRoundTrip(t, "PlayerReady", protocol.MsgPlayerReady, protocol.PlayerReadyPayload{
		PlayerID: "p1",
		Ready:    true,
	})
}

func TestPayloadRoundTrip_StatsAndLeaderboard(t *testing.T) {
	t.Parallel()

	assertRoundTrip(t, "StatsResult", protocol.MsgStatsResult, protocol.StatsResultPayload{
		PlayerID:      "p1",
		PlayerName:    "Player1",
		TotalGames:    100,
		Wins:          60,
		Losses:        40,
		WinRate:       0.6,
		LandlordGames: 50,
		LandlordWins:  30,
		FarmerGames:   50,
		FarmerWins:    30,
		Score:         1000,
		Rank:          5,
		CurrentStreak: 3,
		MaxWinStreak:  10,
	})

	assertRoundTrip(t, "LeaderboardResult", protocol.MsgLeaderboardResult, protocol.LeaderboardResultPayload{
		Type: "total",
		Entries: []protocol.LeaderboardEntry{
			{Rank: 1, PlayerID: "p1", PlayerName: "Champion", Score: 1000},
			{Rank: 2, PlayerID: "p2", PlayerName: "Runner", Score: 900},
		},
	})

	assertRoundTrip(t, "RoomListResult", protocol.MsgRoomListResult, protocol.RoomListResultPayload{
		Rooms: []protocol.RoomListItem{
			{RoomCode: "123456", PlayerCount: 2, MaxPlayers: 3},
		},
	})
}

func TestPayloadRoundTrip_ReconnectionMessages(t *testing.T) {
	t.Parallel()

	assertRoundTrip(t, "Reconnected with GameState", protocol.MsgReconnected, protocol.ReconnectedPayload{
		PlayerID:   "p1",
		PlayerName: "Player1",
		RoomCode:   "123456",
		GameState: &protocol.GameStateDTO{
			Phase:       "playing",
			BidHistory:  []protocol.PublicBid{},
			PublicPlays: []protocol.PublicPlay{},
			LastPlayed:  []protocol.CardInfo{},
			Players: []protocol.PlayerInfo{
				{ID: "p1", Name: "Player1", Seat: 0, IsLandlord: true},
				{ID: "p2", Name: "Player2", Seat: 1, IsBot: true},
			},
			Hand:        []protocol.CardInfo{{Suit: 0, Rank: 3}},
			BottomCards: []protocol.CardInfo{{Suit: 1, Rank: 5}},
			CurrentTurn: "p1",
			MustPlay:    true,
		},
	})

	assertRoundTrip(t, "PlayerOffline", protocol.MsgPlayerOffline, protocol.PlayerOfflinePayload{
		PlayerID:   "p1",
		PlayerName: "Player1",
		Timeout:    120,
	})

	assertRoundTrip(t, "PlayerOnline", protocol.MsgPlayerOnline, protocol.PlayerOnlinePayload{
		PlayerID:   "p1",
		PlayerName: "Player1",
	})
}

func TestPayloadRoundTrip_MaintenanceMessages(t *testing.T) {
	t.Parallel()

	assertRoundTrip(t, "Maintenance", protocol.MsgMaintenancePush, protocol.MaintenancePayload{Maintenance: true})

	assertRoundTrip(t, "MaintenanceStatus", protocol.MsgMaintenancePull, protocol.MaintenanceStatusPayload{Maintenance: false})
}

func assertRoundTrip[T any](t *testing.T, name string, messageType protocol.MessageType, original T) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Parallel()
		data, err := EncodePayload(messageType, original)
		require.NoError(t, err)
		var result T
		require.NoError(t, DecodePayload(messageType, data, &result))
		assert.Equal(t, original, result)
	})
}
