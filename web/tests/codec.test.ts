import { describe, expect, it } from 'vitest';
import { decodeMessage, encodeMessage } from '../src/protocol/codec';
import { MsgType } from '../src/protocol/types';

describe('protocol codec', () => {
  it.each([MsgType.GameStart, MsgType.Reconnected])('preserves explicit bot identity in %s messages', type => {
    const players = [{ id: 'human', name: '🤖不是机器人', is_bot: false }, { id: 'bot', name: '小王', is_bot: true }];
    const payload = type === MsgType.GameStart ? { players } : { game_state: { players } };
    const decoded = decodeMessage(encodeMessage(type, payload));
    const result = decoded.payload as { players: { is_bot: boolean }[]; game_state: { players: { is_bot: boolean }[] } };
    const actual = type === MsgType.GameStart ? result.players : result.game_state.players;
    expect(actual.map(player => player.is_bot)).toEqual([false, true]);
  });

  it('round trips room join payloads', () => {
    const decoded = decodeMessage(encodeMessage(MsgType.JoinRoom, { room_code: '123456' }));
    expect(decoded.type).toBe(MsgType.JoinRoom);
    expect(decoded.payload).toEqual({ room_code: '123456' });
  });

  it('round trips play cards payloads', () => {
    const decoded = decodeMessage(encodeMessage(MsgType.PlayCards, {
      turn_token: 'round-turn',
      cards: [
        { suit: 1, rank: 14, color: 1 },
        { suit: 3, rank: 14, color: 1 }
      ]
    }));
    expect(decoded.type).toBe(MsgType.PlayCards);
    expect(decoded.payload).toEqual({
      turn_token: 'round-turn',
      cards: [
        { suit: 1, rank: 14, color: 1 },
        { suit: 3, rank: 14, color: 1 }
      ]
    });
  });

  it('decodes maintenance aliases using Go protocol names', () => {
    const decoded = decodeMessage(encodeMessage(MsgType.MaintenancePush, { maintenance: true }));
    expect(decoded.type).toBe(MsgType.MaintenancePush);
    expect(decoded.payload).toEqual({ maintenance: true });
  });

  it('preserves turn tokens on pass requests', () => {
    const decoded = decodeMessage(encodeMessage(MsgType.Pass, { turn_token: 'saved-turn' }));
    expect(decoded.type).toBe(MsgType.Pass);
    expect(decoded.payload).toMatchObject({ turn_token: 'saved-turn' });
  });

  it('decodes server-broadcast chat envelopes without throwing when TUI players chat', () => {
    const decoded = decodeMessage(encodeMessage('chat', {
      sender_id: 'p1',
      sender_name: '青竹',
      content: '你好',
      scope: 'lobby',
      time: 1
    }));
    expect(decoded.type).toBe('chat');
    expect(decoded.payload).toEqual({
      sender_id: 'p1',
      sender_name: '青竹',
      content: '你好',
      scope: 'lobby',
      time: 1
    });
  });

  it('preserves proto3 default values and 64-bit numeric fields with static codecs', () => {
    const zeroOnline = decodeMessage(encodeMessage(MsgType.OnlineCount, { count: 0 }));
    expect(zeroOnline).toEqual({ type: MsgType.OnlineCount, payload: { count: 0 } });

    const ts = 1_794_481_363_471;
    const pong = decodeMessage(encodeMessage(MsgType.Pong, { client_timestamp: ts - 25, server_timestamp: ts }));
    expect(pong).toEqual({
      type: MsgType.Pong,
      payload: { client_timestamp: ts - 25, server_timestamp: ts }
    });
  });
});
