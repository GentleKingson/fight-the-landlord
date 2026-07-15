import { ProtocolDecodeError, ProtocolEncodeError, decodeMessage, encodeMessage } from '../protocol/codec';
import { MsgType, type CommandMeta, type IncomingMessage, type MessageType, type OutgoingPayload } from '../protocol/types';
import {
  forgetReconnect,
  loadReconnect,
  useAppStore,
  type StoredIdentity
} from '../stores/appStore';
import { readClientVersion } from '../version/compatibility';

type Listener = (message: IncomingMessage) => void;

export type SendResult =
  | { ok: true }
  | { ok: false; reason: 'not-connected' | 'encode-failed' | 'send-failed'; error?: Error };

export type CommandMetadata = Partial<CommandMeta> & { request_id: string };

export interface GameSocketOptions {
  heartbeatIntervalMs?: number;
  pongTimeoutMs?: number;
  reconnectBaseDelayMs?: number;
  reconnectMaxDelayMs?: number;
  reconnectJitterRatio?: number;
  random?: () => number;
}

interface ResolvedGameSocketOptions {
  heartbeatIntervalMs: number;
  pongTimeoutMs: number;
  reconnectBaseDelayMs: number;
  reconnectMaxDelayMs: number;
  reconnectJitterRatio: number;
  random: () => number;
}

const DEFAULT_OPTIONS: ResolvedGameSocketOptions = {
  heartbeatIntervalMs: 5000,
  pongTimeoutMs: 10_000,
  reconnectBaseDelayMs: 1000,
  reconnectMaxDelayMs: 30_000,
  reconnectJitterRatio: 0.2,
  random: Math.random
};

const PROTOCOL_VERSION = '1';
const WEB_CLIENT_KIND = 'web';
const REQUIRED_CAPABILITIES = [
  'command_correlation',
  'idempotency',
  'game_context',
  'protobuf_chat'
] as const;

export class GameSocket {
  private socket: WebSocket | null = null;
  private heartbeat: number | null = null;
  private pongWatchdog: number | null = null;
  private reconnectTimer: number | null = null;
  private reconnectAttempt = 0;
  private reconnectIdentity: StoredIdentity | null = null;
  private intentionalClose = false;
  private networkOffline = false;
  private reconnectWhenClosed = false;
  private closeError: string | null = null;
  private browserListenersInstalled = false;
  private negotiated = false;
  private helloRequestId: string | null = null;
  private readonly listeners = new Set<Listener>();
  private readonly options: ResolvedGameSocketOptions;

  constructor(private readonly url: string, options: GameSocketOptions = {}) {
    this.options = resolveOptions(options);
  }

  connect(): void {
    this.installBrowserListeners();
    if (this.socket) return;
    this.clearReconnectTimer();

    this.intentionalClose = false;
    this.networkOffline = typeof navigator !== 'undefined' && navigator.onLine === false;
    if (this.networkOffline) {
      const store = useAppStore.getState();
      store.setConnected(false);
      store.setConnectionStatus('offline');
      store.setError('网络已离线，恢复后将自动重连');
      return;
    }

    this.reconnectIdentity = loadReconnect();
    const store = useAppStore.getState();
    store.prepareConnection(this.reconnectIdentity);

    let socket: WebSocket;
    try {
      socket = new WebSocket(this.url);
    } catch (error) {
      store.setConnectionStatus('offline');
      store.setError(`无法建立连接：${errorMessage(error)}`);
      this.scheduleReconnect();
      return;
    }

    this.socket = socket;
    this.negotiated = false;
    this.helloRequestId = null;
    this.closeError = null;
    socket.binaryType = 'arraybuffer';

    socket.onopen = () => {
      if (this.socket !== socket) return;
      const requestId = createRequestID();
      this.helloRequestId = requestId;
      const result = this.send(MsgType.Hello, {
        protocol_version: PROTOCOL_VERSION,
        client_version: readClientVersion(),
        capabilities: [...REQUIRED_CAPABILITIES],
        client_kind: WEB_CLIENT_KIND
      }, { request_id: requestId });
      if (!result.ok) {
        const detail = result.error?.message;
        this.closeUnhealthySocket(socket, detail ? `无法完成协议协商：${detail}` : '无法完成协议协商');
      }
    };

    socket.onmessage = (event) => {
      if (this.socket !== socket) return;
      let message: IncomingMessage;
      try {
        message = decodeMessage(event.data as ArrayBuffer);
      } catch (error) {
        const detail = error instanceof ProtocolDecodeError ? error.message : errorMessage(error);
        useAppStore.getState().setError(`收到无效的服务器消息：${detail}`);
        return;
      }

      if (message.type === MsgType.Pong) this.clearPongWatchdog();
      if (!this.advanceNegotiationState(message, socket)) return;
      useAppStore.getState().handleMessage(message);
      this.advanceIdentityState(message, socket);
      for (const listener of this.listeners) listener(message);
    };

    socket.onerror = () => {
      if (this.socket !== socket) return;
      useAppStore.getState().setError('连接失败');
    };

    socket.onclose = () => {
      if (this.socket !== socket) return;
      this.stopHeartbeat();
      this.negotiated = false;
      this.helloRequestId = null;
      this.socket = null;
      const reconnectImmediately = this.reconnectWhenClosed;
      const closeError = this.closeError;
      this.reconnectWhenClosed = false;
      this.closeError = null;
      const currentStore = useAppStore.getState();
      currentStore.setConnected(false);
      currentStore.setConnectionStatus(this.intentionalClose ? 'idle' : 'offline');
      if (!this.intentionalClose && !this.networkOffline) {
        currentStore.setError(closeError ?? '与服务器断开，正在重连');
        if (reconnectImmediately) this.connect();
        else this.scheduleReconnect();
      }
    };
  }

