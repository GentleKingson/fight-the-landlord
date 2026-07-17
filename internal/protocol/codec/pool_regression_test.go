package codec

import (
	"runtime/debug"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/pb"
)

func TestDecodeReturnsMessageToPoolForUnmappedProtoEnum(t *testing.T) {
	previousGCPercent := debug.SetGCPercent(-1)
	created := 0
	messagePool = sync.Pool{New: func() any {
		created++
		return &protocol.Message{}
	}}
	t.Cleanup(func() {
		debug.SetGCPercent(previousGCPercent)
		messagePool = sync.Pool{New: func() any { return &protocol.Message{} }}
	})

	encoded, err := proto.Marshal(&pb.Message{Type: pb.MessageType(9999)})
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 256
	for range attempts {
		message, decodeErr := Decode(encoded)
		if decodeErr == nil || message != nil {
			t.Fatalf("unknown enum decoded unexpectedly: message=%v error=%v", message, decodeErr)
		}
	}
	// Disable GC while measuring because sync.Pool is allowed to discard values
	// at a collection boundary. Without returning the error-path message, every
	// attempt necessarily allocates even in this deterministic window.
	if created >= attempts {
		t.Fatalf("unknown enum leaked every pooled message: created %d for %d attempts", created, attempts)
	}
}
