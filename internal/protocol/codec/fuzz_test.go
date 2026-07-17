package codec

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

func FuzzMessageCodecRoundTrip(f *testing.F) {
	seed := MustNewMessage(protocol.MsgPing, protocol.PingPayload{Timestamp: 42})
	seed.Command = &protocol.CommandMeta{RequestID: "fuzz-seed"}
	encoded, err := Encode(seed)
	if err != nil {
		f.Fatal(err)
	}
	PutMessage(seed)
	f.Add(encoded)
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		message, err := Decode(data)
		if err != nil {
			return
		}
		defer PutMessage(message)

		roundTripBytes, err := Encode(message)
		if err != nil {
			t.Fatalf("successfully decoded message could not be encoded: %v", err)
		}
		roundTrip, err := Decode(roundTripBytes)
		if err != nil {
			t.Fatalf("round-trip decode failed: %v", err)
		}
		defer PutMessage(roundTrip)

		if message.Type != roundTrip.Type || !bytes.Equal(message.Payload, roundTrip.Payload) {
			t.Fatalf("message changed across round trip")
		}
		if !reflect.DeepEqual(message.Event, roundTrip.Event) || !reflect.DeepEqual(message.Command, roundTrip.Command) {
			t.Fatalf("message metadata changed across round trip")
		}
	})
}