  disconnect(): void {
    this.shutdown();
  }

  shutdown(): void {
    this.intentionalClose = true;
    const store = useAppStore.getState();
    store.setConnectionStatus('closing');
    this.stopHeartbeat();
    this.clearReconnectTimer();
    this.reconnectAttempt = 0;
    this.reconnectIdentity = null;
    this.negotiated = false;
    this.helloRequestId = null;
    this.reconnectWhenClosed = false;
    this.closeError = null;
    this.removeBrowserListeners();
    const socket = this.socket;
    this.socket = null;
    socket?.close();
    store.setConnected(false);
    store.setConnectionStatus('idle');
  }

  forgetIdentity(): void {
    forgetReconnect();
    this.reconnectIdentity = null;
    useAppStore.getState().clearIdentity();
  }

  async logout(): Promise<boolean> {
    // Detach handlers before reading the credential. A late Reconnected frame
    // can no longer rotate localStorage while revocation is in flight.
    this.shutdown();
    const identity = loadReconnect();
    this.forgetIdentity();
    let revokeFailed = false;
    if (identity && typeof fetch === 'function') {
      try {
        const response = await fetch('/session/revoke', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ player_id: identity.id, token: identity.token }),
          credentials: 'same-origin',
          keepalive: true
        });
        revokeFailed = !response.ok;
      } catch {
        revokeFailed = true;
      }
    }
    if (revokeFailed) {
      useAppStore.getState().setError('服务器会话撤销失败，旧凭证将在短期有效期结束后失效');
    }
    return !revokeFailed;
  }

  send(type: MessageType, payload?: OutgoingPayload, command?: CommandMetadata): SendResult {
    if (
      this.socket?.readyState !== WebSocket.OPEN
      || (!this.negotiated && type !== MsgType.Hello)
    ) return { ok: false, reason: 'not-connected' };
    let frame: Uint8Array<ArrayBufferLike>;
    try {
      const effectiveCommand = command ?? { request_id: createRequestID() };
      const encode = encodeMessage as (
        messageType: MessageType,
        value?: OutgoingPayload,
        metadata?: CommandMetadata
      ) => Uint8Array<ArrayBufferLike>;
      frame = encode(type, payload, effectiveCommand);
    } catch (error) {
      const typedError = error instanceof Error ? error : new Error(String(error));
      const detail = error instanceof ProtocolEncodeError ? error.message : typedError.message;
      useAppStore.getState().setError(`消息编码失败：${detail}`);
      return { ok: false, reason: 'encode-failed', error: typedError };
    }

    try {
      this.socket.send(frame);
      return { ok: true };
    } catch (error) {
      const typedError = error instanceof Error ? error : new Error(String(error));
      useAppStore.getState().setError(`消息发送失败：${typedError.message}`);
      return { ok: false, reason: 'send-failed', error: typedError };
    }
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private advanceIdentityState(message: IncomingMessage, socket: WebSocket): void {
    if (message.type === MsgType.Connected) {
      this.reconnectAttempt = 0;
      const currentStore = useAppStore.getState();
      if (this.reconnectIdentity) {
        currentStore.setConnectionStatus('reconnecting');
        const result = this.sendCommand(MsgType.Reconnect, {
          player_id: this.reconnectIdentity.id,
          token: this.reconnectIdentity.token
        });
        if (!result.ok) currentStore.setError('无法发送重连请求，将在连接恢复后重试');
      } else {
        queueMicrotask(() => {
          if (this.socket === socket) useAppStore.getState().setConnectionStatus('connected');
        });
      }
      this.sendCommand(MsgType.GetOnlineCount);
      this.sendCommand(MsgType.GetMaintenanceStatus);
      return;
    }

    if (message.type === MsgType.Reconnected) {
      this.reconnectIdentity = null;
      queueMicrotask(() => {
        if (this.socket === socket) useAppStore.getState().setConnectionStatus('connected');
      });
      return;
    }

    if (message.type === MsgType.Error && useAppStore.getState().connectionStatus === 'connected') {
      this.reconnectIdentity = null;
    }
  }

  private advanceNegotiationState(message: IncomingMessage, socket: WebSocket): boolean {
    if (message.type === MsgType.Negotiated) {
      const error = this.negotiationError(message);
      if (error) {
        this.rejectNegotiation(socket, error, 'invalid negotiation');
        return false;
      }
      this.helloRequestId = null;
      this.negotiated = true;
      if (!document.hidden) this.startHeartbeat();
      return true;
    }

    if (message.type === MsgType.ProtocolRejected) {
      const payload = message.payload as { request_id?: string; reason?: string };
      const reason = payload.request_id === this.helloRequestId
        ? payload.reason || '客户端协议与服务器不兼容'
        : '协议拒绝响应的 request_id 不匹配';
      this.rejectNegotiation(socket, reason, 'protocol rejected');
      return false;
    }
    return true;
  }

  private negotiationError(message: IncomingMessage): string | null {
    if (!this.helloRequestId || message.command?.request_id !== this.helloRequestId) {
      return '协议协商响应的 request_id 不匹配';
    }
    const payload = message.payload as {
      protocol_version?: string;
      capabilities?: string[];
      client_kind?: string;
    };
    if (payload.protocol_version !== PROTOCOL_VERSION) {
      return `服务器协商了不兼容的协议版本：${payload.protocol_version || '未知'}`;
    }
    if (payload.client_kind !== WEB_CLIENT_KIND) {
      return `服务器协商了错误的客户端类型：${payload.client_kind || '未知'}`;
    }
    if (!Array.isArray(payload.capabilities)) {
      return '服务器协议协商缺少 capabilities';
    }
    const missing = REQUIRED_CAPABILITIES.find((capability) => !payload.capabilities?.includes(capability));
    return missing ? `服务器协议协商缺少必需 capability：${missing}` : null;
  }

  private rejectNegotiation(socket: WebSocket, message: string, closeReason: string): void {
    this.intentionalClose = true;
    this.negotiated = false;
    this.helloRequestId = null;
    this.closeError = message;
    useAppStore.getState().setError(message);
    socket.close(4002, closeReason);
  }

  private sendCommand(type: MessageType, payload?: OutgoingPayload): SendResult {
    return this.send(type, payload, { request_id: createRequestID() });
  }

  private startHeartbeat(sendImmediately = false): void {
    this.stopHeartbeat();
    if (sendImmediately) this.sendHeartbeat();
    this.heartbeat = window.setInterval(() => this.sendHeartbeat(), this.options.heartbeatIntervalMs);
  }

  private sendHeartbeat(): void {
    if (this.pongWatchdog !== null) return;
    const socket = this.socket;
    if (!socket || socket.readyState !== WebSocket.OPEN) return;
    const result = this.sendCommand(MsgType.Ping, { timestamp: Date.now() });
    if (!result.ok) {
      if (result.reason === 'send-failed') this.closeUnhealthySocket(socket, '心跳发送失败');
      return;
    }
    this.pongWatchdog = window.setTimeout(() => {
      this.pongWatchdog = null;
      this.closeUnhealthySocket(socket, '服务器心跳超时，正在重连');
    }, this.options.pongTimeoutMs);
  }

  private closeUnhealthySocket(socket: WebSocket, message: string): void {
    if (this.socket !== socket) return;
    this.closeError = message;
    useAppStore.getState().setError(message);
    socket.close(4000, 'heartbeat timeout');
  }

  private stopHeartbeat(): void {
    if (this.heartbeat !== null) window.clearInterval(this.heartbeat);
    this.heartbeat = null;
    this.clearPongWatchdog();
  }

  private clearPongWatchdog(): void {
    if (this.pongWatchdog !== null) window.clearTimeout(this.pongWatchdog);
    this.pongWatchdog = null;
  }

  private scheduleReconnect(): void {
    if (this.intentionalClose || this.networkOffline || this.reconnectTimer !== null || this.socket) return;
    const exponent = Math.min(this.reconnectAttempt, 30);
    const exponentialDelay = this.options.reconnectBaseDelayMs * 2 ** exponent;
    const jitterMultiplier = 1 + (unitRandom(this.options.random) * 2 - 1) * this.options.reconnectJitterRatio;
    const delay = Math.min(this.options.reconnectMaxDelayMs, Math.max(0, Math.round(exponentialDelay * jitterMultiplier)));
    this.reconnectAttempt += 1;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) window.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
  }

  private installBrowserListeners(): void {
    if (this.browserListenersInstalled) return;
    window.addEventListener('online', this.handleOnline);
    window.addEventListener('offline', this.handleOffline);
    document.addEventListener('visibilitychange', this.handleVisibilityChange);
    this.browserListenersInstalled = true;
  }

  private removeBrowserListeners(): void {
    if (!this.browserListenersInstalled) return;
    window.removeEventListener('online', this.handleOnline);
    window.removeEventListener('offline', this.handleOffline);
    document.removeEventListener('visibilitychange', this.handleVisibilityChange);
    this.browserListenersInstalled = false;
  }

  private readonly handleOnline = (): void => {
    if (this.intentionalClose) return;
    this.networkOffline = false;
    this.reconnectAttempt = 0;
    this.clearReconnectTimer();
    if (!this.socket) {
      this.connect();
    } else if (this.socket.readyState === WebSocket.CLOSING || this.socket.readyState === WebSocket.CLOSED) {
      this.reconnectWhenClosed = true;
    }
  };

  private readonly handleOffline = (): void => {
    if (this.intentionalClose) return;
    this.networkOffline = true;
    this.reconnectWhenClosed = false;
    this.closeError = null;
    this.clearReconnectTimer();
    this.stopHeartbeat();
    const store = useAppStore.getState();
    store.setConnected(false);
    store.setConnectionStatus('offline');
    store.setError('网络已离线，恢复后将自动重连');
    this.socket?.close(4001, 'browser offline');
  };

  private readonly handleVisibilityChange = (): void => {
    if (this.intentionalClose) return;
    if (document.hidden) {
      this.stopHeartbeat();
      return;
    }
    if (this.networkOffline || (typeof navigator !== 'undefined' && navigator.onLine === false)) return;
    if (this.socket?.readyState === WebSocket.OPEN && this.negotiated) {
      this.startHeartbeat(true);
    } else if (!this.socket) {
      this.clearReconnectTimer();
      this.connect();
    }
  };
}

