package handler

import (
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/ui/model"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestTUIDealWaitsForOfficialBidTurn(t *testing.T) {
	m := model.NewOnlineModel("localhost:1780")
	m.SetPlayerInfo("me", "Me")
	m.SetPhase(model.PhaseBidding)
	m.Game().SetBidTurn("me")
	m.Game().SetTimerDuration(15 * time.Second)
	m.Game().State().BidToken = "old"
	m.Game().State().Multiplier = 4
	msg := codec.MustNewMessage(protocol.MsgDealCards, protocol.DealCardsPayload{DealID: "round-1", DealStartAt: 1000, DealEndAt: 4870, ServerTime: 1000})
	handleMsgDealCards(m, msg)
	require.Equal(t, model.PhaseBidding, m.Phase())
	require.Empty(t, m.Game().BidTurn())
	require.Empty(t, m.Game().State().BidToken)
	require.Zero(t, m.Game().TimerDuration())
	require.Empty(t, m.Game().State().BottomCards)
	require.Equal(t, 1, m.Game().State().Multiplier)
	require.NotNil(t, handleMsgBidTurn(m, codec.MustNewMessage(protocol.MsgBidTurn, protocol.BidTurnPayload{PlayerID: "me", Timeout: 15, BidToken: "new"})))
	require.Equal(t, "me", m.Game().BidTurn())
	require.Equal(t, "new", m.Game().State().BidToken)
	require.Equal(t, 15*time.Second, m.Game().TimerDuration())
	// The landlord's hand refresh carries no deal ID and cannot clear the turn.
	handleMsgDealCards(m, codec.MustNewMessage(protocol.MsgDealCards, protocol.DealCardsPayload{}))
	require.Equal(t, "new", m.Game().State().BidToken)
	handleMsgDealCards(m, codec.MustNewMessage(protocol.MsgDealCards, protocol.DealCardsPayload{DealID: "round-2"}))
	require.Empty(t, m.Game().BidTurn())
}

func TestTUIDealingReconnectHasNoActiveBidder(t *testing.T) {
	m := model.NewOnlineModel("localhost:1780")
	m.SetPlayerInfo("me", "Me")
	m.Game().SetBidTurn("me")
	m.Game().State().BidToken = "old"
	handleMsgReconnected(m, codec.MustNewMessage(protocol.MsgReconnected, protocol.ReconnectedPayload{
		PlayerID: "me", PlayerName: "Me", RoomCode: "123456", GameState: &protocol.GameStateDTO{Phase: "dealing", DealID: "round-2"},
	}))
	require.Equal(t, model.PhaseBidding, m.Phase())
	require.Empty(t, m.Game().BidTurn())
	require.Empty(t, m.Game().State().BidToken)
	require.Equal(t, "round-2", m.Game().State().DealID)
	require.Zero(t, m.Game().TimerDuration())
}
