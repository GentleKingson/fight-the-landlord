package codec

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert/msgtype"
	payloadconv "github.com/palemoky/fight-the-landlord/internal/protocol/convert/payload"
	"github.com/palemoky/fight-the-landlord/internal/protocol/pb"
)

// NewMessage 创建一个新消息
// 注意: 使用完毕后应调用 PutMessage 归还对象到池
func NewMessage(msgType protocol.MessageType, payload any) (*protocol.Message, error) {
	msg := GetMessage()
	msg.Type = msgType

	if payload != nil {
		var err error
		// 使用 Protobuf 编码 payload
		msg.Payload, err = payloadconv.EncodePayload(msgType, payload)
		if err != nil {
			PutMessage(msg) // 失败时归还
			return nil, err
		}
	}
	return msg, nil
}

// MustNewMessage 创建消息，失败时 panic
func MustNewMessage(msgType protocol.MessageType, payload any) *protocol.Message {
	msg, err := NewMessage(msgType, payload)
	if err != nil {
		panic(err)
	}
	return msg
}

// Encode 将消息编码为 Protobuf 字节
func Encode(m *protocol.Message) ([]byte, error) {
	pbMsg := GetPBMessage()
	defer PutPBMessage(pbMsg)

	pbMsg.Type = msgtype.StringToProtoMessageType(string(m.Type))
	if pbMsg.Type == pb.MessageType_MSG_UNKNOWN {
		return nil, fmt.Errorf("unknown message type %q", m.Type)
	}
	pbMsg.Payload = m.Payload // Protobuf payload
	if m.Event != nil {
		pbMsg.Event = &pb.EventMeta{
			StreamId:       m.Event.StreamID,
			EventVersion:   m.Event.EventVersion,
			GameId:         m.Event.GameID,
			TurnId:         m.Event.TurnID,
			ServerTimeMs:   m.Event.ServerTimeMS,
			TurnDeadlineMs: m.Event.TurnDeadlineMS,
		}
	}
	if m.Command != nil {
		pbMsg.Command = &pb.CommandMeta{
			RequestId:      m.Command.RequestID,
			ExpectedGameId: m.Command.ExpectedGameID,
			ExpectedTurnId: m.Command.ExpectedTurnID,
		}
	}

	return proto.Marshal(pbMsg)
}

// Decode 从 Protobuf 字节解码消息
// 注意: 使用完毕后应调用 PutMessage 归还对象到池
func Decode(data []byte) (*protocol.Message, error) {
	pbMsg := GetPBMessage()
	defer PutPBMessage(pbMsg)

	if err := proto.Unmarshal(data, pbMsg); err != nil {
		return nil, err
	}
	if pbMsg.Type == pb.MessageType_MSG_UNKNOWN {
		return nil, fmt.Errorf("unknown protobuf message type %d", pbMsg.Type)
	}

	msg := GetMessage()
	typeName := msgtype.ProtoMessageTypeToString(pbMsg.Type)
	if typeName == "unknown" {
		PutMessage(msg)
		return nil, fmt.Errorf("unknown protobuf message type %d", pbMsg.Type)
	}
	msg.Type = protocol.MessageType(typeName)
	msg.Payload = append([]byte(nil), pbMsg.Payload...) // 复制 payload 避免引用
	if pbMsg.Event != nil {
		msg.Event = &protocol.EventMeta{
			StreamID:       pbMsg.Event.StreamId,
			EventVersion:   pbMsg.Event.EventVersion,
			GameID:         pbMsg.Event.GameId,
			TurnID:         pbMsg.Event.TurnId,
			ServerTimeMS:   pbMsg.Event.ServerTimeMs,
			TurnDeadlineMS: pbMsg.Event.TurnDeadlineMs,
		}
	}
	if pbMsg.Command != nil {
		msg.Command = &protocol.CommandMeta{
			RequestID:      pbMsg.Command.RequestId,
			ExpectedGameID: pbMsg.Command.ExpectedGameId,
			ExpectedTurnID: pbMsg.Command.ExpectedTurnId,
		}
	}

	return msg, nil
}

