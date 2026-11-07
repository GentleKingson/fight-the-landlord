import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { GameSocket } from '../src/transport/wsClient';
import { encodeMessage, decodeMessage } from '../src/protocol/codec';
import { loadReconnect, useAppStore } from '../src/stores/appStore';

class FakeSocket {
  static OPEN = 1;
  static latest: FakeSocket;
  readyState = 1;
  sent: Uint8Array[] = [];
  onopen?: () => void;
  onmessage?: (event: { data: Uint8Array }) => void;
  onclose?: (event: { code: number }) => void;
  constructor() { FakeSocket.latest = this; }
  send(data: Uint8Array) { this.sent.push(data); }
  close(code = 1000) { this.onclose?.({ code }); }
  receive(type: string, payload: Record<string, unknown>) { this.onmessage?.({ data: encodeMessage(type, payload) }); }
}

beforeEach(() => { sessionStorage.clear(); vi.stubGlobal('WebSocket', FakeSocket); });

test('reload and repeated reconnect preserve the original identity and token', () => {
  sessionStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'old', token: 'old-token' }));
  const socket = new GameSocket('ws://localhost/ws');
  for (let i = 0; i < 2; i++) {
    socket.connect();
    const transport = FakeSocket.latest;
    transport.onopen?.();
    expect(decodeMessage(transport.sent[0]).payload).toMatchObject({ player_id: 'old', token: 'old-token' });
    transport.receive('connected', { player_id: 'temporary', player_name: 'Temp', reconnect_token: 'temporary-token' });
    expect(loadReconnect()).toEqual({ id: 'old', token: 'old-token' });
    transport.receive('reconnected', { player_id: 'old', player_name: 'Player' });
    expect(useAppStore.getState().playerId).toBe('old');
    socket.close();
    expect(loadReconnect()).toEqual({ id: 'old', token: 'old-token' });
  }
});

test('expired reconnect falls back to the new server identity', () => {
  sessionStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'old', token: 'expired' }));
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  FakeSocket.latest.onopen?.();
  FakeSocket.latest.receive('connected', { player_id: 'new', player_name: 'New', reconnect_token: 'new-token' });
  FakeSocket.latest.receive('error', { code: 1, message: 'expired' });
  expect(loadReconnect()).toEqual({ id: 'new', token: 'new-token' });
  expect(useAppStore.getState().phase).toBe('lobby');
  socket.close();
});


afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

test('a replaced tab stops reconnecting while network failures still retry', () => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  const first = FakeSocket.latest;
  first.onopen?.();
  first.close(1006);
  vi.advanceTimersByTime(2500);
  const second = FakeSocket.latest;
  expect(second).not.toBe(first);
  second.onopen?.();
  second.close(4001);
  vi.advanceTimersByTime(15000);
  expect(FakeSocket.latest).toBe(second);
  expect(useAppStore.getState().connected).toBe(false);
  expect(useAppStore.getState().error).toContain('本页面已停止重连');
  socket.close();
});
