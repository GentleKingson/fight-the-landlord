package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/palemoky/fight-the-landlord/internal/config"
	r "github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	payloadconv "github.com/palemoky/fight-the-landlord/internal/protocol/convert/payload"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

// Helper to create a room with a running game session and mock clients
func setupGameRoom(t *testing.T) (*r.Room, *session.GameSession, []*testutil.MockClient) {
	t.Helper()
	room := &r.Room{
		Code:        "123",
		Players:     make(map[string]*r.RoomPlayer),
		PlayerOrder: []string{"p1", "p2", "p3"},
	}

	clients := make([]*testutil.MockClient, 3)
	for i := range 3 {
		c := new(testutil.MockClient)
		id := room.PlayerOrder[i]
		c.On("GetID").Return(id)
		c.On("GetName").Return("Player" + id)
		c.On("GetRoom").Return("123")
		// Unexpected calls allowed for setup
		c.On("SetRoom", mock.Anything).Maybe()
		c.On("Close").Maybe()
		c.On("SendMessage", mock.Anything).Maybe()

		room.Players[id] = &r.RoomPlayer{
			Client: c,
			Seat:   i,
			Ready:  true,
		}
		clients[i] = c
	}

	// Create and start session
	gs := session.NewGameSession(room, nil, config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	return room, gs, clients
}

func TestHandler_HandleBid_Success(t *testing.T) {
	room, gs, clients := setupGameRoom(t)

	mockServer := new(testutil.MockServer)

	// Create real RoomManager and add room
	rm := r.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	rm.AddRoomForTest(room)

	h := NewHandler(HandlerDeps{
		Server:      mockServer,
		RoomManager: rm,
	})
	h.SetGameSession(room.Code, gs)

	assert.NotNil(t, h.GetGameSession(room.Code))

	// 叫地主者叫，其余玩家不抢，叫地主者成为地主并进入出牌阶段
	driveBiddingToPlay(t, h, room, gs, clients)

	assert.Equal(t, session.GameStatePlaying, gs.GetStateForSerialization(),
		"One player should successfully bid and become landlord")
}

// driveBiddingToPlay 驱动叫抢地主流程直至进入出牌阶段：
// 当前叫地主者叫地主，随后每个当前抢家都不抢，最终叫地主者成为地主。
func driveBiddingToPlay(t *testing.T, h *Handler, room *r.Room, gs *session.GameSession, clients []*testutil.MockClient) {
	t.Helper()

	clientByID := make(map[string]*testutil.MockClient, len(clients))
	for _, c := range clients {
		clientByID[c.GetID()] = c
	}

	sendBid := func(bidderIdx int, bid bool) {
		id := room.PlayerOrder[bidderIdx]
		payloadBytes, _ := payloadconv.EncodePayload(protocol.MsgBid, protocol.BidPayload{Bid: bid})
		h.handleBid(clientByID[id], &protocol.Message{Type: protocol.MsgBid, Payload: payloadBytes})
	}

	// 第一个当前玩家叫地主
	sendBid(gs.GetCurrentBidderForSerialization(), true)

	// 其余玩家依次不抢，直到确定地主
	for i := 0; i < 5 && gs.GetStateForSerialization() == session.GameStateBidding; i++ {
		sendBid(gs.GetCurrentBidderForSerialization(), false)
	}
}

func TestHandler_HandlePlayCards_Success(t *testing.T) {
	room, gs, clients := setupGameRoom(t)

	mockServer := new(testutil.MockServer)

	// Create real RoomManager and add room
	rm := r.NewRoomManager(nil, config.GameConfig{RoomTimeout: 10})
	rm.AddRoomForTest(room)

	h := NewHandler(HandlerDeps{
		Server:      mockServer,
		RoomManager: rm,
	})
	h.SetGameSession(room.Code, gs)

	assert.NotNil(t, h.GetGameSession(room.Code))

	// Force bidding phase to pass by simulating valid bids
	mockLdb := new(testutil.MockLeaderboard)
	mockServer.On("GetLeaderboard").Return(mockLdb)

	// 驱动叫抢地主流程进入出牌阶段
	driveBiddingToPlay(t, h, room, gs, clients)
	assert.Equal(t, session.GameStatePlaying, gs.GetStateForSerialization(), "Should reach playing phase")

	// Identify Landlord Client
	landlordIdx := gs.GetCurrentPlayerForSerialization()
	landlordID := room.PlayerOrder[landlordIdx]

	var landlordClient *testutil.MockClient
	for _, c := range clients {
		if c.GetID() == landlordID {
			landlordClient = c
			break
		}
	}
	assert.NotNil(t, landlordClient)

	// Play cards
	players := gs.GetPlayersForSerialization()
	landlordPlayer := players[landlordIdx]

	cardToPlay := landlordPlayer.Hand[0]
	playPayload := protocol.PlayCardsPayload{
		Cards: []protocol.CardInfo{
			{Suit: int(cardToPlay.Suit), Rank: int(cardToPlay.Rank), Color: int(cardToPlay.Color)},
		},
	}
	payloadBytes, _ := payloadconv.EncodePayload(protocol.MsgPlayCards, playPayload)
	msg := &protocol.Message{Type: protocol.MsgPlayCards, Payload: payloadBytes}

	h.handlePlayCards(landlordClient, msg)

	// Verify hand size decreased
	assert.Equal(t, 19, len(landlordPlayer.Hand))
}
