package session

import (
	"fmt"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/stretchr/testify/require"
)

func classicBidGame(t *testing.T, start int) *GameSession {
	t.Helper()
	r := room.NewMockRoom("CLASSIC", testutil.NewSimpleClient("a", "A"))
	r.Players["b"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("b", "B"), Seat: 1}
	r.Players["c"] = &room.RoomPlayer{Client: testutil.NewSimpleClient("c", "C"), Seat: 2}
	r.PlayerOrder = []string{"a", "b", "c"}
	gs := NewGameSession(r, nil, config.GameConfig{BidTimeout: 600, TurnTimeout: 600})
	gs.Start()
	gs.finishDeal(gs.dealID)
	t.Cleanup(gs.StopAllTimers)
	// 控制首叫座位，避免测试依赖随机抽样。
	gs.currentBidder = start
	return gs
}

func TestClassicBidOrder(t *testing.T) {
	cases := []struct {
		name                 string
		seats                []int
		bids                 []bool
		landlord, multiplier int
	}{
		{"A叫无人抢", []int{0, 1, 2}, []bool{true, false, false}, 0, 1},
		{"C抢A放弃", []int{0, 1, 2, 0}, []bool{true, false, true, false}, 2, 2},
		{"C抢A反抢", []int{0, 1, 2, 0}, []bool{true, false, true, true}, 0, 4},
		{"B抢CA放弃", []int{0, 1, 2, 0}, []bool{true, true, false, false}, 1, 2},
		{"B抢A反抢", []int{0, 1, 2, 0}, []bool{true, true, false, true}, 0, 4},
		{"BC抢A放弃", []int{0, 1, 2, 0}, []bool{true, true, true, false}, 2, 4},
		{"BCA各抢一次", []int{0, 1, 2, 0}, []bool{true, true, true, true}, 0, 8},
		{"A不叫B叫C不抢", []int{0, 1, 2}, []bool{false, true, false}, 1, 1},
		{"A不叫C抢B放弃", []int{0, 1, 2, 1}, []bool{false, true, true, false}, 2, 2},
		{"A不叫C抢B反抢", []int{0, 1, 2, 1}, []bool{false, true, true, true}, 1, 4},
		{"AB不叫C直接成为地主", []int{0, 1, 2}, []bool{false, false, true}, 2, 1},
	}
	for start := range 3 {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("start%d/%s", start, tc.name), func(t *testing.T) {
				gs := classicBidGame(t, start)
				for step, relative := range tc.seats {
					seat := (start + relative) % 3
					require.Equal(t, GameStateBidding, gs.state)
					require.Equal(t, seat, gs.currentBidder, "step %d", step)
					isGrab := gs.landlordCaller != -1
					if isGrab {
						require.False(t, gs.declinedCall[seat])
						require.False(t, gs.grabDecided[seat])
					}
					// 重连补发当前轮次必须与资格状态一致，且不重启计时器。
					client := gs.room.Players[gs.players[seat].ID].Client.(*testutil.SimpleClient)
					timer, turn := gs.turnTimer, gs.turnID
					gs.ResendTurnTo(client)
					payload, err := codec.ParsePayload[protocol.BidTurnPayload](client.Messages[len(client.Messages)-1])
					require.NoError(t, err)
					require.Equal(t, gs.players[seat].ID, payload.PlayerID)
					require.Equal(t, isGrab, payload.IsGrab)
					require.Same(t, timer, gs.turnTimer)
					require.Equal(t, turn, gs.turnID)
					wrong := gs.players[(seat+1)%3].ID
					require.ErrorIs(t, gs.HandleBid(wrong, true), apperrors.ErrNotYourTurn)
					require.Equal(t, turn, gs.turnID)
					require.NoError(t, gs.HandleBid(gs.players[seat].ID, tc.bids[step]))
					if !isGrab && !tc.bids[step] {
						require.True(t, gs.declinedCall[seat])
					}
					if isGrab {
						require.True(t, gs.grabDecided[seat])
					}
				}
				require.Equal(t, GameStatePlaying, gs.state)
				require.Equal(t, (start+tc.landlord)%3, gs.landlordCandidate)
				require.Equal(t, tc.multiplier, gs.bidMultiplier)
				assertRoundCards(t, gs, true)
			})
		}
	}
}

func TestClassicBidTimeoutLosesEligibility(t *testing.T) {
	for _, source := range []string{"timeout", "offline"} {
		t.Run(source, func(t *testing.T) {
			gs := classicBidGame(t, 0)
			oldTurn := gs.turnID
			gs.handleTurnTimeout(oldTurn, source) // A 不叫
			require.True(t, gs.declinedCall[0])
			require.NoError(t, gs.HandleBid("b", true))
			require.Equal(t, 2, gs.currentBidder)
			require.ErrorIs(t, gs.HandleBid("a", true), apperrors.ErrNotYourTurn)
			require.NoError(t, gs.HandleBid("c", true))
			require.Equal(t, 1, gs.currentBidder) // 跳过 A，B 反抢
			gs.handleTurnTimeout(oldTurn, source) // 旧回调不可消耗 B 的机会
			require.False(t, gs.grabDecided[1])
			gs.handleTurnTimeout(gs.turnID, source)
			require.Equal(t, GameStatePlaying, gs.state)
			require.Equal(t, 2, gs.landlordCandidate)
			require.Equal(t, 2, gs.bidMultiplier)
		})
	}
}
