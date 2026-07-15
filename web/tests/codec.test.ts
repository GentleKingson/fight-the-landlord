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

  it('decodes authoritative event metadata from the message envelope', () => {
    const Envelope = protocolRoot.lookupType('protocol.Message');
    const PlayTurn = protocolRoot.lookupType('protocol.PlayTurnPayload');
    const frame = Envelope.encode(Envelope.create({
      type: MessageType.MSG_PLAY_TURN,
      payload: PlayTurn.encode(PlayTurn.create({
        player_id: 'p1',
        timeout: 30,
        must_play: true,
        can_beat: true
      })).finish(),
      event: {
        stream_id: 'game:game-1',
        event_version: 42,
        game_id: 'game-1',
        turn_id: 7,
        server_time_ms: 1_700_000_000_000,
        turn_deadline_ms: 1_700_000_030_000
      }
    })).finish();

    expect(decodeMessage(frame)).toEqual({
      type: MsgType.PlayTurn,
      payload: { player_id: 'p1', timeout: 30, must_play: true, can_beat: true },
      event: {
        stream_id: 'game:game-1',
        event_version: 42,
        game_id: 'game-1',
        turn_id: 7,
        server_time_ms: 1_700_000_000_000,
        turn_deadline_ms: 1_700_000_030_000
      }
    });
  });

  it('round trips correlated command metadata in the message envelope', () => {
    expect(decodeMessage(encodeMessage(MsgType.Pass, undefined, {
      request_id: 'request-7',
      expected_game_id: 'game-1',
      expected_turn_id: 7
    }))).toMatchObject({
      type: MsgType.Pass,
      payload: {},
      command: {
        request_id: 'request-7',
        expected_game_id: 'game-1',
        expected_turn_id: 7
      }
    });
    expect(() => encodeMessage(MsgType.Pass, undefined, { request_id: '' })).toThrow(/requires request_id/);
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
