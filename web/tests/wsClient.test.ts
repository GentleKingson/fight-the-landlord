import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { decodeMessage, encodeMessage } from '../src/protocol/codec';
import { MsgType } from '../src/protocol/types';
import { loadReconnect, useAppStore } from '../src/stores/appStore';
import { GameSocket } from '../src/transport/wsClient';

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  binaryType: BinaryType = 'blob';
  sent: Uint8Array[] = [];
  onopen: ((this: WebSocket, event: Event) => unknown) | null = null;
  onmessage: ((this: WebSocket, event: MessageEvent) => unknown) | null = null;
  onerror: ((this: WebSocket, event: Event) => unknown) | null = null;
  onclose: ((this: WebSocket, event: CloseEvent) => unknown) | null = null;

  constructor(url: string | URL) {
    this.url = String(url);
    FakeWebSocket.instances.push(this);
  }

  open(): void {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.call(this as unknown as WebSocket, new Event('open'));
  }

  receive(frame: Uint8Array): void {
    this.onmessage?.call(
      this as unknown as WebSocket,
      new MessageEvent('message', { data: Uint8Array.from(frame).buffer })
    );
  }

  send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
    if (this.readyState !== FakeWebSocket.OPEN) throw new Error('socket is not open');
    if (ArrayBuffer.isView(data)) {
      this.sent.push(Uint8Array.from(new Uint8Array(data.buffer, data.byteOffset, data.byteLength)));
      return;
    }
    if (data instanceof ArrayBuffer) {
      this.sent.push(new Uint8Array(data));
      return;
    }
    throw new Error(`unsupported fake frame: ${typeof data}`);
  }

  close(): void {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.call(this as unknown as WebSocket, new CloseEvent('close'));
  }
}

describe('GameSocket reconnect identity state machine', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket);
    FakeWebSocket.instances = [];
    localStorage.clear();
    useAppStore.setState({
      connected: false,
      connectionStatus: 'idle',
      playerId: '',
      playerName: '',
      reconnectToken: '',
      reconnectCandidate: null,
      provisionalIdentity: null,
      reconnectNotice: '',
      error: '',
      phase: 'connecting',
      roomCode: ''
    });
  });

  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('accepts and persists a fresh connection only after Connected', async () => {
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    expect(useAppStore.getState().connectionStatus).toBe('connecting');

    socket.open();
    expect(loadReconnect()).toBeNull();
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'fresh-player',
      player_name: '新玩家',
      reconnect_token: 'fresh-token'
    }));
    await Promise.resolve();

    expect(useAppStore.getState().connectionStatus).toBe('connected');
    expect(loadReconnect()).toEqual({ id: 'fresh-player', token: 'fresh-token' });
    expect(socket.sent.map((frame) => decodeMessage(frame).type)).toEqual([
      MsgType.GetOnlineCount,
      MsgType.GetMaintenanceStatus
    ]);
    gameSocket.shutdown();
  });

  it('keeps provisional credentials out of storage until reconnect succeeds', async () => {
    localStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'old-player', token: 'old-token' }));
    useAppStore.setState({ playerId: 'old-player', reconnectToken: 'old-token' });
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    socket.open();
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'temp-player',
      player_name: '临时玩家',
      reconnect_token: 'temp-token'
    }));

    expect(useAppStore.getState().connectionStatus).toBe('reconnecting');
    expect(useAppStore.getState().playerId).toBe('old-player');
    expect(loadReconnect()).toEqual({ id: 'old-player', token: 'old-token' });
    expect(decodeMessage(socket.sent[0])).toEqual({
      type: MsgType.Reconnect,
      payload: { token: 'old-token', player_id: 'old-player' }
    });

    socket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'old-player',
      player_name: '青竹',
      room_code: '',
      reconnect_token: 'rotated-token'
    }));
    await Promise.resolve();
    expect(useAppStore.getState().connectionStatus).toBe('connected');
    expect(loadReconnect()).toEqual({ id: 'old-player', token: 'rotated-token' });
    gameSocket.shutdown();
  });

  it('can reconnect the same identity twice with successively rotated tokens', async () => {
    localStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'player', token: 'token-1' }));
    const gameSocket = new GameSocket('ws://example.test/ws');

    gameSocket.connect();
    let socket = latestSocket();
    socket.open();
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'temp-1', player_name: '临时一', reconnect_token: 'temp-token-1'
    }));
    socket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'player', player_name: '青竹', room_code: '', reconnect_token: 'token-2'
    }));
    await Promise.resolve();
    gameSocket.shutdown();

    gameSocket.connect();
    socket = latestSocket();
    socket.open();
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'temp-2', player_name: '临时二', reconnect_token: 'temp-token-2'
    }));
    expect(decodeMessage(socket.sent[0]).payload).toEqual({ token: 'token-2', player_id: 'player' });
    socket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'player', player_name: '青竹', room_code: '', reconnect_token: 'token-3'
    }));
    await Promise.resolve();

    expect(loadReconnect()).toEqual({ id: 'player', token: 'token-3' });
    gameSocket.shutdown();
  });

  it('StrictMode-style shutdown and reconnect preserves identity', () => {
    localStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'player', token: 'token' }));
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    gameSocket.shutdown();
    gameSocket.connect();

    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(loadReconnect()).toEqual({ id: 'player', token: 'token' });
    expect(useAppStore.getState().connectionStatus).toBe('connecting');
    gameSocket.shutdown();
  });

  it('returns an explicit failure instead of silently dropping a command', () => {
    const gameSocket = new GameSocket('ws://example.test/ws');
    expect(gameSocket.send(MsgType.Ready)).toEqual({ ok: false, reason: 'not-connected' });
  });
});

function latestSocket(): FakeWebSocket {
  const socket = FakeWebSocket.instances.at(-1);
  if (!socket) throw new Error('expected a WebSocket instance');
  return socket;
}
