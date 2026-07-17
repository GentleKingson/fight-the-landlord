package payload

import (
	"slices"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

func FuzzPlayCardsPayloadRoundTrip(f *testing.F) {
	seed, err := EncodePayload(protocol.MsgPlayCards, protocol.PlayCardsPayload{
		Cards: []protocol.CardInfo{{Suit: 1, Rank: 3}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{})
	f.Add([]byte{0x0a, 0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		var decoded protocol.PlayCardsPayload
		if err := DecodePayload(protocol.MsgPlayCards, data, &decoded); err != nil {
			return
		}
		encoded, err := EncodePayload(protocol.MsgPlayCards, decoded)
		if err != nil {
			t.Fatalf("successfully decoded payload could not be encoded: %v", err)
		}
		var roundTrip protocol.PlayCardsPayload
		if err := DecodePayload(protocol.MsgPlayCards, encoded, &roundTrip); err != nil {
			t.Fatalf("round-trip decode failed: %v", err)
		}
		if !slices.Equal(decoded.Cards, roundTrip.Cards) {
			t.Fatalf("payload changed across round trip: decoded=%#v roundTrip=%#v", decoded, roundTrip)
		}
	})
}
