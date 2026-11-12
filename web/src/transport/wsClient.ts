import { resetClock } from './clock';
import { decodeMessage, encodeMessage } from '../protocol/codec';
import { MsgType, type IncomingMessage, type MessageType, type OutgoingPayload } from '../protocol/types';
import { useAppStore } from '../stores/appStore';

type Listener = (message: IncomingMessage) => void;

export class GameSocket {
  private socket: WebSocket | null = null;
  private pending: { token: string; player: string; at: number } | null = null;
  private lastPong = 0;
  private ready = false;
  private attempts = 0;
  private dialTimer: number | null = null;
  private wake = () => {
    if (document.hidden || this.intentionalClose) return;
    if (this.socket && Date.now() - this.lastPong > 15000) this.recover();
    else if (!this.socket) this.connect();
    else this.send(MsgType.Ping, { timestamp: Date.now() });
  };
  private heartbeat: number | null = null;
  private heartbeatsSinceOnlineCount = 0;
  private reconnectTimer: number | null = null;
  private intentionalClose = false;
  private readonly listeners = new Set<Listener>();

  constructor(private readonly url: string) {}

  connect(): void {
    if (this.socket) return;
    this.intentionalClose = false;
    if (this.reconnectTimer !== null) window.clearTimeout(this.reconnectTimer);
    window.addEventListener('online', this.wake);
    document.addEventListener('visibilitychange', this.wake);
    this.ready = false;
    resetClock();
    // Storage only restores a fresh page. A reconnect must retain the in-memory
    // submission even when storage was unavailable or its write failed.
    if (!this.pending) {
      try { this.pending = JSON.parse(sessionStorage.getItem('ddz_pending_action') || 'null'); } catch { /* optional reload recovery */ }
    }
    if (this.pending) useAppStore.setState({ submission: 'unknown', submissionMessage: '正在核对上次操作，请勿重复提交' });
    const store = useAppStore.getState();
    store.setError('');

    // Authentication and recovery happen during the cookie-authenticated handshake.
    try { sessionStorage.removeItem('ddz_next_reconnect'); } catch { /* storage may be disabled */ }
    const socket = new WebSocket(this.url);
    this.socket = socket;
    socket.binaryType = 'arraybuffer';
    this.dialTimer = window.setTimeout(() => this.recover(), 15000);

    socket.onopen = () => {
      if (this.socket !== socket) return;
      if (this.dialTimer !== null) window.clearTimeout(this.dialTimer);
      this.dialTimer = null;
      this.lastPong = Date.now();
      useAppStore.getState().setConnected(true);
      this.startHeartbeat();
      this.send(MsgType.Ping, { timestamp: Date.now() });
      this.send(MsgType.GetOnlineCount);
      this.send(MsgType.GetMaintenanceStatus);
    };

    socket.onmessage = (event) => {
      if (this.socket !== socket) return;
      let message: IncomingMessage;
      try { message = decodeMessage(event.data as ArrayBuffer); }
      catch { useAppStore.getState().setError('收到无效的服务器消息'); return; }
      if (message.type === MsgType.Pong) this.lastPong = Date.now();
      // The server sends connected (identity) before reconnected (final recovery
      // result). Only the latter can resolve a pending action or unlock sending.
      if (message.type === MsgType.Reconnected) {
        this.ready = true;
        this.attempts = 0;
        if (this.pending) {
          const p = message.payload as { player_id?: string; game_state?: { last_action_token?: string } };
          if (p.game_state) this.finish(p.player_id === this.pending.player && p.game_state.last_action_token === this.pending.token);
          else this.finish(null);
        }
      }
      const acknowledgement = message.payload as { action_token?: string; player_id?: string };
      if (this.pending && acknowledgement.action_token === this.pending.token && acknowledgement.player_id === this.pending.player) this.finish(true);
      // Errors may include a Redis commit whose acknowledgement was lost. Resolve
      // from a fresh authoritative snapshot instead of allowing a second click.
      if (message.type === MsgType.Error && this.pending) this.recover();
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
      if (this.dialTimer !== null) window.clearTimeout(this.dialTimer);
      this.dialTimer = null;
      this.ready = false;
      this.markUnknown();
      this.socket = null;
      useAppStore.getState().setConnected(false);
      if (event.code === 4001) {
        this.intentionalClose = true;
        useAppStore.getState().setError('此账号已在另一个页面继续游戏，本页面已停止重连');
        return;
      }
      if (!this.intentionalClose) {
        useAppStore.getState().setError('与服务器断开，正在重连');
        this.reconnectTimer = window.setTimeout(() => this.connect(), Math.min(15000, 2500 * 2 ** this.attempts++));
      }
    };
  }

