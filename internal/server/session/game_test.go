package session

import (
	"testing"

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

func TestHandleBid_Success(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// 叫地主的玩家
	caller := gs.players[gs.currentBidder]
	err := gs.HandleBid(caller.ID, true)
	require.NoError(t, err)

	// 叫地主后进入抢地主阶段，尚未确定地主
	assert.Equal(t, GameStateBidding, gs.state)
	assert.Equal(t, 1, gs.bidMultiplier)

	// 其余两名玩家都不抢，叫地主者成为地主
	for range 2 {
		grabber := gs.players[gs.currentBidder]
		require.NoError(t, gs.HandleBid(grabber.ID, false))
	}

	// 验证地主确定，底倍为 1
	assert.Equal(t, GameStatePlaying, gs.state)
	assert.True(t, caller.IsLandlord)
	assert.Equal(t, 1, gs.bidMultiplier)
}

func TestHandleBid_Grab_DoublesMultiplier(t *testing.T) {
	t.Parallel()

	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// 叫地主
	caller := gs.players[gs.currentBidder]
	require.NoError(t, gs.HandleBid(caller.ID, true))
	assert.Equal(t, 1, gs.bidMultiplier)

	// 下一位抢地主 → 倍数翻倍，暂定地主易主
	grabber := gs.players[gs.currentBidder]
	require.NoError(t, gs.HandleBid(grabber.ID, true))
	assert.Equal(t, 2, gs.bidMultiplier)
	assert.Equal(t, grabber.ID, gs.players[gs.landlordCandidate].ID)

	// 余下两名玩家都放弃，抢地主结束，倍数 2，地主为最后的抢家
	for range 2 {
		p := gs.players[gs.currentBidder]
		require.NoError(t, gs.HandleBid(p.ID, false))
	}
	assert.Equal(t, GameStatePlaying, gs.state)
	assert.True(t, grabber.IsLandlord)
	assert.Equal(t, 2, gs.bidMultiplier)
}

func TestHandleBid_AllGrab_EndsAfterOneRound(t *testing.T) {
	t.Parallel()

	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// A 叫地主，随后 B、C、A 依次抢（每人最多一次），抢地主必须结束，倍数不会无限翻倍
	caller := gs.players[gs.currentBidder]
	require.NoError(t, gs.HandleBid(caller.ID, true))

	// 抢地主阶段所有人都抢：B、C 与叫地主者 A 的反抢，共 3 次决策
	grabbers := make([]string, 0, 3)
	for range 3 {
		p := gs.players[gs.currentBidder]
		grabbers = append(grabbers, p.ID)
		require.NoError(t, gs.HandleBid(p.ID, true))
	}

	// 抢满一轮后强制结束，进入出牌阶段
	assert.Equal(t, GameStatePlaying, gs.state)
	// 3 次抢，倍数翻 3 次：1 → 2 → 4 → 8
	assert.Equal(t, 8, gs.bidMultiplier)
	// 最后一个抢地主者（叫地主者 A 的反抢）成为地主
	assert.Equal(t, grabbers[len(grabbers)-1], gs.players[gs.landlordCandidate].ID)
	assert.True(t, caller.IsLandlord)
}

func TestHandleBid_NotYourTurn(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// Try to bid with wrong player
	wrongPlayer := gs.players[(gs.currentBidder+1)%3]
	err := gs.HandleBid(wrongPlayer.ID, true)
	assert.ErrorIs(t, err, apperrors.ErrNotYourTurn)
}

func TestHandleBid_GameNotStarted(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	// Don't start the game

	err := gs.HandleBid("p1", true)
	assert.ErrorIs(t, err, apperrors.ErrGameNotStart)
}

func TestHandleBid_AllPass_Redeal(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// 所有玩家都不叫 → 流局重新发牌
	for range 3 {
		currentBidder := gs.players[gs.currentBidder]
		require.NoError(t, gs.HandleBid(currentBidder.ID, false))
	}

	// 重新发牌后仍处于叫地主阶段，无人成为地主，状态被重置
	assert.Equal(t, GameStateBidding, gs.state)
	assert.Equal(t, -1, gs.landlordCaller)
	assert.Equal(t, 0, gs.bidPasses)
	assert.Equal(t, 1, gs.bidMultiplier)
	for _, p := range gs.players {
		assert.False(t, p.IsLandlord)
		assert.Len(t, p.Hand, 17)
	}
}

func TestHandlePlayCards_Success(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// Set landlord and start playing
	gs.mu.Lock()
	gs.state = GameStatePlaying
	gs.currentPlayer = 0
	gs.players[0].IsLandlord = true
	// Give player some cards
	gs.players[0].Hand = []card.Card{
		{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
		{Suit: card.Heart, Rank: card.Rank3, Color: card.Red},
		{Suit: card.Diamond, Rank: card.Rank3, Color: card.Red},
	}
	gs.mu.Unlock()

	// Play cards
	cardsToPlay := []protocol.CardInfo{
		convert.CardToInfo(gs.players[0].Hand[0]),
		convert.CardToInfo(gs.players[0].Hand[1]),
		convert.CardToInfo(gs.players[0].Hand[2]),
	}

	err := gs.HandlePlayCards("p1", cardsToPlay)
	require.NoError(t, err)

	// Verify cards were removed
	assert.Len(t, gs.players[0].Hand, 0)
}

func TestHandlePlayCards_NotYourTurn(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	gs.mu.Lock()
	gs.state = GameStatePlaying
	gs.currentPlayer = 0
	gs.mu.Unlock()

	// Try to play with wrong player
	err := gs.HandlePlayCards("p2", []protocol.CardInfo{})
	assert.ErrorIs(t, err, apperrors.ErrNotYourTurn)
}

func TestHandlePlayCards_InvalidCards(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	gs.mu.Lock()
	gs.state = GameStatePlaying
	gs.currentPlayer = 0
	gs.players[0].Hand = []card.Card{
		{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
	}
	gs.mu.Unlock()

	// Try to play cards not in hand
	invalidCards := []protocol.CardInfo{
		{Suit: int(card.Heart), Rank: int(card.RankA), Color: int(card.Red)},
	}

	err := gs.HandlePlayCards("p1", invalidCards)
	assert.ErrorIs(t, err, apperrors.ErrInvalidCards)
}

func TestHandlePass_Success(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	gs.mu.Lock()
	gs.state = GameStatePlaying
	gs.currentPlayer = 1
	gs.lastPlayerIdx = 0                                                        // Player 0 played last
	gs.lastPlayedHand = rule.ParsedHand{Type: rule.Single, KeyRank: card.Rank3} // Non-empty
	gs.mu.Unlock()

	// Pass successfully
	err := gs.HandlePass("p2")
	require.NoError(t, err)

	// Verify turn moved to next player
	assert.Equal(t, 2, gs.currentPlayer)
}

func TestHandlePass_MustPlay(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	gs.mu.Lock()
	gs.state = GameStatePlaying
	gs.currentPlayer = 0
	gs.lastPlayerIdx = 0 // Same player - must play
	gs.mu.Unlock()

	// Try to pass when must play
	err := gs.HandlePass("p1")
	assert.ErrorIs(t, err, apperrors.ErrMustPlay)
}

func TestHandlePass_TwoPassesNewRound(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	gs.mu.Lock()
	gs.state = GameStatePlaying
	gs.currentPlayer = 1
	gs.lastPlayerIdx = 0
	gs.lastPlayedHand = rule.ParsedHand{Type: rule.Single, KeyRank: card.Rank3} // Non-empty
	gs.consecutivePasses = 1                                                    // Already one pass
	gs.mu.Unlock()

	// Second pass should trigger new round
	err := gs.HandlePass("p2")
	require.NoError(t, err)

	// Verify new round started
	assert.Equal(t, 0, gs.consecutivePasses)
	assert.True(t, gs.lastPlayedHand.IsEmpty())
}

func TestValidateCardsInHand(t *testing.T) {
	t.Parallel()

	// Setup
	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})

	player := &GamePlayer{
		Hand: []card.Card{
			{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
			{Suit: card.Heart, Rank: card.Rank4, Color: card.Red},
			{Suit: card.Diamond, Rank: card.Rank5, Color: card.Red},
		},
	}

	tests := []struct {
		name  string
		cards []card.Card
		valid bool
	}{
		{
			name: "Valid cards",
			cards: []card.Card{
				{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
			},
			valid: true,
		},
		{
			name: "Multiple valid cards",
			cards: []card.Card{
				{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
				{Suit: card.Heart, Rank: card.Rank4, Color: card.Red},
			},
			valid: true,
		},
		{
			name: "Invalid card not in hand",
			cards: []card.Card{
				{Suit: card.Club, Rank: card.RankA, Color: card.Black},
			},
			valid: false,
		},
		{
			name: "Duplicate card (only one in hand)",
			cards: []card.Card{
				{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
				{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := gs.validateCardsInHand(player, tt.cards)
			assert.Equal(t, tt.valid, result)
		})
	}
}

func TestFinalMultiplierAndScores(t *testing.T) {
	t.Parallel()

	newSession := func() *GameSession {
		r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
		r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
		r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
		r.PlayerOrder = []string{"p1", "p2", "p3"}
		gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
		gs.players[0].IsLandlord = true // p1 是地主
		return gs
	}

	t.Run("底倍×炸弹", func(t *testing.T) {
		t.Parallel()
		gs := newSession()
		gs.bidMultiplier = 2 // 抢地主后底倍为 2
		gs.bombCount = 2     // 两个炸弹/王炸
		gs.landlordPlays = 5
		gs.farmerPlays = 3
		mult := gs.finalMultiplier(gs.players[0])
		assert.Equal(t, 8, mult) // 2 × 2 × 2
	})

	t.Run("春天翻倍", func(t *testing.T) {
		t.Parallel()
		gs := newSession()
		gs.bidMultiplier = 1
		gs.landlordPlays = 9
		gs.farmerPlays = 0 // 农民一张未出
		mult := gs.finalMultiplier(gs.players[0])
		assert.Equal(t, 2, mult) // 春天 ×2
	})

	t.Run("反春天翻倍", func(t *testing.T) {
		t.Parallel()
		gs := newSession()
		gs.bidMultiplier = 1
		gs.landlordPlays = 1 // 地主仅首攻出过一手
		gs.farmerPlays = 8
		mult := gs.finalMultiplier(gs.players[1]) // 农民获胜
		assert.Equal(t, 2, mult)                  // 反春天 ×2
	})

	t.Run("地主获胜得分", func(t *testing.T) {
		t.Parallel()
		gs := newSession()
		scores := gs.computeScores(gs.players[0], 3) // 地主获胜，倍数 3
		require.Len(t, scores, 3)
		assert.Equal(t, 6, scores[0].Score)  // 地主 +2×3
		assert.Equal(t, -3, scores[1].Score) // 农民 -3
		assert.Equal(t, -3, scores[2].Score)
	})

	t.Run("农民获胜得分", func(t *testing.T) {
		t.Parallel()
		gs := newSession()
		scores := gs.computeScores(gs.players[1], 2) // 农民获胜，倍数 2
		require.Len(t, scores, 3)
		assert.Equal(t, -4, scores[0].Score) // 地主 -2×2
		assert.Equal(t, 2, scores[1].Score)  // 农民 +2
		assert.Equal(t, 2, scores[2].Score)
	})
}

func TestHandleBid_MaxRedeals_ForceLandlord(t *testing.T) {
	t.Parallel()

	r := room.NewMockRoom("TEST123", testutil.NewSimpleClient("p1", "Player1"))
	r.Players["p2"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p2", "Player2"), Seat: 1}
	r.Players["p3"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("p3", "Player3"), Seat: 2}
	r.PlayerOrder = []string{"p1", "p2", "p3"}

	gs := NewGameSession(r, storage.NewLeaderboardManager(nil), config.GameConfig{TurnTimeout: 30, BidTimeout: 15})
	gs.Start()

	// 持续无人叫地主，反复流局，直到达到上限被强制指定地主
	for i := 0; gs.state == GameStateBidding && i < 100; i++ {
		bidder := gs.players[gs.currentBidder]
		require.NoError(t, gs.HandleBid(bidder.ID, false))
	}

	// 达到流局上限后随机强制指定地主，进入出牌阶段，底倍为 1
	assert.Equal(t, GameStatePlaying, gs.state)
	assert.Equal(t, maxRedeals, gs.redealCount)
	assert.Equal(t, 1, gs.bidMultiplier)
	landlordCount := 0
	for _, p := range gs.players {
		if p.IsLandlord {
			landlordCount++
			assert.Len(t, p.Hand, 20) // 地主含 3 张底牌
		}
	}
	assert.Equal(t, 1, landlordCount)
}
