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
  closeCalls: Array<{ code?: number; reason?: string }> = [];
  deferClose = false;
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

  close(code?: number, reason?: string): void {
    this.closeCalls.push({ code, reason });
    if (this.readyState === FakeWebSocket.CLOSED) return;
    if (this.deferClose) {
      this.readyState = FakeWebSocket.CLOSING;
      return;
    }
    this.finishClose();
  }

  finishClose(): void {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.call(this as unknown as WebSocket, new CloseEvent('close'));
  }
}

describe('GameSocket reconnect identity state machine', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllTimers();
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket);
    FakeWebSocket.instances = [];
    localStorage.clear();
    vi.advanceTimersByTime(0);
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
    vi.restoreAllMocks();
  });

  it('sends Hello first and gates heartbeats and commands until negotiation succeeds', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', { heartbeatIntervalMs: 100 });
    gameSocket.connect();
    const socket = latestSocket();
    socket.open();

    expect(socket.sent).toHaveLength(1);
    expect(decodeMessage(socket.sent[0])).toMatchObject({
      type: MsgType.Hello,
      payload: {
        protocol_version: '1',
        client_version: 'dev',
        capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat'],
        client_kind: 'web'
      },
      command: { request_id: expect.any(String) }
    });
    expect(vi.getTimerCount()).toBe(0);
    expect(gameSocket.send(MsgType.Ready, undefined, { request_id: 'too-early' })).toEqual({
      ok: false,
      reason: 'not-connected'
    });

    const helloRequestId = decodeMessage(socket.sent[0]).command?.request_id;
    if (!helloRequestId) throw new Error('expected hello request id');
    socket.receive(encodeMessage(MsgType.Negotiated, {
      protocol_version: '1',
      server_version: 'test',
      capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat'],
      client_kind: 'web'
    }, { request_id: helloRequestId }));
    expect(vi.getTimerCount()).toBe(1);
    expect(gameSocket.send(MsgType.Ready, undefined, { request_id: 'after-negotiation' })).toEqual({ ok: true });
    gameSocket.shutdown();
  });

  it('surfaces a protocol rejection and does not reconnect indefinitely', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', {
      reconnectBaseDelayMs: 25,
      reconnectJitterRatio: 0
    });
    gameSocket.connect();
    const socket = latestSocket();
    socket.open();
    const requestId = decodeMessage(socket.sent[0]).command?.request_id;
    if (!requestId) throw new Error('expected hello request id');

    socket.receive(encodeMessage(MsgType.ProtocolRejected, {
      request_id: requestId,
      reason: '协议版本不兼容',
      supported_protocol_version: '1',
      min_client_version: ''
    }, { request_id: requestId }));

    expect(useAppStore.getState().error).toBe('协议版本不兼容');
    expect(socket.closeCalls).toContainEqual({ code: 4002, reason: 'protocol rejected' });
    vi.advanceTimersByTime(1_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
    gameSocket.shutdown();
  });

  it.each([
    {
      name: 'mismatched request ID',
      requestId: 'different-hello',
      overrides: {},
      error: 'request_id 不匹配'
    },
    {
      name: 'incompatible protocol version',
      overrides: { protocol_version: '2' },
      error: '不兼容的协议版本'
    },
    {
      name: 'wrong client kind',
      overrides: { client_kind: 'tui' },
      error: '错误的客户端类型'
    },
    {
      name: 'missing required capability',
      overrides: { capabilities: ['command_correlation', 'idempotency', 'game_context'] },
      error: 'protobuf_chat'
    }
  ])('rejects Negotiated with $name without reconnecting', ({ requestId, overrides, error }) => {
    const gameSocket = new GameSocket('ws://example.test/ws', {
      reconnectBaseDelayMs: 25,
      reconnectJitterRatio: 0
    });
    gameSocket.connect();
    const socket = latestSocket();
    socket.open();
    const helloRequestId = decodeMessage(socket.sent[0]).command?.request_id;
    if (!helloRequestId) throw new Error('expected hello request id');

    socket.receive(encodeMessage(MsgType.Negotiated, {
      protocol_version: '1',
      server_version: 'test',
      capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat'],
      client_kind: 'web',
      ...overrides
    }, { request_id: requestId ?? helloRequestId }));

    expect(useAppStore.getState().error).toContain(error);
    expect(socket.closeCalls).toContainEqual({ code: 4002, reason: 'invalid negotiation' });
    expect(gameSocket.send(MsgType.Ready, undefined, { request_id: 'must-stay-gated' })).toEqual({
      ok: false,
      reason: 'not-connected'
    });
    vi.advanceTimersByTime(1_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
    gameSocket.shutdown();
  });

  it('accepts and persists a fresh connection only after Connected', async () => {
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    expect(useAppStore.getState().connectionStatus).toBe('connecting');

    openAndNegotiate(socket);
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
    storeReconnect('old-player', 'old-token');
    useAppStore.setState({ playerId: 'old-player', reconnectToken: 'old-token' });
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'temp-player',
      player_name: '临时玩家',
      reconnect_token: 'temp-token'
    }));

    expect(useAppStore.getState().connectionStatus).toBe('reconnecting');
    expect(useAppStore.getState().playerId).toBe('old-player');
    expect(loadReconnect()).toEqual({ id: 'old-player', token: 'old-token' });
    expect(decodeMessage(socket.sent[0])).toMatchObject({
      type: MsgType.Reconnect,
      payload: { token: 'old-token', player_id: 'old-player' },
      command: { request_id: expect.any(String) }
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
    storeReconnect('player', 'token-1');
    const gameSocket = new GameSocket('ws://example.test/ws');

    gameSocket.connect();
    let socket = latestSocket();
    openAndNegotiate(socket);
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
    openAndNegotiate(socket);
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
    storeReconnect('player', 'token');
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    gameSocket.shutdown();
    gameSocket.connect();

    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(loadReconnect()).toEqual({ id: 'player', token: 'token' });
    expect(useAppStore.getState().connectionStatus).toBe('connecting');
    gameSocket.shutdown();
  });

  it('revokes the exact persisted credential before clearing a logout', async () => {
    storeReconnect('logout-player', 'logout-token');
    const fetchMock = vi.fn().mockResolvedValue({ ok: true });
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws');

    await expect(gameSocket.logout()).resolves.toBe(true);

    expect(fetchMock).toHaveBeenCalledWith('/session/revoke', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      keepalive: true,
      body: JSON.stringify({ player_id: 'logout-player', token: 'logout-token' })
    }));
    expect(loadReconnect()).toBeNull();
    expect(useAppStore.getState().connectionStatus).toBe('idle');
  });

  it('quiesces reconnect handlers before revoking so a late rotation cannot orphan a live token', async () => {
    storeReconnect('logout-player', 'logout-token');
    let resolveFetch: ((value: { ok: boolean }) => void) | undefined;
    const fetchMock = vi.fn(() => new Promise<{ ok: boolean }>((resolve) => {
      resolveFetch = resolve;
    }));
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'temporary', player_name: '临时玩家', reconnect_token: 'temporary-token'
    }));

    const logoutResult = gameSocket.logout();
    expect(loadReconnect()).toBeNull();
    socket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'logout-player', player_name: '玩家', room_code: '', reconnect_token: 'late-rotated-token'
    }));
    expect(loadReconnect()).toBeNull();

    resolveFetch?.({ ok: true });
    await expect(logoutResult).resolves.toBe(true);
    expect(fetchMock).toHaveBeenCalledWith('/session/revoke', expect.objectContaining({
      body: JSON.stringify({ player_id: 'logout-player', token: 'logout-token' })
    }));
    expect(useAppStore.getState().reconnectToken).toBe('');
  });

  it('returns an explicit failure instead of silently dropping a command', () => {
    const gameSocket = new GameSocket('ws://example.test/ws');
    expect(gameSocket.send(MsgType.Ready)).toEqual({ ok: false, reason: 'not-connected' });
  });

  it('reports encoding failures and tolerates an isolated malformed incoming frame', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', { heartbeatIntervalMs: 60_000 });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);

    const result = gameSocket.send(MsgType.Reconnect);
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.reason).toBe('encode-failed');
    expect(useAppStore.getState().error).toContain('消息编码失败');
    expect(socket.sent).toHaveLength(0);

    socket.receive(Uint8Array.of(0xff));
    expect(useAppStore.getState().error).toContain('收到无效的服务器消息');
    expect(socket.readyState).toBe(FakeWebSocket.OPEN);
    gameSocket.shutdown();
  });

  it('closes and renegotiates after the consecutive malformed-frame threshold', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 60_000,
      malformedFrameThreshold: 3,
      reconnectBaseDelayMs: 25,
      reconnectJitterRatio: 0
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);

    socket.receive(Uint8Array.of(0xff));
    socket.receive(Uint8Array.of(0xff));
    expect(socket.readyState).toBe(FakeWebSocket.OPEN);
    socket.receive(Uint8Array.of(0xff));

    expect(socket.closeCalls).toContainEqual({ code: 4003, reason: 'malformed server frames' });
    expect(useAppStore.getState().error).toContain('连续收到 3 条无效');
    vi.advanceTimersByTime(24);
    expect(FakeWebSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(2);
    openAndNegotiate(latestSocket());
    gameSocket.shutdown();
  });

  it('resets the malformed-frame counter after any valid frame', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 60_000,
      malformedFrameThreshold: 3
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);

    socket.receive(Uint8Array.of(0xff));
    socket.receive(Uint8Array.of(0xff));
    socket.receive(encodeMessage(MsgType.OnlineCount, { count: 7 }));
    socket.receive(Uint8Array.of(0xff));
    socket.receive(Uint8Array.of(0xff));
    expect(socket.readyState).toBe(FakeWebSocket.OPEN);
    socket.receive(Uint8Array.of(0xff));
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);
    gameSocket.shutdown();
  });

  it('renegotiates and reuses the rotated credential after an unknown snapshot phase', async () => {
    storeReconnect('player', 'token-1');
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 60_000,
      reconnectBaseDelayMs: 25,
      reconnectJitterRatio: 0
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);

    socket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'player',
      player_name: '青竹',
      room_code: '123456',
      reconnect_token: 'token-2',
      game_state: { phase: 'paused' }
    }));
    vi.runAllTicks();
    vi.advanceTimersByTime(0);

    expect(socket.closeCalls).toContainEqual({ code: 4003, reason: 'authoritative resync' });
    expect(useAppStore.getState().error).toContain('paused');
    expect(loadReconnect()).toEqual({ id: 'player', token: 'token-2' });
    expect(vi.getTimerCount()).toBe(1);

    gameSocket.shutdown();
    expect(vi.getTimerCount()).toBe(0);

    const retryGameSocket = new GameSocket('ws://example.test/ws', { heartbeatIntervalMs: 60_000 });
    retryGameSocket.connect();
    const retry = latestSocket();
    openAndNegotiate(retry);
    retry.receive(encodeMessage(MsgType.Connected, {
      player_id: 'temporary',
      player_name: '临时玩家',
      reconnect_token: 'temporary-token'
    }));
    expect(decodeMessage(retry.sent[0])).toMatchObject({
      type: MsgType.Reconnect,
      payload: { player_id: 'player', token: 'token-2' }
    });
    await Promise.resolve();
    vi.runAllTicks();
    expect(vi.getTimerCount()).toBe(1);
    retryGameSocket.shutdown();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('uses capped exponential reconnect delays without creating duplicate sockets or timers', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', {
      reconnectBaseDelayMs: 100,
      reconnectMaxDelayMs: 400,
      reconnectJitterRatio: 0,
      heartbeatIntervalMs: 60_000
    });
    gameSocket.connect();
    gameSocket.connect();
    expect(FakeWebSocket.instances).toHaveLength(1);

    latestSocket().close();
    expect(vi.getTimerCount()).toBe(1);
    latestSocket().close();
    expect(vi.getTimerCount()).toBe(1);
    vi.advanceTimersByTime(99);
    expect(FakeWebSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(2);

    latestSocket().close();
    vi.advanceTimersByTime(199);
    expect(FakeWebSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(3);

    latestSocket().close();
    vi.advanceTimersByTime(399);
    expect(FakeWebSocket.instances).toHaveLength(3);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(4);

    latestSocket().close();
    vi.advanceTimersByTime(400);
    expect(FakeWebSocket.instances).toHaveLength(5);
    gameSocket.shutdown();
  });

  it('applies deterministic reconnect jitter and resets backoff after Connected', async () => {
    const random = vi.fn(() => 0);
    const gameSocket = new GameSocket('ws://example.test/ws', {
      reconnectBaseDelayMs: 100,
      reconnectMaxDelayMs: 1000,
      reconnectJitterRatio: 0.2,
      heartbeatIntervalMs: 60_000,
      random
    });
    gameSocket.connect();
    latestSocket().close();
    vi.advanceTimersByTime(79);
    expect(FakeWebSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(2);

    const recovered = latestSocket();
    openAndNegotiate(recovered);
    recovered.receive(encodeMessage(MsgType.Connected, {
      player_id: 'fresh-player',
      player_name: '新玩家',
      reconnect_token: 'fresh-token'
    }));
    await Promise.resolve();
    recovered.close();

    vi.advanceTimersByTime(79);
    expect(FakeWebSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(3);
    expect(random).toHaveBeenCalledTimes(2);
    gameSocket.shutdown();
  });

  it('closes a half-open connection when the Pong watchdog expires', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 100,
      pongTimeoutMs: 50,
      reconnectBaseDelayMs: 25,
      reconnectJitterRatio: 0
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);

    vi.advanceTimersByTime(100);
    expect(socket.sent.map((frame) => decodeMessage(frame).type)).toEqual([MsgType.Ping]);
    vi.advanceTimersByTime(49);
    expect(socket.readyState).toBe(FakeWebSocket.OPEN);
    vi.advanceTimersByTime(1);

    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);
    expect(socket.closeCalls).toContainEqual({ code: 4000, reason: 'heartbeat timeout' });
    expect(useAppStore.getState().error).toBe('服务器心跳超时，正在重连');
    vi.advanceTimersByTime(25);
    expect(FakeWebSocket.instances).toHaveLength(2);
    gameSocket.shutdown();
  });

  it('accepts Pong and maintains only one heartbeat watchdog cycle', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 100,
      pongTimeoutMs: 150,
      reconnectBaseDelayMs: 25,
      reconnectJitterRatio: 0
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    expect(vi.getTimerCount()).toBe(1);

    vi.advanceTimersByTime(100);
    expect(vi.getTimerCount()).toBe(2);
    vi.advanceTimersByTime(100);
    expect(vi.getTimerCount()).toBe(2);
    expect(socket.sent.map((frame) => decodeMessage(frame).type)).toEqual([MsgType.Ping]);
    socket.receive(encodeMessage(MsgType.Pong, {
      client_timestamp: Date.now(),
      server_timestamp: Date.now()
    }));
    expect(vi.getTimerCount()).toBe(1);
    vi.advanceTimersByTime(100);
    expect(socket.sent.map((frame) => decodeMessage(frame).type)).toEqual([MsgType.Ping, MsgType.Ping]);
    expect(socket.readyState).toBe(FakeWebSocket.OPEN);
    gameSocket.shutdown();
  });

  it('pauses while offline and reconnects immediately when the browser comes online', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', {
      reconnectBaseDelayMs: 100,
      reconnectJitterRatio: 0,
      heartbeatIntervalMs: 60_000
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);

    window.dispatchEvent(new Event('offline'));
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);
    expect(useAppStore.getState().connectionStatus).toBe('offline');
    vi.advanceTimersByTime(10_000);
    expect(FakeWebSocket.instances).toHaveLength(1);

    window.dispatchEvent(new Event('online'));
    expect(FakeWebSocket.instances).toHaveLength(2);
    window.dispatchEvent(new Event('online'));
    gameSocket.connect();
    expect(FakeWebSocket.instances).toHaveLength(2);
    gameSocket.shutdown();
  });

  it('waits for an offline socket to finish closing before reconnecting online', () => {
    const gameSocket = new GameSocket('ws://example.test/ws', {
      reconnectBaseDelayMs: 100,
      reconnectJitterRatio: 0,
      heartbeatIntervalMs: 60_000
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.deferClose = true;

    window.dispatchEvent(new Event('offline'));
    expect(socket.readyState).toBe(FakeWebSocket.CLOSING);
    window.dispatchEvent(new Event('online'));
    expect(FakeWebSocket.instances).toHaveLength(1);

    socket.finishClose();
    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(vi.getTimerCount()).toBe(0);
    gameSocket.shutdown();
  });

  it('does not create a socket until an initially offline browser comes online', () => {
    let online = false;
    vi.spyOn(navigator, 'onLine', 'get').mockImplementation(() => online);
    const gameSocket = new GameSocket('ws://example.test/ws', {
      reconnectBaseDelayMs: 100,
      reconnectJitterRatio: 0
    });

    gameSocket.connect();
    expect(FakeWebSocket.instances).toHaveLength(0);
    expect(useAppStore.getState().connectionStatus).toBe('offline');
    online = true;
    window.dispatchEvent(new Event('online'));
    expect(FakeWebSocket.instances).toHaveLength(1);
    gameSocket.shutdown();
  });

  it('pauses hidden-page heartbeats and sends one immediately when visible again', () => {
    let hidden = false;
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 100,
      pongTimeoutMs: 500,
      reconnectBaseDelayMs: 25,
      reconnectJitterRatio: 0
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    expect(vi.getTimerCount()).toBe(1);

    hidden = true;
    document.dispatchEvent(new Event('visibilitychange'));
    expect(vi.getTimerCount()).toBe(0);
    vi.advanceTimersByTime(1000);
    expect(socket.sent).toHaveLength(0);

    hidden = false;
    document.dispatchEvent(new Event('visibilitychange'));
    expect(vi.getTimerCount()).toBe(2);
    expect(socket.sent.map((frame) => decodeMessage(frame).type)).toEqual([MsgType.Ping]);
    socket.receive(encodeMessage(MsgType.Pong, {
      client_timestamp: Date.now(),
      server_timestamp: Date.now()
    }));
    expect(vi.getTimerCount()).toBe(1);
    document.dispatchEvent(new Event('visibilitychange'));
    expect(vi.getTimerCount()).toBe(2);
    socket.receive(encodeMessage(MsgType.Pong, {
      client_timestamp: Date.now(),
      server_timestamp: Date.now()
    }));
    vi.advanceTimersByTime(100);
    expect(socket.sent.map((frame) => decodeMessage(frame).type)).toEqual([
      MsgType.Ping,
      MsgType.Ping,
      MsgType.Ping
    ]);
    gameSocket.shutdown();
  });
});

function latestSocket(): FakeWebSocket {
  const socket = FakeWebSocket.instances.at(-1);
  if (!socket) throw new Error('expected a WebSocket instance');
  return socket;
}

function openAndNegotiate(socket: FakeWebSocket): void {
  socket.open();
  const hello = decodeMessage(socket.sent.at(-1) ?? new Uint8Array());
  expect(hello).toMatchObject({
    type: MsgType.Hello,
    payload: {
      protocol_version: '1',
      capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat'],
      client_kind: 'web'
    },
    command: { request_id: expect.any(String) }
  });
  socket.receive(encodeMessage(MsgType.Negotiated, {
    protocol_version: '1',
    server_version: 'test',
    capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat'],
    client_kind: 'web'
  }, { request_id: hello.command?.request_id }));
  socket.sent = [];
}

function storeReconnect(id: string, token: string, expiresAt = Date.now() + 120_000): void {
  localStorage.setItem('ddz_next_reconnect', JSON.stringify({ id, token, expires_at: expiresAt }));
}
