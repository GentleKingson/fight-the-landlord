import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { decodeMessage, encodeMessage } from '../src/protocol/codec';
import { MsgType } from '../src/protocol/types';
import { useAppStore } from '../src/stores/appStore';
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
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, status: 204 }));
    vi.advanceTimersByTime(0);
    useAppStore.setState({
      connected: false,
      connectionStatus: 'idle',
      playerId: '',
      playerName: '',
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
        capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat', 'http_only_session_ticket'],
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
      capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat', 'http_only_session_ticket'],
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
    },
    {
      name: 'missing Web session capability',
      overrides: { capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat'] },
      error: 'http_only_session_ticket'
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
      capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat', 'http_only_session_ticket'],
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

  it('commits a fresh browser session ticket without exposing a reconnect token', async () => {
    const fetchMock = vi.mocked(fetch);
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    expect(useAppStore.getState().connectionStatus).toBe('connecting');

    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'fresh-player',
      player_name: '新玩家',
      web_session_ticket: 'fresh-ticket',
      reconnect_available: false
    }));
    await flushPromises();

    expect(useAppStore.getState().connectionStatus).toBe('connected');
    expect(useAppStore.getState().playerId).toBe('fresh-player');
    expect(fetchMock).toHaveBeenCalledWith('/session/commit', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify({ ticket: 'fresh-ticket' })
    }));
    expect(fetchMock).toHaveBeenCalledWith('/session/refresh', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify({})
    }));
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(localStorage.getItem('ddz_next_reconnect')).toBeNull();
    expect(socket.sent.map((frame) => decodeMessage(frame).type)).toEqual([
      MsgType.GetOnlineCount,
      MsgType.GetMaintenanceStatus
    ]);
    gameSocket.shutdown();
  });

  it('waits for authenticated successor-cookie observation before declaring the socket connected', async () => {
    let resolveRefresh: ((value: { ok: boolean; status: number }) => void) | undefined;
    const fetchMock = vi.fn((input: string | URL | Request) => {
      if (String(input) === '/session/refresh') {
        return new Promise<{ ok: boolean; status: number }>((resolve) => {
          resolveRefresh = resolve;
        });
      }
      return Promise.resolve({ ok: true, status: 204 });
    });
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'observed-player',
      player_name: '玩家',
      web_session_ticket: 'observed-ticket',
      reconnect_available: false
    }));
    await flushPromises();

    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      '/session/commit',
      '/session/refresh'
    ]);
    expect(useAppStore.getState().connected).toBe(false);
    expect(useAppStore.getState().connectionStatus).not.toBe('connected');
    expect(socket.sent).toHaveLength(0);

    resolveRefresh?.({ ok: true, status: 204 });
    await flushPromises();
    expect(useAppStore.getState().connectionStatus).toBe('connected');
    expect(socket.sent.map((frame) => decodeMessage(frame).type)).toEqual([
      MsgType.GetOnlineCount,
      MsgType.GetMaintenanceStatus
    ]);
    gameSocket.shutdown();
  });

  it('renegotiates instead of publishing identity when successor-cookie observation fails', async () => {
    const fetchMock = vi.fn((input: string | URL | Request) => Promise.resolve(
      String(input) === '/session/refresh'
        ? { ok: false, status: 401 }
        : { ok: true, status: 204 }
    ));
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'unobserved-player',
      player_name: '玩家',
      web_session_ticket: 'unobserved-ticket',
      reconnect_available: false
    }));
    await flushPromises();

    expect(useAppStore.getState().connected).toBe(false);
    expect(useAppStore.getState().error).toContain('Web 会话确认失败：HTTP 401');
    expect(socket.closeCalls).toContainEqual({ code: 4003, reason: 'web session commit failed' });
    gameSocket.shutdown();
  });

  it('refreshes an active HttpOnly session beyond the seven-day cookie lifetime', async () => {
    const dayMs = 24 * 60 * 60 * 1000;
    const fetchMock = vi.mocked(fetch);
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 10 * dayMs,
      sessionRefreshIntervalMs: dayMs
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'long-lived-player',
      player_name: '常驻玩家',
      web_session_ticket: 'long-lived-ticket',
      reconnect_available: false
    }));
    await flushPromises();
    fetchMock.mockClear();

    for (let day = 0; day < 8; day += 1) {
      await vi.advanceTimersByTimeAsync(dayMs);
    }

    const refreshCalls = fetchMock.mock.calls.filter(([input]) => String(input) === '/session/refresh');
    expect(refreshCalls).toHaveLength(8);
    for (const [, init] of refreshCalls) {
      expect(init).toEqual(expect.objectContaining({
        method: 'POST',
        credentials: 'same-origin',
        cache: 'no-store',
        body: JSON.stringify({})
      }));
    }

    gameSocket.shutdown();
    await vi.advanceTimersByTimeAsync(dayMs);
    expect(fetchMock.mock.calls.filter(([input]) => String(input) === '/session/refresh')).toHaveLength(8);
  });

  it('refreshes an overdue session once when a suspended page becomes visible', async () => {
    const dayMs = 24 * 60 * 60 * 1000;
    const fetchMock = vi.mocked(fetch);
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 10 * dayMs,
      sessionRefreshIntervalMs: dayMs
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'resumed-player',
      player_name: '恢复玩家',
      web_session_ticket: 'resumed-ticket',
      reconnect_available: false
    }));
    await flushPromises();
    fetchMock.mockClear();

    vi.setSystemTime(Date.now() + 2 * dayMs);
    document.dispatchEvent(new Event('visibilitychange'));
    await flushPromises();
    document.dispatchEvent(new Event('visibilitychange'));
    await Promise.resolve();

    expect(fetchMock.mock.calls.filter(([input]) => String(input) === '/session/refresh')).toHaveLength(1);
    gameSocket.shutdown();
  });

  it('uses the cookie signal to send an empty reconnect payload and commits the rotated ticket', async () => {
    const fetchMock = vi.mocked(fetch);
    useAppStore.setState({ playerId: 'old-player', playerName: '旧玩家' });
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'temp-player',
      player_name: '临时玩家',
      web_session_ticket: 'temporary-ticket',
      reconnect_available: true
    }));

    expect(useAppStore.getState().connectionStatus).toBe('reconnecting');
    expect(useAppStore.getState().playerId).toBe('old-player');
    expect(decodeMessage(socket.sent[0])).toMatchObject({
      type: MsgType.Reconnect,
      payload: {},
      command: { request_id: expect.any(String) }
    });

    socket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'old-player',
      player_name: '青竹',
      room_code: '',
      web_session_ticket: 'rotated-ticket'
    }));
    await flushPromises();
    expect(useAppStore.getState().connectionStatus).toBe('connected');
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock).toHaveBeenCalledWith('/session/commit', expect.objectContaining({
      body: JSON.stringify({ ticket: 'rotated-ticket' })
    }));
    gameSocket.shutdown();
  });

  it('retries a losing concurrent handshake without committing its provisional ticket', async () => {
    const fetchMock = vi.mocked(fetch);
    const options = {
      heartbeatIntervalMs: 60_000,
      reconnectBaseDelayMs: 25,
      reconnectJitterRatio: 0
    };
    const winner = new GameSocket('ws://example.test/ws', options);
    const loser = new GameSocket('ws://example.test/ws', options);

    winner.connect();
    const winnerSocket = latestSocket();
    loser.connect();
    const loserSocket = latestSocket();
    openAndNegotiate(winnerSocket);
    openAndNegotiate(loserSocket);

    winnerSocket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'winner-temp',
      player_name: '临时一',
      web_session_ticket: 'winner-provisional',
      reconnect_available: true
    }));
    loserSocket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'loser-temp',
      player_name: '临时二',
      web_session_ticket: 'must-not-commit',
      reconnect_available: true
    }));
    const loserReconnectRequestId = decodeMessage(loserSocket.sent[0]).command?.request_id;
    if (!loserReconnectRequestId) throw new Error('expected losing reconnect request id');

    winnerSocket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'restored-player',
      player_name: '青竹',
      room_code: '',
      web_session_ticket: 'winner-rotated'
    }));
    await flushPromises();
    loserSocket.receive(encodeMessage(MsgType.Error, {
      code: 1003,
      message: '重连令牌无效',
      request_id: loserReconnectRequestId
    }, { request_id: loserReconnectRequestId }));

    expect(loserSocket.closeCalls).toContainEqual({
      code: 4003,
      reason: 'web session reconnect failed'
    });
    expect(useAppStore.getState().provisionalIdentity).toBeNull();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      '/session/commit',
      '/session/refresh'
    ]);
    expect(fetchMock).toHaveBeenCalledWith('/session/commit', expect.objectContaining({
      body: JSON.stringify({ ticket: 'winner-rotated' })
    }));

    vi.advanceTimersByTime(24);
    expect(FakeWebSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(3);

    winner.shutdown();
    loser.shutdown();
  });

  it('can restore the same cookie-backed identity twice', async () => {
    const fetchMock = vi.mocked(fetch);
    const gameSocket = new GameSocket('ws://example.test/ws');

    gameSocket.connect();
    let socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'temp-1', player_name: '临时一', web_session_ticket: 'temp-ticket-1', reconnect_available: true
    }));
    expect(decodeMessage(socket.sent[0]).payload).toEqual({ token: '', player_id: '' });
    socket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'player', player_name: '青竹', room_code: '', web_session_ticket: 'rotated-ticket-1'
    }));
    await flushPromises();
    gameSocket.shutdown();

    gameSocket.connect();
    socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'temp-2', player_name: '临时二', web_session_ticket: 'temp-ticket-2', reconnect_available: true
    }));
    expect(decodeMessage(socket.sent[0]).payload).toEqual({ token: '', player_id: '' });
    socket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'player', player_name: '青竹', room_code: '', web_session_ticket: 'rotated-ticket-2'
    }));
    await flushPromises();

    expect(fetchMock).toHaveBeenCalledTimes(4);
    gameSocket.shutdown();
  });

  it('StrictMode-style shutdown and reconnect creates one active socket and clears legacy storage', () => {
    localStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'player', token: 'legacy-token' }));
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    gameSocket.shutdown();
    gameSocket.connect();

    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(localStorage.getItem('ddz_next_reconnect')).toBeNull();
    expect(useAppStore.getState().connectionStatus).toBe('connecting');
    gameSocket.shutdown();
  });

  it('revokes the HttpOnly cookie with an empty bounded JSON request', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true });
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws');

    await expect(gameSocket.logout()).resolves.toBe(true);

    expect(fetchMock).toHaveBeenCalledWith('/session/revoke', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      keepalive: true,
      body: JSON.stringify({})
    }));
    expect(useAppStore.getState().connectionStatus).toBe('idle');
  });

  it('waits for an in-flight cookie commit before sending the final revoke', async () => {
    let resolveCommit: ((value: { ok: boolean; status: number }) => void) | undefined;
    const callOrder: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const url = String(input);
      callOrder.push(url);
      if (url === '/session/commit') {
        return new Promise<{ ok: boolean; status: number }>((resolve) => {
          resolveCommit = resolve;
        });
      }
      return Promise.resolve({ ok: true, status: 204 });
    });
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws');
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'commit-player',
      player_name: '玩家',
      web_session_ticket: 'pending-ticket',
      reconnect_available: false
    }));
    expect(callOrder).toEqual(['/session/commit']);

    const logoutResult = gameSocket.logout();
    await Promise.resolve();
    expect(callOrder).toEqual(['/session/commit']);

    resolveCommit?.({ ok: true, status: 204 });
    await expect(logoutResult).resolves.toBe(true);
    expect(callOrder).toEqual(['/session/commit', '/session/revoke']);
    expect(useAppStore.getState().connectionStatus).toBe('idle');
  });

  it('bounds a stalled cookie commit before sending the final revoke', async () => {
    const callOrder: string[] = [];
    let commitSignal: AbortSignal | null | undefined;
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const url = String(input);
      callOrder.push(url);
      if (url === '/session/commit') {
        commitSignal = init?.signal;
        return new Promise<{ ok: boolean; status: number }>(() => undefined);
      }
      return Promise.resolve({ ok: true, status: 204 });
    });
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws', { sessionRequestTimeoutMs: 100 });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'stalled-commit-player',
      player_name: '玩家',
      web_session_ticket: 'stalled-ticket',
      reconnect_available: false
    }));

    const logoutResult = gameSocket.logout();
    await Promise.resolve();
    expect(callOrder).toEqual(['/session/commit']);
    expect(commitSignal?.aborted).toBe(false);

    await vi.advanceTimersByTimeAsync(99);
    expect(callOrder).toEqual(['/session/commit']);
    await vi.advanceTimersByTimeAsync(1);

    await expect(logoutResult).resolves.toBe(true);
    expect(commitSignal?.aborted).toBe(true);
    expect(callOrder).toEqual(['/session/commit', '/session/revoke']);
  });

  it('waits for an in-flight periodic cookie refresh before sending the final revoke', async () => {
    let resolveRefresh: ((value: { ok: boolean; status: number }) => void) | undefined;
    const callOrder: string[] = [];
    let refreshCalls = 0;
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const url = String(input);
      callOrder.push(url);
      if (url === '/session/refresh') {
        refreshCalls += 1;
        if (refreshCalls === 1) return Promise.resolve({ ok: true, status: 204 });
        return new Promise<{ ok: boolean; status: number }>((resolve) => {
          resolveRefresh = resolve;
        });
      }
      return Promise.resolve({ ok: true, status: 204 });
    });
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 60_000,
      sessionRefreshIntervalMs: 100
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'refresh-player',
      player_name: '玩家',
      web_session_ticket: 'refresh-ticket',
      reconnect_available: false
    }));
    await flushPromises();
    expect(callOrder).toEqual(['/session/commit', '/session/refresh']);

    vi.advanceTimersByTime(100);
    expect(callOrder).toEqual(['/session/commit', '/session/refresh', '/session/refresh']);
    const logoutResult = gameSocket.logout();
    await Promise.resolve();
    expect(callOrder).toEqual(['/session/commit', '/session/refresh', '/session/refresh']);

    resolveRefresh?.({ ok: true, status: 204 });
    await expect(logoutResult).resolves.toBe(true);
    expect(callOrder).toEqual(['/session/commit', '/session/refresh', '/session/refresh', '/session/revoke']);
  });

  it('bounds a stalled periodic refresh before sending the final revoke', async () => {
    const callOrder: string[] = [];
    let periodicRefreshSignal: AbortSignal | null | undefined;
    let refreshCalls = 0;
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const url = String(input);
      callOrder.push(url);
      if (url === '/session/refresh') {
        refreshCalls += 1;
        if (refreshCalls > 1) {
          periodicRefreshSignal = init?.signal;
          return new Promise<{ ok: boolean; status: number }>(() => undefined);
        }
      }
      return Promise.resolve({ ok: true, status: 204 });
    });
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws', {
      heartbeatIntervalMs: 60_000,
      sessionRefreshIntervalMs: 100,
      sessionRequestTimeoutMs: 50
    });
    gameSocket.connect();
    const socket = latestSocket();
    openAndNegotiate(socket);
    socket.receive(encodeMessage(MsgType.Connected, {
      player_id: 'stalled-refresh-player',
      player_name: '玩家',
      web_session_ticket: 'refresh-ticket',
      reconnect_available: false
    }));
    await flushPromises();

    await vi.advanceTimersByTimeAsync(100);
    expect(callOrder).toEqual(['/session/commit', '/session/refresh', '/session/refresh']);
    const logoutResult = gameSocket.logout();
    await Promise.resolve();
    expect(periodicRefreshSignal?.aborted).toBe(false);

    await vi.advanceTimersByTimeAsync(49);
    expect(callOrder).not.toContain('/session/revoke');
    await vi.advanceTimersByTimeAsync(1);

    await expect(logoutResult).resolves.toBe(true);
    expect(periodicRefreshSignal?.aborted).toBe(true);
    expect(callOrder).toEqual(['/session/commit', '/session/refresh', '/session/refresh', '/session/revoke']);
  });

  it('aborts and reports a stalled revoke within the request bound', async () => {
    let revokeSignal: AbortSignal | null | undefined;
    const fetchMock = vi.fn((_input: string | URL | Request, init?: RequestInit) => {
      revokeSignal = init?.signal;
      return new Promise<{ ok: boolean; status: number }>(() => undefined);
    });
    vi.stubGlobal('fetch', fetchMock);
    const gameSocket = new GameSocket('ws://example.test/ws', { sessionRequestTimeoutMs: 75 });

    const logoutResult = gameSocket.logout();
    await Promise.resolve();
    expect(revokeSignal?.aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(75);

    await expect(logoutResult).resolves.toBe(false);
    expect(revokeSignal?.aborted).toBe(true);
    expect(useAppStore.getState().error).toContain('服务器会话撤销失败');
  });

  it('quiesces reconnect handlers before revoking so a late ticket cannot restore identity state', async () => {
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
      player_id: 'temporary', player_name: '临时玩家', web_session_ticket: 'temporary-ticket', reconnect_available: true
    }));

    const logoutResult = gameSocket.logout();
    socket.receive(encodeMessage(MsgType.Reconnected, {
      player_id: 'logout-player', player_name: '玩家', room_code: '', web_session_ticket: 'late-ticket'
    }));
    expect(useAppStore.getState().playerId).toBe('');

    resolveFetch?.({ ok: true });
    await expect(logoutResult).resolves.toBe(true);
    expect(fetchMock).toHaveBeenCalledWith('/session/revoke', expect.objectContaining({
      body: JSON.stringify({})
    }));
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

    const result = gameSocket.send(MsgType.JoinRoom);
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

  it('does not commit a rotated cookie ticket after an unknown snapshot phase', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockClear();
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
      web_session_ticket: 'must-not-commit',
      game_state: { phase: 'paused' }
    }));
    vi.runAllTicks();
    vi.advanceTimersByTime(0);

    expect(socket.closeCalls).toContainEqual({ code: 4003, reason: 'authoritative resync' });
    expect(useAppStore.getState().error).toContain('paused');
    expect(fetchMock).not.toHaveBeenCalled();
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
      web_session_ticket: 'temporary-ticket',
      reconnect_available: true
    }));
    expect(decodeMessage(retry.sent[0])).toMatchObject({
      type: MsgType.Reconnect,
      payload: {}
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
      web_session_ticket: 'fresh-ticket',
      reconnect_available: false
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

async function flushPromises(): Promise<void> {
  for (let pass = 0; pass < 20; pass += 1) await Promise.resolve();
}

function openAndNegotiate(socket: FakeWebSocket): void {
  socket.open();
  const hello = decodeMessage(socket.sent.at(-1) ?? new Uint8Array());
  expect(hello).toMatchObject({
    type: MsgType.Hello,
    payload: {
      protocol_version: '1',
      capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat', 'http_only_session_ticket'],
      client_kind: 'web'
    },
    command: { request_id: expect.any(String) }
  });
  socket.receive(encodeMessage(MsgType.Negotiated, {
    protocol_version: '1',
    server_version: 'test',
    capabilities: ['command_correlation', 'idempotency', 'game_context', 'protobuf_chat', 'http_only_session_ticket'],
    client_kind: 'web'
  }, { request_id: hello.command?.request_id }));
  socket.sent = [];
}