export function createRequestID(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID();
  return `web-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

export function createGameSocket(): GameSocket {
  const url = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`;
  return new GameSocket(url);
}

function resolveOptions(options: GameSocketOptions): ResolvedGameSocketOptions {
  const heartbeatIntervalMs = positiveDuration(options.heartbeatIntervalMs, DEFAULT_OPTIONS.heartbeatIntervalMs);
  const pongTimeoutMs = positiveDuration(options.pongTimeoutMs, DEFAULT_OPTIONS.pongTimeoutMs);
  const reconnectBaseDelayMs = positiveDuration(options.reconnectBaseDelayMs, DEFAULT_OPTIONS.reconnectBaseDelayMs);
  const reconnectMaxDelayMs = positiveDuration(options.reconnectMaxDelayMs, DEFAULT_OPTIONS.reconnectMaxDelayMs);
  const reconnectJitterRatio = Math.min(1, Math.max(0, options.reconnectJitterRatio ?? DEFAULT_OPTIONS.reconnectJitterRatio));
  return {
    heartbeatIntervalMs,
    pongTimeoutMs,
    reconnectBaseDelayMs,
    reconnectMaxDelayMs,
    reconnectJitterRatio,
    random: options.random ?? DEFAULT_OPTIONS.random
  };
}

function unitRandom(random: () => number): number {
  const value = random();
  if (!Number.isFinite(value)) return 0.5;
  return Math.min(1, Math.max(0, value));
}

function positiveDuration(value: number | undefined, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : fallback;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