  close(): void {
    this.intentionalClose = true;
    window.removeEventListener('online', this.wake);
    document.removeEventListener('visibilitychange', this.wake);
    if (this.dialTimer !== null) window.clearTimeout(this.dialTimer);
    this.dialTimer = null;
    this.ready = false;
    this.markUnknown();
    this.stopHeartbeat();
    if (this.reconnectTimer) window.clearTimeout(this.reconnectTimer);
    const socket = this.socket;
    this.socket = null;
    socket?.close();
    useAppStore.getState().setConnected(false);
  }

  send(type: MessageType | string, payload?: OutgoingPayload): boolean {
    const action = ['bid', 'play_cards', 'pass'].includes(type);
    const state = useAppStore.getState();
    if (action && this.pending) return false;
    if (this.socket?.readyState !== WebSocket.OPEN || (action && !this.ready)) {
      if (action) useAppStore.setState({ submission: 'rejected', submissionMessage: '操作未发送：连接尚未恢复，请稍后重新操作' });
      return false;
    }
    if (action) {
      const token = type === 'bid' ? state.bidToken : state.turnToken;
      if (!token || state.currentTurn !== state.playerId || (state.timerStart > 0 && Date.now() >= state.timerStart + state.timeout * 1000)) {
        useAppStore.setState({ submission: 'rejected', submissionMessage: '操作未发送：回合已变化，请按当前状态操作' });
        return false;
      }
      this.pending = { token, player: state.playerId, at: Date.now() };
      try { sessionStorage.setItem('ddz_pending_action', JSON.stringify(this.pending)); } catch { /* optional reload recovery */ }
      useAppStore.setState({ submission: 'pending', submissionMessage: '操作已提交，等待服务器确认…' });
    }
    try { this.socket.send(encodeMessage(type, payload)); return true; }
    catch { this.recover(); return false; }
  }

  private finish(accepted: boolean | null): void {
    this.pending = null;
    try { sessionStorage.removeItem('ddz_pending_action'); } catch { /* optional storage */ }
    if (accepted === null) {
      useAppStore.setState({ submission: 'idle', submissionMessage: '对局已不可恢复，无法核对上次操作结果；不会自动重发' });
      return;
    }
    useAppStore.setState({ submission: accepted ? 'confirmed' : 'rejected', submissionMessage: accepted ? '' : '服务器未接受上次操作，请按恢复后的回合状态操作' });
  }

  private markUnknown(): void {
    if (this.pending) useAppStore.setState({ submission: 'unknown', submissionMessage: '连接中断，操作结果待核对；重连后自动确认，不会自动重发' });
  }

  private recover(): void {
    const socket = this.socket;
    if (!socket) return;
    // Detach immediately: a half-open TCP close can otherwise stall indefinitely.
    socket.onclose?.({ code: 1006 } as CloseEvent);
    socket.close();
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.heartbeat = window.setInterval(() => {
      if (Date.now() - this.lastPong >= 15000 || (this.pending && Date.now() - this.pending.at >= 10000)) {
        this.recover();
        return;
      }
      this.send(MsgType.Ping, { timestamp: Date.now() });
      if (++this.heartbeatsSinceOnlineCount === 2) {
        this.send(MsgType.GetOnlineCount);
        this.heartbeatsSinceOnlineCount = 0;
      }
    }, 5000);
  }

  private stopHeartbeat(): void {
    if (this.heartbeat !== null) window.clearInterval(this.heartbeat);
    this.heartbeat = null;
    this.heartbeatsSinceOnlineCount = 0;
  }
}

export function createGameSocket(): GameSocket {
  const url = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`;
  return new GameSocket(url);
}
