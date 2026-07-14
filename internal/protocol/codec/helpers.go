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
