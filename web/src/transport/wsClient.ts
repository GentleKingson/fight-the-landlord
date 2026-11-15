import { resetClock } from './clock';
import { decodeMessage, encodeMessage } from '../protocol/codec';
import { MsgType, type IncomingMessage, type MessageType, type OutgoingPayload } from '../protocol/types';
import { useAppStore } from '../stores/appStore';

type Listener = (message: IncomingMessage) => void;

interface PendingAction {
  token: string;
  player: string;
  at: number;
}

const PENDING_ACTION_STORAGE_KEY = 'ddz_pending_action';
const LEGACY_RECONNECT_STORAGE_KEY = 'ddz_next_reconnect';
const DIAL_TIMEOUT_MS = 15_000;
const PONG_TIMEOUT_MS = 15_000;
const PENDING_ACTION_TIMEOUT_MS = 10_000;
const HEARTBEAT_INTERVAL_MS = 5000;
const MAX_RECONNECT_DELAY_MS = 15_000;
const BASE_RECONNECT_DELAY_MS = 2500;
const SESSION_REPLACED_CLOSE_CODE = 4001;
const ABNORMAL_CLOSE_CODE = 1006;
const GAME_ACTIONS = new Set<string>([MsgType.Bid, MsgType.PlayCards, MsgType.Pass]);
const READY_GATED_LOBBY_ACTIONS = new Set<string>([
  MsgType.CreateRoom,
  MsgType.JoinRoom,
  MsgType.LeaveRoom,
  MsgType.QuickMatch,
  MsgType.CancelMatch,
  MsgType.PracticeMatch,
  MsgType.Ready,
  MsgType.CancelReady,
  MsgType.StartGame
]);

/** Manages the game WebSocket connection, heartbeat, reconnects, and idempotent turn submissions. */
export class GameSocket {
  private socket: WebSocket | null = null;
  private pending: PendingAction | null = null;
  private lastPong = 0;
  private ready = false;
  private attempts = 0;
  private dialTimer: number | null = null;
  private readonly wake = (): void => {
    if (document.hidden || this.intentionalClose) return;
    if (this.socket && Date.now() - this.lastPong > PONG_TIMEOUT_MS) {
      this.recover();
    } else if (!this.socket) {
      this.connect();
    } else {
      this.send(MsgType.Ping, { timestamp: Date.now() });
    }
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
      try {
        const stored: PendingAction | null = JSON.parse(sessionStorage.getItem(PENDING_ACTION_STORAGE_KEY) || 'null');
        this.pending = stored;
      } catch {
        // Optional reload recovery when sessionStorage is unavailable or malformed.
      }
    }
    if (this.pending) {
      useAppStore.setState({ submission: 'unknown', submissionMessage: '正在核对上次操作，请勿重复提交' });
    }
    useAppStore.setState({ error: '', sessionReplaced: false });

