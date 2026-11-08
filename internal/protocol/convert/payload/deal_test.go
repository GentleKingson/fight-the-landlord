package payload

import (
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDealProtocolRoundTrip(t *testing.T) {
	deal := protocol.DealCardsPayload{DealID: "round-2", DealStartAt: 1730000000000, DealEndAt: 1730000003870, ServerTime: 1730000001000}
	raw, err := EncodePayload(protocol.MsgDealCards, deal)
	require.NoError(t, err)
	var got protocol.DealCardsPayload
	require.NoError(t, DecodePayload(protocol.MsgDealCards, raw, &got))
	require.Equal(t, deal.DealID, got.DealID)
	require.Equal(t, deal.DealStartAt, got.DealStartAt)
	require.Equal(t, deal.DealEndAt, got.DealEndAt)
	require.Equal(t, deal.ServerTime, got.ServerTime)
	for _, bid := range []bool{true, false} {
		original := protocol.BidPayload{Bid: bid, BidToken: "turn-2"}
		raw, err = EncodePayload(protocol.MsgBid, original)
		require.NoError(t, err)
		var got protocol.BidPayload
		require.NoError(t, DecodePayload(protocol.MsgBid, raw, &got))
		require.Equal(t, original, got)
	}
	original := protocol.ReconnectedPayload{GameState: &protocol.GameStateDTO{Phase: "dealing", DealID: deal.DealID, DealStartAt: deal.DealStartAt, DealEndAt: deal.DealEndAt, ServerTime: deal.ServerTime}}
	raw, err = EncodePayload(protocol.MsgReconnected, original)
	require.NoError(t, err)
	var restored protocol.ReconnectedPayload
	require.NoError(t, DecodePayload(protocol.MsgReconnected, raw, &restored))
	require.Equal(t, deal.DealEndAt, restored.GameState.DealEndAt)
	require.Equal(t, deal.DealID, restored.GameState.DealID)
}
