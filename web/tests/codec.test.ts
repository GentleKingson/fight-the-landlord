import { describe, expect, it } from 'vitest';
import {
  ProtocolDecodeError,
  ProtocolEncodeError,
  decodeMessage,
  encodeMessage
} from '../src/protocol/codec';
import {
  MessageType,
  MsgType,
  REQUIRED_FIELDS_BY_NAME,
  type MessageName
} from '../src/protocol/generated';
import { protocol } from '../src/protocol/generated-runtime.js';

describe('protocol codec', () => {
  it('encodes the negotiation frame without dynamic code generation', () => {
    const originalFunction = globalThis.Function;
    globalThis.Function = (() => {
      throw new EvalError('dynamic code generation is blocked by CSP');
    }) as unknown as FunctionConstructor;

    try {
      expect(decodeMessage(encodeMessage(MsgType.Hello, {
        protocol_version: '1.0.0',
        client_version: '0.1.0',
        capabilities: ['command_correlation'],
        client_kind: 'web'
      }))).toMatchObject({ type: MsgType.Hello });
    } finally {
      globalThis.Function = originalFunction;
    }
  });

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

  it('accepts protocol rejection when no minimum client version is configured', () => {
    expect(decodeMessage(encodeMessage(MsgType.ProtocolRejected, {
      request_id: 'hello-1',
      reason: 'unsupported protocol version',
      supported_protocol_version: '1'
    }))).toEqual({
      type: MsgType.ProtocolRejected,
      payload: {
        request_id: 'hello-1',
        reason: 'unsupported protocol version',
        supported_protocol_version: '1',
        min_client_version: ''
      }
    });
  });

  it('decodes authoritative event metadata from the message envelope', () => {
    const Envelope = protocol.Message;
    const PlayTurn = protocol.PlayTurnPayload;
    const frame = Envelope.encode({
      type: MessageType.MSG_PLAY_TURN,
      payload: PlayTurn.encode({
        player_id: 'p1',
        timeout: 30,
        must_play: true,
        can_beat: true
      }).finish(),
      event: {
        stream_id: 'game:game-1',
        event_version: 42,
        game_id: 'game-1',
        turn_id: 7,
        server_time_ms: 1_700_000_000_000,
        turn_deadline_ms: 1_700_000_030_000
      }
    }).finish();

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

    const Envelope = protocol.Message;
    const unknown = Envelope.encode({
      type: 999 as MessageType,
      payload: new Uint8Array()
    }).finish();
    expect(() => decodeMessage(unknown)).toThrowError(ProtocolDecodeError);
  });

  it('rejects malformed payload bytes and missing required fields', () => {
    const Envelope = protocol.Message;
    const malformed = Envelope.encode({
      type: MessageType.MSG_JOIN_ROOM,
      payload: Uint8Array.of(0xff)
    }).finish();
    expect(() => decodeMessage(malformed)).toThrow(ProtocolDecodeError);

    const JoinRoom = protocol.JoinRoomPayload;
    const missingField = Envelope.encode({
      type: MessageType.MSG_JOIN_ROOM,
      payload: JoinRoom.encode({}).finish()
    }).finish();
    expect(() => decodeMessage(missingField)).toThrow(/missing required field room_code/);
    expect(() => encodeMessage(MsgType.JoinRoom, {})).toThrow(/missing required field room_code/);
  });

  it('rejects int64 values that JavaScript cannot represent exactly', () => {
    expect(() => encodeMessage(MsgType.Ping, { timestamp: Number.MAX_SAFE_INTEGER + 1 })).toThrow(
      /safe integer range/
    );
  });

  it('rejects unsafe int64 values nested inside repeated payload messages', () => {
    expect(() => encodeMessage(MsgType.Reconnected, {
      player_id: 'p1',
      player_name: '青竹',
      game_state: {
        settlement: {
          scores: [{ score: Number.MAX_SAFE_INTEGER + 1 }]
        }
      }
    })).toThrow(/game_state\.settlement\.scores\[0\]\.score/);
  });

  it('rejects decoded nested int64 overflow as a protocol decode error', () => {
    const payload = protocol.ReconnectedPayload.encode({
      player_id: 'p1',
      player_name: '青竹',
      game_state: {
        phase: 'ended',
        settlement: {
          multiplier: 1,
          scores: [{
            player_id: 'p1',
            player_name: '青竹',
            is_landlord: true,
            score: Number.MAX_SAFE_INTEGER + 1
          }],
          player_hands: []
        }
      }
    }).finish();
    const frame = protocol.Message.encode({
      type: MessageType.MSG_RECONNECTED,
      payload
    }).finish();

    expect(() => decodeMessage(frame)).toThrowError(ProtocolDecodeError);
    expect(() => decodeMessage(frame)).toThrow(/game_state\.settlement\.scores\[0\]\.score/);
  });

  it('uses required-field metadata emitted by the protocol generator', () => {
    expect(REQUIRED_FIELDS_BY_NAME).toMatchObject({
      join_room: ['room_code'],
      reconnected: ['player_id', 'player_name'],
      command_ack: ['request_id', 'command_type']
    });
  });
});
