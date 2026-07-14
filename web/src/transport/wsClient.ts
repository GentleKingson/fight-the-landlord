import { ProtocolDecodeError, ProtocolEncodeError, decodeMessage, encodeMessage } from '../protocol/codec';
import { MsgType, type IncomingMessage, type MessageType, type OutgoingPayload } from '../protocol/types';
import {
  forgetReconnect,
  loadReconnect,
  useAppStore,
  type StoredIdentity
} from '../stores/appStore';

type Listener = (message: IncomingMessage) => void;

export type SendResult =
  | { ok: true }
  | { ok: false; reason: 'not-connected' | 'encode-failed' | 'send-failed'; error?: Error };

export class GameSocket {
  private socket: WebSocket | null = null;
  private heartbeat: number | null = null;
  private reconnectTimer: number | null = null;
  private reconnectIdentity: StoredIdentity | null = null;
  private intentionalClose = false;
  private readonly listeners = new Set<Listener>();

  constructor(private readonly url: string) {}

  connect(): void {
    if (this.socket) return;
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }

    this.intentionalClose = false;
    this.reconnectIdentity = loadReconnect();
    const store = useAppStore.getState();
    store.prepareConnection(this.reconnectIdentity);

    let socket: WebSocket;
    try {
      socket = new WebSocket(this.url);
    } catch (error) {
      store.setConnectionStatus('offline');
      store.setError(`无法建立连接：${errorMessage(error)}`);
      return;
    }

    this.socket = socket;
    socket.binaryType = 'arraybuffer';

    socket.onopen = () => {
      if (this.socket !== socket) return;
      this.startHeartbeat();
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
      this.socket = null;
      const currentStore = useAppStore.getState();
      currentStore.setConnected(false);
      currentStore.setConnectionStatus(this.intentionalClose ? 'idle' : 'offline');
      if (!this.intentionalClose) {
        currentStore.setError('与服务器断开，正在重连');
        this.reconnectTimer = window.setTimeout(() => this.connect(), 2500);
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
    if (this.reconnectTimer !== null) window.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    this.reconnectIdentity = null;
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

  logout(): void {
    this.forgetIdentity();
    this.shutdown();
  }

  send(type: MessageType, payload?: OutgoingPayload): SendResult {
    if (this.socket?.readyState !== WebSocket.OPEN) return { ok: false, reason: 'not-connected' };
    let frame: Uint8Array<ArrayBufferLike>;
    try {
      const encode = encodeMessage as (messageType: MessageType, value?: OutgoingPayload) => Uint8Array<ArrayBufferLike>;
      frame = encode(type, payload);
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
      const currentStore = useAppStore.getState();
      if (this.reconnectIdentity) {
        currentStore.setConnectionStatus('reconnecting');
        const result = this.send(MsgType.Reconnect, {
          player_id: this.reconnectIdentity.id,
          token: this.reconnectIdentity.token
        });
        if (!result.ok) currentStore.setError('无法发送重连请求，将在连接恢复后重试');
      } else {
        queueMicrotask(() => {
          if (this.socket === socket) useAppStore.getState().setConnectionStatus('connected');
        });
      }
      this.send(MsgType.GetOnlineCount);
      this.send(MsgType.GetMaintenanceStatus);
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

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.heartbeat = window.setInterval(() => {
      this.send(MsgType.Ping, { timestamp: Date.now() });
    }, 5000);
  }

  private stopHeartbeat(): void {
    if (this.heartbeat !== null) window.clearInterval(this.heartbeat);
    this.heartbeat = null;
  }
}

export function createGameSocket(): GameSocket {
  const url = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`;
  return new GameSocket(url);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
