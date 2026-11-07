import { decodeMessage, encodeMessage } from '../protocol/codec';
import { MsgType, type IncomingMessage, type MessageType, type OutgoingPayload } from '../protocol/types';
import { loadReconnect, useAppStore } from '../stores/appStore';

type Listener = (message: IncomingMessage) => void;

export class GameSocket {
  private socket: WebSocket | null = null;
  private heartbeat: number | null = null;
  private reconnectTimer: number | null = null;
  private intentionalClose = false;
  private readonly listeners = new Set<Listener>();

  constructor(private readonly url: string) {}

  connect(): void {
    if (this.socket) return;
    this.intentionalClose = false;
    const store = useAppStore.getState();
    store.setError('');

    const saved = loadReconnect();
    let reconnecting = Boolean(saved?.id && saved.token);
    let freshIdentity: IncomingMessage | null = null;
    const socket = new WebSocket(this.url);
    this.socket = socket;
    socket.binaryType = 'arraybuffer';

    socket.onopen = () => {
      if (this.socket !== socket) return;
      useAppStore.getState().setConnected(true);
      this.startHeartbeat();
      if (saved?.id && saved.token) {
        this.send(MsgType.Reconnect, { player_id: saved.id, token: saved.token });
      }
      this.send(MsgType.GetOnlineCount);
      this.send(MsgType.GetMaintenanceStatus);
    };

    socket.onmessage = (event) => {
      if (this.socket !== socket) return;
      let message: IncomingMessage;
      try { message = decodeMessage(event.data as ArrayBuffer); }
      catch { useAppStore.getState().setError('收到无效的服务器消息'); return; }
      // The server sends a temporary identity before answering Reconnect.
      // Keep the old token until recovery succeeds (also across page reloads).
      if (reconnecting && message.type === MsgType.Connected) {
        freshIdentity = message;
        return;
      }
      if (reconnecting && message.type === MsgType.Reconnected) {
        useAppStore.setState({ reconnectToken: saved!.token });
        reconnecting = false;
      } else if (reconnecting && message.type === MsgType.Error) {
        reconnecting = false;
        if (freshIdentity) useAppStore.getState().handleMessage(freshIdentity);
      }
      useAppStore.getState().handleMessage(message);
      for (const listener of this.listeners) listener(message);
    };

    socket.onerror = () => {
      if (this.socket !== socket) return;
      useAppStore.getState().setError('连接失败');
    };

    socket.onclose = (event) => {
      if (this.socket !== socket) return;
      this.stopHeartbeat();
      this.socket = null;
      useAppStore.getState().setConnected(false);
      if (event.code === 4001) {
        this.intentionalClose = true;
        useAppStore.getState().setError('此账号已在另一个页面继续游戏，本页面已停止重连');
        return;
      }
      if (!this.intentionalClose) {
        useAppStore.getState().setError('与服务器断开，正在重连');
        this.reconnectTimer = window.setTimeout(() => this.connect(), 2500);
      }
    };
  }

  close(): void {
    this.intentionalClose = true;
    this.stopHeartbeat();
    if (this.reconnectTimer) window.clearTimeout(this.reconnectTimer);
    const socket = this.socket;
    this.socket = null;
    socket?.close();
    useAppStore.getState().setConnected(false);
  }

  send(type: MessageType | string, payload?: OutgoingPayload): void {
    if (this.socket?.readyState !== WebSocket.OPEN) return;
    this.socket.send(encodeMessage(type, payload));
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.heartbeat = window.setInterval(() => {
      this.send(MsgType.Ping, { timestamp: Date.now() });
    }, 5000);
  }

  private stopHeartbeat(): void {
    if (this.heartbeat) window.clearInterval(this.heartbeat);
    this.heartbeat = null;
  }
}

export function createGameSocket(): GameSocket {
  const url = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`;
  return new GameSocket(url);
}
