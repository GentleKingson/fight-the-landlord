package codec

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/palemoky/fight-the-landlord/internal/protocol/convert/msgtype"
	"github.com/palemoky/fight-the-landlord/internal/protocol/pb"
)

type crossCodecMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type crossCodecFixture struct {
	Type  string `json:"type"`
	Frame string `json:"frame"`
}

func TestGoEncodingMatchesWebGoldenInput(t *testing.T) {
	t.Parallel()

	manifest := loadCrossCodecManifest(t)
	fixtures := encodeGoFixtures(t, manifest)
	fixturePath := filepath.Join("..", "testdata", "go-to-web.json")
	want := marshalFixtures(t, fixtures)

	if os.Getenv("UPDATE_PROTOCOL_FIXTURES") == "1" {
		require.NoError(t, os.WriteFile(fixturePath, want, 0o644))
		return
	}

	got, err := os.ReadFile(fixturePath)
	require.NoError(t, err, "run UPDATE_PROTOCOL_FIXTURES=1 go test ./internal/protocol/codec")
	require.True(t, bytes.Equal(want, got), "Go protocol fixture is stale; regenerate it")
}

func TestWebFixturesDecodeInGo(t *testing.T) {
	t.Parallel()

	manifest := loadCrossCodecManifest(t)
	fixtureData, err := os.ReadFile(filepath.Join("..", "testdata", "web-to-go.json"))
	require.NoError(t, err)
	var fixtures []crossCodecFixture
	require.NoError(t, json.Unmarshal(fixtureData, &fixtures))
	require.Len(t, fixtures, len(manifest))

	for index, fixture := range fixtures {
		entry := manifest[index]
		require.Equal(t, entry.Type, fixture.Type)
		frame, decodeErr := hex.DecodeString(fixture.Frame)
		require.NoError(t, decodeErr)

		var envelope pb.Message
		require.NoError(t, proto.Unmarshal(frame, &envelope))
		require.Equal(t, msgtype.StringToProtoMessageType(entry.Type), envelope.Type)

		expected := payloadMessage(t, entry)
		if expected == nil {
			require.Empty(t, envelope.Payload)
			continue
		}
		actual := expected.ProtoReflect().Type().New().Interface()
		require.NoError(t, proto.Unmarshal(envelope.Payload, actual))
		require.True(t, proto.Equal(expected, actual), "payload mismatch for %s", entry.Type)
	}
}

func loadCrossCodecManifest(t *testing.T) []crossCodecMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "messages.json"))
	require.NoError(t, err)
	var manifest []crossCodecMessage
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.NotEmpty(t, manifest)
	return manifest
}

func encodeGoFixtures(t *testing.T, manifest []crossCodecMessage) []crossCodecFixture {
	t.Helper()
	fixtures := make([]crossCodecFixture, 0, len(manifest))
	for _, entry := range manifest {
		messageType := msgtype.StringToProtoMessageType(entry.Type)
		require.NotEqual(t, pb.MessageType_MSG_UNKNOWN, messageType, "manifest contains unknown type %q", entry.Type)

		var payload []byte
		payloadValue := payloadMessage(t, entry)
		if payloadValue != nil {
			var err error
			payload, err = proto.MarshalOptions{Deterministic: true}.Marshal(payloadValue)
			require.NoError(t, err)
		}
		frame, err := proto.MarshalOptions{Deterministic: true}.Marshal(&pb.Message{
			Type:    messageType,
			Payload: payload,
		})
		require.NoError(t, err)
		fixtures = append(fixtures, crossCodecFixture{Type: entry.Type, Frame: hex.EncodeToString(frame)})
	}
	return fixtures
}

func payloadMessage(t *testing.T, entry crossCodecMessage) proto.Message {
	t.Helper()
	if string(entry.Payload) == "null" || len(entry.Payload) == 0 {
		return nil
	}

	name := protoreflect.FullName("protocol." + snakeToPascal(entry.Type) + "Payload")
	messageType, err := protoregistry.GlobalTypes.FindMessageByName(name)
	require.NoError(t, err, "missing payload message %s", name)
	message := messageType.New().Interface()
	require.NoError(t, protojson.UnmarshalOptions{DiscardUnknown: false}.Unmarshal(entry.Payload, message), entry.Type)
	return message
}

func snakeToPascal(value string) string {
	var result strings.Builder
	upperNext := true
	for _, character := range value {
		if character == '_' {
			upperNext = true
			continue
		}
		if upperNext {
			character = unicode.ToUpper(character)
			upperNext = false
		}
		result.WriteRune(character)
	}
	return result.String()
}

func marshalFixtures(t *testing.T, fixtures []crossCodecFixture) []byte {
	t.Helper()
	data, err := json.MarshalIndent(fixtures, "", "  ")
	require.NoError(t, err)
	return append(data, '\n')
}