    // Authentication and recovery happen during the cookie-authenticated handshake.
    try {
      sessionStorage.removeItem(LEGACY_RECONNECT_STORAGE_KEY);
    } catch {
      // Storage may be disabled in private browsing contexts.
    }
    const socket = new WebSocket(this.url);
    this.socket = socket;
    socket.binaryType = 'arraybuffer';
    this.dialTimer = window.setTimeout(() => {
      this.recover();
    }, DIAL_TIMEOUT_MS);

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
      try {
        // socket.binaryType is configured as 'arraybuffer' above.
        message = decodeMessage(event.data as ArrayBuffer);
      } catch {
        useAppStore.getState().setError('收到无效的服务器消息');
        return;
      }
      if (message.type === MsgType.Pong) this.lastPong = Date.now();
      // The server sends connected (identity) before reconnected (final recovery
      // result). Only the latter can resolve a pending action or unlock sending.
      if (message.type === MsgType.Reconnected) {
        this.ready = true;
        this.attempts = 0;
        if (this.pending) {
          const payload = message.payload as { player_id?: string; game_state?: { last_action_token?: string } };
          if (payload.game_state) {
            this.finish(payload.player_id === this.pending.player && payload.game_state.last_action_token === this.pending.token);
          } else {
            this.finish(null);
          }
        }
      }
      const acknowledgement = message.payload as { action_token?: string; player_id?: string };
      if (
        this.pending &&
        acknowledgement.action_token === this.pending.token &&
        acknowledgement.player_id === this.pending.player
      ) {
        this.finish(true);
      }
      // Errors may include a Redis commit whose acknowledgement was lost. Resolve
      // from a fresh authoritative snapshot instead of allowing a second click.
      if (message.type === MsgType.Error && this.pending) this.recover();
      useAppStore.getState().handleMessage(message);
      for (const listener of this.listeners) {
        listener(message);
      }
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
      if (event.code === SESSION_REPLACED_CLOSE_CODE) {
        this.intentionalClose = true;
        useAppStore.setState({ sessionReplaced: true });
        return;
      }
      if (!this.intentionalClose) {
        useAppStore.getState().setError('与服务器断开，正在重连');
        this.reconnectTimer = window.setTimeout(() => {
          this.connect();
        }, Math.min(MAX_RECONNECT_DELAY_MS, BASE_RECONNECT_DELAY_MS * 2 ** this.attempts++));
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
    const action = GAME_ACTIONS.has(type);
    const requiresReady = action || READY_GATED_LOBBY_ACTIONS.has(type);
    const state = useAppStore.getState();
    if (action && this.pending) return false;
    if (this.socket?.readyState !== WebSocket.OPEN || (requiresReady && !this.ready)) {
      if (action) {
        useAppStore.setState({ submission: 'rejected', submissionMessage: '操作未发送：连接尚未恢复，请稍后重新操作' });
      }
      return false;
    }
    if (action) {
      const token = type === 'bid' ? state.bidToken : state.turnToken;
      if (!token || state.currentTurn !== state.playerId) {
        useAppStore.setState({ submission: 'rejected', submissionMessage: '操作未发送：回合已变化，请按当前状态操作' });
        return false;
      }
      this.pending = { token, player: state.playerId, at: Date.now() };
      try {
        sessionStorage.setItem(PENDING_ACTION_STORAGE_KEY, JSON.stringify(this.pending));
      } catch {
        // Optional reload recovery when sessionStorage is unavailable.
      }
      useAppStore.setState({ submission: 'pending', submissionMessage: '操作已提交，等待服务器确认…' });
    }
    try {
      this.socket.send(encodeMessage(type, payload));
      return true;
    } catch {
      this.recover();
      return false;
    }
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private finish(accepted: boolean | null): void {
    this.pending = null;
    try {
      sessionStorage.removeItem(PENDING_ACTION_STORAGE_KEY);
    } catch {
      // Optional storage cleanup.
    }
    if (accepted === null) {
      useAppStore.setState({ submission: 'idle', submissionMessage: '对局已不可恢复，无法核对上次操作结果；不会自动重发' });
      return;
    }
    useAppStore.setState({
      submission: accepted ? 'confirmed' : 'rejected',
      submissionMessage: accepted ? '' : '服务器未接受上次操作，请按恢复后的回合状态操作'
    });
  }

  private markUnknown(): void {
    if (this.pending) {
      useAppStore.setState({ submission: 'unknown', submissionMessage: '连接中断，操作结果待核对；重连后自动确认，不会自动重发' });
    }
  }

  private recover(): void {
    const socket = this.socket;
    if (!socket) return;
    // Detach immediately: a half-open TCP close can otherwise stall indefinitely.
    socket.onclose?.({ code: ABNORMAL_CLOSE_CODE } as CloseEvent);
    socket.close();
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.heartbeat = window.setInterval(() => {
      if (
        Date.now() - this.lastPong >= PONG_TIMEOUT_MS ||
        (this.pending && Date.now() - this.pending.at >= PENDING_ACTION_TIMEOUT_MS)
      ) {
        this.recover();
        return;
      }
      this.send(MsgType.Ping, { timestamp: Date.now() });
      if (++this.heartbeatsSinceOnlineCount === 2) {
        this.send(MsgType.GetOnlineCount);
        this.heartbeatsSinceOnlineCount = 0;
      }
    }, HEARTBEAT_INTERVAL_MS);
  }

  private stopHeartbeat(): void {
    if (this.heartbeat !== null) window.clearInterval(this.heartbeat);
    this.heartbeat = null;
    this.heartbeatsSinceOnlineCount = 0;
  }
}

/** Creates a GameSocket targeting the current page host's `/ws` endpoint. */
export function createGameSocket(): GameSocket {
  const url = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`;
  return new GameSocket(url);
}