// ParsePayload 解析消息的 Payload 到指定类型
func ParsePayload[T any](msg *protocol.Message) (*T, error) {
	var payload T
	if err := payloadconv.DecodePayload(msg.Type, msg.Payload, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// ParseChatPayload also accepts the legacy JSON Chat payload embedded in a
// protobuf Message envelope. The bool reports whether that fallback was used.
func ParseChatPayload(msg *protocol.Message) (*protocol.ChatPayload, bool, error) {
	var payload protocol.ChatPayload
	legacy, err := payloadconv.DecodeChatPayload(msg.Payload, &payload)
	return &payload, legacy, err
}

// CloneMessage returns an immutable copy suitable for replay caches.
func CloneMessage(msg *protocol.Message) *protocol.Message {
	if msg == nil {
		return nil
	}
	clone := &protocol.Message{Type: msg.Type, Payload: append([]byte(nil), msg.Payload...)}
	if msg.Event != nil {
		event := *msg.Event
		clone.Event = &event
	}
	if msg.Command != nil {
		command := *msg.Command
		clone.Command = &command
	}
	return clone
}

// NewErrorMessage 创建错误消息
func NewErrorMessage(code int) *protocol.Message {
	msg, _ := NewMessage(protocol.MsgError, protocol.ErrorPayload{
		Code:    code,
		Message: protocol.ErrorMessages[code],
	})
	return msg
}

// NewErrorMessageWithText 创建带自定义文本的错误消息
func NewErrorMessageWithText(code int, text string) *protocol.Message {
	msg, _ := NewMessage(protocol.MsgError, protocol.ErrorPayload{
		Code:    code,
		Message: text,
	})
	return msg
}

// NewCommandErrorMessage 创建可关联到具体命令的错误消息。
func NewCommandErrorMessage(code int, command protocol.MessageType) *protocol.Message {
	msg, _ := NewMessage(protocol.MsgError, protocol.ErrorPayload{
		Code:        code,
		Message:     protocol.ErrorMessages[code],
		CommandType: command,
	})
	return msg
}

// NewCommandErrorMessageWithText 创建带自定义文本且可关联到具体命令的错误消息。
func NewCommandErrorMessageWithText(code int, text string, command protocol.MessageType) *protocol.Message {
	msg, _ := NewMessage(protocol.MsgError, protocol.ErrorPayload{
		Code:        code,
		Message:     text,
		CommandType: command,
	})
	return msg
}

// CorrelateError copies an Error and attaches the command request identity to
// both the envelope and payload without mutating a shared/broadcast message.
func CorrelateError(msg *protocol.Message, requestID string, command protocol.MessageType) *protocol.Message {
	if msg == nil || msg.Type != protocol.MsgError {
		return CloneMessage(msg)
	}
	var payload protocol.ErrorPayload
	if err := payloadconv.DecodePayload(msg.Type, msg.Payload, &payload); err != nil {
		return CloneMessage(msg)
	}
	if payload.CommandType == "" {
		payload.CommandType = command
	}
	payload.RequestID = requestID
	correlated := MustNewMessage(protocol.MsgError, payload)
	correlated.Event = msg.Event
	correlated.Command = &protocol.CommandMeta{RequestID: requestID}
	return correlated
}

func NewCommandAckMessage(requestID string, command protocol.MessageType) *protocol.Message {
	msg := MustNewMessage(protocol.MsgCommandAck, protocol.CommandAckPayload{
		RequestID: requestID, CommandType: command,
	})
	msg.Command = &protocol.CommandMeta{RequestID: requestID}
	return msg
}

func NewCorrelatedCommandErrorMessage(code int, text, requestID string, command protocol.MessageType) *protocol.Message {
	if text == "" {
		text = protocol.ErrorMessages[code]
	}
	msg := MustNewMessage(protocol.MsgError, protocol.ErrorPayload{
		Code: code, Message: text, CommandType: command, RequestID: requestID,
	})
	msg.Command = &protocol.CommandMeta{RequestID: requestID}
	return msg
}
