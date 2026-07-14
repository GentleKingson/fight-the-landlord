import { describe, expect, it } from 'vitest';
import {
  ProtocolDecodeError,
  ProtocolEncodeError,
  decodeMessage,
  encodeMessage
} from '../src/protocol/codec';
import { MessageType, MsgType, protocolRoot, type MessageName } from '../src/protocol/generated';

describe('protocol codec', () => {
  it('round trips room join payloads', () => {
    const decoded = decodeMessage(encodeMessage(MsgType.JoinRoom, { room_code: '123456' }));
    expect(decoded).toEqual({ type: MsgType.JoinRoom, payload: { room_code: '123456' } });
  });

  it('preserves empty arrays and zero values', () => {
    expect(decodeMessage(encodeMessage(MsgType.PlayCards, { cards: [] }))).toEqual({
      type: MsgType.PlayCards,
      payload: { cards: [] }
    });
    expect(decodeMessage(encodeMessage(MsgType.Ping, { timestamp: 0 }))).toEqual({
      type: MsgType.Ping,
      payload: { timestamp: 0 }
    });
  });

  it('uses canonical maintenance names', () => {
    const decoded = decodeMessage(encodeMessage(MsgType.Maintenance, { maintenance: true }));
    expect(decoded).toEqual({ type: MsgType.Maintenance, payload: { maintenance: true } });
  });

  it('round trips Unicode chat as protobuf without a JSON fallback', () => {
    const payload = {
      sender_id: 'p1',
      sender_name: '青竹',
      content: '你好',
      scope: 'lobby',
      time: 1,
      is_system: false,
      message_id: '消息-1',
      room_code: '',
      game_id: '',
      server_time: 1
    };
    expect(decodeMessage(encodeMessage(MsgType.Chat, payload))).toEqual({ type: MsgType.Chat, payload });
  });

  it('rejects unknown outgoing and incoming message types', () => {
    expect(() => encodeMessage('not_real' as MessageName)).toThrow(ProtocolEncodeError);

    const Envelope = protocolRoot.lookupType('protocol.Message');
    const unknown = Envelope.encode(Envelope.create({ type: 999, payload: new Uint8Array() })).finish();
    expect(() => decodeMessage(unknown)).toThrowError(ProtocolDecodeError);
  });

  it('rejects malformed payload bytes and missing required fields', () => {
    const Envelope = protocolRoot.lookupType('protocol.Message');
    const malformed = Envelope.encode(Envelope.create({
      type: MessageType.MSG_JOIN_ROOM,
      payload: Uint8Array.of(0xff)
    })).finish();
    expect(() => decodeMessage(malformed)).toThrow(ProtocolDecodeError);

    const JoinRoom = protocolRoot.lookupType('protocol.JoinRoomPayload');
    const missingField = Envelope.encode(Envelope.create({
      type: MessageType.MSG_JOIN_ROOM,
      payload: JoinRoom.encode(JoinRoom.create({})).finish()
    })).finish();
    expect(() => decodeMessage(missingField)).toThrow(/missing required field room_code/);
    expect(() => encodeMessage(MsgType.JoinRoom, {})).toThrow(/missing required field room_code/);
  });

  it('rejects int64 values that JavaScript cannot represent exactly', () => {
    expect(() => encodeMessage(MsgType.Ping, { timestamp: Number.MAX_SAFE_INTEGER + 1 })).toThrow(
      /safe integer range/
    );
  });
});
