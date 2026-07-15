import { create } from 'zustand';
import { MsgType, WireMessageType, type ChatPayload, type GameStateDTO, type IncomingMessage, type LeaderboardEntry, type LobbyPanel, type PlayerInfo, type RoomListItem, type StatsResultPayload, type UtilityDrawer } from '../protocol/types';
import { initialConnectionSlice, type ConnectionSlice, type ConnectionStatus, type StoredIdentity } from './slices/connectionSlice';
import { initialLobbySlice, type LobbySlice } from './slices/lobbySlice';
import { initialRoomSlice, mergePlayer, normalizeRoomPlayer, normalizeRoomPlayers, type RoomSlice } from './slices/roomSlice';
import { GameSnapshotSyncError, initialGameSlice, isGameMessage, mapSnapshotPhase, reduceGameMessage, restoreGameSnapshot, shouldRestoreSnapshot, type GameSlice } from './slices/gameSlice';
import { initialUiSlice, type BusinessError, type BusinessErrorCategory, type CommandKind, type CommandRequest, type PendingCommand, type UiSlice } from './slices/uiSlice';
import { observePong, observeServerTimestamp } from './slices/clock';
import { useChatStore } from './slices/chatSlice';

export type { ConnectionStatus, StoredIdentity } from './slices/connectionSlice';
export type { BusinessError, BusinessErrorCategory, CommandKind, CommandRequest, PendingCommand } from './slices/uiSlice';
export type { SeatAction, TableAction } from './slices/gameSlice';
export { gameChatContext, roomChatContext, selectChatMessages, useChatStore } from './slices/chatSlice';

interface AppActions {
  setConnected: (connected: boolean) => void;
  prepareConnection: (identity: StoredIdentity | null) => void;
  setConnectionStatus: (status: ConnectionStatus) => void;
  clearIdentity: () => void;
  setError: (error: string) => void;
  setBusinessError: (category: BusinessErrorCategory, message: string, retry?: CommandRequest, command?: CommandKind) => void;
  clearBusinessError: () => void;
  beginCommand: (request: CommandRequest, requestId: string, timeoutMs: number, retryable: boolean) => void;
  finishCommand: (kind: CommandKind, requestId: string) => void;
  clearPendingCommands: () => void;
  setLobbyPanel: (panel: LobbyPanel) => void;
  setRoomCodeInput: (value: string) => void;
  setChatInput: (value: string) => void;
  setDrawer: (drawer: UtilityDrawer) => void;
  toggleCard: (key: string) => void;
  setSelection: (keys: string[]) => void;
  clearSelection: () => void;
  leaveLocalRoom: () => void;
  handleMessage: (message: IncomingMessage) => MessageHandlingResult | undefined;
}

export interface MessageHandlingResult {
  authoritativeResyncRequired: true;
  reason: string;
}

export type AppState = ConnectionSlice & LobbySlice & RoomSlice & GameSlice & UiSlice & AppActions;

const commandTimers = new Map<CommandKind, number>();
let nextBusinessErrorID = 1;

export const useAppStore = create<AppState>((set, get) => ({
  ...initialConnectionSlice,
  ...initialLobbySlice,
  ...initialRoomSlice,
  ...initialGameSlice,
  ...initialUiSlice,

  setConnected: (connected) => {
    if (!connected) {
      cancelAllCommandTimers();
      set({ connected, pendingCommands: {} });
      return;
    }
    set({ connected });
  },
  prepareConnection: (reconnectCandidate) => {
    cancelAllCommandTimers();
    set({
      connected: false,
      connectionStatus: 'connecting',
      reconnectCandidate,
      provisionalIdentity: null,
      reconnectNotice: '',
      error: '',
      pendingCommands: {},
      businessError: null
    });
  },
  setConnectionStatus: (connectionStatus) => {
    if (connectionStatus === 'offline' || connectionStatus === 'closing' || connectionStatus === 'idle') {
      cancelAllCommandTimers();
      set({ connectionStatus, pendingCommands: {} });
      return;
    }
    set({ connectionStatus });
  },
  clearIdentity: () => set({
    playerId: '',
    playerName: '',
    reconnectToken: '',
    reconnectCandidate: null,
    provisionalIdentity: null,
    reconnectNotice: ''
  }),
  setError: (error) => set({
    error,
    businessError: error ? createBusinessError('network', error) : null
  }),
  setBusinessError: (category, message, retry, command) => set({
    error: message,
    businessError: createBusinessError(category, message, retry, command)
  }),
  clearBusinessError: () => set({ error: '', businessError: null }),
  beginCommand: (request, requestId, timeoutMs, retryable) => {
    const kind = request.kind;
    const startedAt = Date.now();
    const pending: PendingCommand = {
      kind,
      requestId,
      request,
      startedAt,
      timeoutAt: startedAt + timeoutMs,
      retryable
    };
    cancelCommandTimer(kind);
    set({ pendingCommands: { ...get().pendingCommands, [kind]: pending } });
    const timer = window.setTimeout(() => {
      commandTimers.delete(kind);
      const current = get().pendingCommands[kind];
      if (!current || current.requestId !== requestId) return;
      const pendingCommands = { ...get().pendingCommands };
      delete pendingCommands[kind];
      set({
        pendingCommands,
        error: `${commandLabel(kind)}等待服务器响应超时`,
        businessError: createBusinessError(
          'timeout',
          `${commandLabel(kind)}等待服务器响应超时`,
          current.retryable ? current.request : undefined,
          kind
        )
      });
    }, timeoutMs);
    commandTimers.set(kind, timer);
  },
  finishCommand: (kind, requestId) => {
    const current = get().pendingCommands[kind];
    if (!current || current.requestId !== requestId) return;
    cancelCommandTimer(kind);
    const pendingCommands = { ...get().pendingCommands };
    delete pendingCommands[kind];
    set({ pendingCommands });
  },
  clearPendingCommands: () => {
    cancelAllCommandTimers();
    set({ pendingCommands: {} });
  },
  setLobbyPanel: (lobbyPanel) => set({ lobbyPanel }),
  setRoomCodeInput: (roomCodeInput) => set({ roomCodeInput }),
  setChatInput: (chatInput) => set({ chatInput }),
  setDrawer: (drawer) => set({ drawer }),
  toggleCard: (key) => {
    const selectedCards = new Set(get().selectedCards);
    if (selectedCards.has(key)) selectedCards.delete(key);
    else selectedCards.add(key);
    set({ selectedCards, tableMessage: '' });
  },
  setSelection: (keys) => set({ selectedCards: new Set(keys) }),
  clearSelection: () => set({ selectedCards: new Set(), tableMessage: '' }),
  leaveLocalRoom: () => set({
    roomCode: '',
    players: [],
    phase: 'lobby',
    matchDeadlineMs: 0,
    matchPractice: false,
    ...initialGameSlice,
    selectedCards: new Set()
  }),
  handleMessage: (message) => {
    const state = get();
    if (isGameMessage(message)) {
      const receivedAt = Date.now();
      const result = reduceGameMessage(state, message, receivedAt, state.serverClockOffsetMs);
      if (!result || result.ignored) return;
      const clock = observeServerTimestamp(state, result.serverTimestamp, receivedAt);
      const clearsTurnMessage = message.type === MsgType.BidTurn || message.type === MsgType.PlayTurn;
      const clearsMatch = message.type === MsgType.GameStart
        ? { matchDeadlineMs: 0, matchPractice: false }
        : {};
      set({ ...result.patch, ...clock, ...clearsMatch, ...(clearsTurnMessage ? { tableMessage: '' } : {}) });
      return;
    }
    switch (message.type) {
      case MsgType.Connected: {
        const payload = message.payload as { player_id: string; player_name: string; reconnect_token: string };
        const provisionalIdentity = {
          id: payload.player_id,
          name: payload.player_name,
          token: payload.reconnect_token
        };
        if (state.reconnectCandidate) {
          set({
            connected: true,
            connectionStatus: 'fresh-connected',
            provisionalIdentity,
            error: '',
            businessError: null
          });
          break;
        }
        persistReconnect(payload.player_id, payload.reconnect_token);
        set({
          connected: true,
          error: '',
          businessError: null,
          connectionStatus: 'fresh-connected',
          phase: 'lobby',
          playerId: payload.player_id,
          playerName: payload.player_name,
          reconnectToken: payload.reconnect_token,
          reconnectCandidate: null,
          provisionalIdentity: null
        });
        break;
      }
      case MsgType.Reconnected: {
        const payload = message.payload as { player_id: string; player_name: string; room_code?: string; game_state?: GameStateDTO; reconnect_token?: string };
        if (!payload.reconnect_token) {
          acceptProvisionalAfterReconnectFailure(set, state, '服务器未确认新的重连凭证');
          break;
        }
        persistReconnect(payload.player_id, payload.reconnect_token);
        const connectionState = {
          connected: true,
          connectionStatus: 'reconnected' as const,
          error: '',
          businessError: null,
          reconnectNotice: '',
          reconnectToken: payload.reconnect_token,
          reconnectCandidate: null,
          provisionalIdentity: null
        };
        if (payload.game_state) {
          try {
            mapSnapshotPhase(payload.game_state.phase);
          } catch (error) {
            if (!(error instanceof GameSnapshotSyncError)) throw error;
            const reason = `${error.message}，正在重新获取权威快照`;
            set({
              connected: false,
              connectionStatus: 'reconnecting',
              playerId: payload.player_id,
              playerName: payload.player_name,
              reconnectToken: payload.reconnect_token,
              reconnectCandidate: null,
              provisionalIdentity: null,
              error: reason,
              businessError: createBusinessError('network', reason)
            });
            return { authoritativeResyncRequired: true, reason };
          }
        }
        if (payload.game_state && shouldRestoreSnapshot(state, payload.game_state, message.event)) {
          const receivedAt = Date.now();
          const snapshot = restoreGameSnapshot(payload.game_state, {
            currentPlayerId: payload.player_id,
            receivedAt,
            event: message.event,
            seenGameStreams: state.seenGameStreams
          });
          const clock = observeServerTimestamp(
            state,
            message.event?.server_time_ms ?? payload.game_state.server_time_ms,
            receivedAt
          );
          set({
            ...connectionState,
            ...clock,
            playerId: payload.player_id,
            playerName: payload.player_name,
            roomCode: payload.room_code ?? '',
            matchDeadlineMs: 0,
            matchPractice: false,
            ...snapshot
          });
        } else {
          set({ ...connectionState, playerId: payload.player_id, playerName: payload.player_name, roomCode: payload.room_code ?? '', ...(payload.game_state ? {} : { phase: payload.room_code ? 'waiting' : 'lobby' }) });
        }
        break;
      }
      case MsgType.Pong: {
        set(observePong(state, message.payload, Date.now()));
        break;
      }
      case MsgType.Error: {
        const payload = message.payload as { code?: number; message?: string; request_id?: string; command_type?: WireMessageType };
        if (state.connectionStatus === 'reconnecting' && isReconnectFailure(payload.code, payload.message)) {
          acceptProvisionalAfterReconnectFailure(set, state, payload.message || '旧会话已失效');
          break;
        }
        const requestId = payload.request_id || commandRequestID(message);
        const correlated = findPendingCommand(state.pendingCommands, requestId, payload.command_type);
        if (requestId && !correlated) break;
        const command = correlated?.kind ?? commandKindForMessageType(payload.command_type);
        const pending = correlated?.pending;
        if (correlated && requestId) get().finishCommand(correlated.kind, requestId);
        const errorMessage = payload.message || '未知错误';
        set({
          error: errorMessage,
          businessError: createBusinessError(
            classifyServerError(payload.code),
            errorMessage,
            pending?.retryable ? pending.request : undefined,
            command
          ),
          tableMessage: state.phase === 'playing' ? errorMessage || '操作被服务器拒绝' : state.tableMessage
        });
        break;
      }
      case MsgType.CommandAck: {
        const payload = message.payload as { request_id?: string; command_type?: WireMessageType };
        const requestId = payload.request_id || commandRequestID(message);
        const correlated = findPendingCommand(state.pendingCommands, requestId, payload.command_type);
        if (correlated && requestId) get().finishCommand(correlated.kind, requestId);
        break;
      }
      case MsgType.Warning: {
        const payload = message.payload as { code?: number; message?: string };
        const warningMessage = payload.message || '请求过于频繁，请稍后再试';
        set({
          error: warningMessage,
          businessError: createBusinessError('rate-limit', warningMessage)
        });
        break;
      }
      case MsgType.OnlineCount:
        set({ onlineCount: (message.payload as { count: number }).count });
        break;
      case MsgType.RoomCreated: {
        const payload = message.payload as { room_code: string; player: PlayerInfo };
        set({ roomCode: payload.room_code, players: [normalizeRoomPlayer(payload.player)], phase: 'waiting', lobbyPanel: 'home' });
        break;
      }
      case MsgType.RoomJoined: {
        const payload = message.payload as { room_code: string; players: PlayerInfo[] };
        set({
          roomCode: payload.room_code,
          players: normalizeRoomPlayers(payload.players ?? []),
          phase: 'waiting',
          lobbyPanel: 'home',
          matchDeadlineMs: 0,
          matchPractice: false
        });
        break;
      }
      case MsgType.PlayerJoined: {
        const payload = message.payload as { player: PlayerInfo };
        set({ players: mergePlayer(state.players, normalizeRoomPlayer(payload.player)) });
        break;
      }
      case MsgType.PlayerLeft: {
        const payload = message.payload as { player_id: string };
        if (payload.player_id !== state.playerId) {
          set({ players: state.players.filter((player) => player.id !== payload.player_id) });
        }
        break;
      }
      case MsgType.PlayerReady: {
        const payload = message.payload as { player_id: string; ready: boolean };
        set({ players: state.players.map((player) => player.id === payload.player_id ? { ...player, ready: payload.ready } : player) });
        break;
      }
      case MsgType.MatchFound:
        set({ phase: 'waiting' });
        break;
      case MsgType.MatchQueued: {
        const payload = message.payload as { deadline_ms: number; practice: boolean };
        set({
          phase: 'matching',
          matchDeadlineMs: payload.deadline_ms,
          matchPractice: payload.practice
        });
        break;
      }
      case MsgType.MatchCancelled: {
        const payload = message.payload as { reason: string };
        const timedOut = payload.reason.toLowerCase().includes('timeout');
        set({
          phase: 'lobby',
          matchDeadlineMs: 0,
          matchPractice: false,
          error: timedOut ? '匹配等待超时，请重试' : state.error,
          businessError: timedOut
            ? createBusinessError(
                'timeout',
                '匹配等待超时，请重试',
                state.matchPractice ? undefined : { kind: 'quick-match' },
                state.matchPractice ? 'practice-match' : 'quick-match'
              )
            : state.businessError
        });
        break;
      }
      case MsgType.RoomLeft: {
        const payload = message.payload as { room_code: string };
        if (payload.room_code !== state.roomCode) break;
        get().leaveLocalRoom();
        break;
      }
      case MsgType.StatsResult:
        set({ stats: message.payload as StatsResultPayload, lobbyPanel: 'stats' });
        break;
      case MsgType.LeaderboardResult: {
        const payload = message.payload as { type: string; entries: LeaderboardEntry[] };
        set({ leaderboardType: payload.type, leaderboard: payload.entries ?? [], lobbyPanel: 'leaderboard' });
        break;
      }
      case MsgType.RoomListResult:
        set({ roomList: (message.payload as { rooms: RoomListItem[] }).rooms ?? [] });
        break;
      case MsgType.Chat: {
        const payload = message.payload as ChatPayload;
        useChatStore.getState().push(payload);
        break;
      }
      case MsgType.PlayerOffline: {
        const payload = message.payload as { player_id: string };
        set({ players: state.players.map((player) => player.id === payload.player_id ? { ...player, online: false } : player) });
        break;
      }
      case MsgType.PlayerOnline: {
        const payload = message.payload as { player_id: string };
        set({ players: state.players.map((player) => player.id === payload.player_id ? { ...player, online: true } : player) });
        break;
      }
      case MsgType.Maintenance:
      case MsgType.MaintenanceStatus:
        set({ maintenance: Boolean((message.payload as { maintenance: boolean }).maintenance) });
        break;
      default:
        break;
    }
  }
}));

const RECONNECT_STORAGE_KEY = 'ddz_next_reconnect';

export function loadReconnect(): { id: string; token: string } | null {
  try {
    const raw = localStorage.getItem(RECONNECT_STORAGE_KEY);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null || !('id' in parsed) || !('token' in parsed)) {
      localStorage.removeItem(RECONNECT_STORAGE_KEY);
      return null;
    }
    const { id, token } = parsed as { id?: unknown; token?: unknown };
    const valid = typeof id === 'string' && id
      && typeof token === 'string' && token;
    if (!valid) {
      localStorage.removeItem(RECONNECT_STORAGE_KEY);
      return null;
    }
    return { id, token };
  } catch {
    localStorage.removeItem(RECONNECT_STORAGE_KEY);
    return null;
  }
}

function persistReconnect(id: string, token: string): void {
  if (!id || !token) return;
  localStorage.setItem(RECONNECT_STORAGE_KEY, JSON.stringify({
    id,
    token
  }));
}

export function forgetReconnect(): void {
  localStorage.removeItem(RECONNECT_STORAGE_KEY);
}

function isReconnectFailure(code?: number, message?: string): boolean {
  return code === 1003 || code === 1004 || Boolean(message?.includes('重连令牌'));
}

function acceptProvisionalAfterReconnectFailure(
  set: (partial: Partial<AppState>) => void,
  state: AppState,
  reason: string
): void {
  const provisional = state.provisionalIdentity;
  if (!provisional) {
    set({
      connected: false,
      connectionStatus: 'offline',
      reconnectCandidate: null,
      error: reason
    });
    return;
  }

  const stored = loadReconnect();
  const attempted = state.reconnectCandidate;
  const ownsStoredAttempt = attempted !== null
    && stored?.id === attempted.id
    && stored.token === attempted.token;
  if (!stored || ownsStoredAttempt) {
    forgetReconnect();
    persistReconnect(provisional.id, provisional.token);
  }
  set({
    ...initialGameSlice,
    connected: true,
    connectionStatus: 'connected',
    playerId: provisional.id,
    playerName: provisional.name,
    reconnectToken: provisional.token,
    reconnectCandidate: null,
    provisionalIdentity: null,
    reconnectNotice: `无法恢复旧牌局，已作为新玩家连接：${reason}`,
    error: `无法恢复旧牌局，已作为新玩家连接：${reason}`,
    businessError: createBusinessError('validation', `无法恢复旧牌局，已作为新玩家连接：${reason}`),
    pendingCommands: {},
    phase: 'lobby',
    roomCode: '',
    players: [],
    selectedCards: new Set()
  });
}

function cancelCommandTimer(kind: CommandKind): void {
  const timer = commandTimers.get(kind);
  if (timer !== undefined) window.clearTimeout(timer);
  commandTimers.delete(kind);
}

function cancelAllCommandTimers(): void {
  for (const timer of commandTimers.values()) window.clearTimeout(timer);
  commandTimers.clear();
}

function createBusinessError(
  category: BusinessErrorCategory,
  message: string,
  retry?: CommandRequest,
  command?: CommandKind
): BusinessError {
  return { id: nextBusinessErrorID++, category, message, retry, command };
}

function classifyServerError(code?: number): BusinessErrorCategory {
  if (code === 1002) return 'rate-limit';
  if (code === 2003) return 'not-in-room';
  if (code === 5003) return 'maintenance';
  return 'validation';
}

function commandRequestID(message: IncomingMessage): string | undefined {
  return message.command?.request_id;
}

function findPendingCommand(
  pendingCommands: Partial<Record<CommandKind, PendingCommand>>,
  requestId: string | undefined,
  messageType?: WireMessageType | string
): { kind: CommandKind; pending: PendingCommand } | undefined {
  if (!requestId) return undefined;
  const expectedKind = commandKindForMessageType(messageType);
  for (const [kind, pending] of Object.entries(pendingCommands) as [CommandKind, PendingCommand | undefined][]) {
    if (!pending || pending.requestId !== requestId) continue;
    if (expectedKind && expectedKind !== kind) return undefined;
    return { kind, pending };
  }
  return undefined;
}

function commandKindForMessageType(messageType?: WireMessageType | string): CommandKind | undefined {
  switch (messageType) {
    case WireMessageType.MSG_CREATE_ROOM:
    case 'create_room': return 'create-room';
    case WireMessageType.MSG_JOIN_ROOM:
    case 'join_room': return 'join-room';
    case WireMessageType.MSG_QUICK_MATCH:
    case 'quick_match': return 'quick-match';
    case WireMessageType.MSG_PRACTICE_MATCH:
    case 'practice_match': return 'practice-match';
    case WireMessageType.MSG_CANCEL_MATCH:
    case 'cancel_match': return 'cancel-match';
    case WireMessageType.MSG_READY:
    case 'ready': return 'ready';
    case WireMessageType.MSG_CANCEL_READY:
    case 'cancel_ready': return 'cancel-ready';
    case WireMessageType.MSG_BID:
    case 'bid': return 'bid';
    case WireMessageType.MSG_PLAY_CARDS:
    case 'play_cards': return 'play';
    case WireMessageType.MSG_PASS:
    case 'pass': return 'pass';
    case WireMessageType.MSG_LEAVE_ROOM:
    case 'leave_room': return 'leave-room';
    case WireMessageType.MSG_CHAT:
    case 'chat': return 'chat';
    case WireMessageType.MSG_GET_STATS:
    case 'get_stats': return 'stats';
    case WireMessageType.MSG_GET_LEADERBOARD:
    case 'get_leaderboard': return 'leaderboard';
    case WireMessageType.MSG_GET_ROOM_LIST:
    case 'get_room_list': return 'room-list';
    default: return undefined;
  }
}

function commandLabel(kind: CommandKind): string {
  switch (kind) {
    case 'create-room': return '创建房间';
    case 'join-room': return '加入房间';
    case 'quick-match': return '快速匹配';
    case 'practice-match': return '人机练习';
    case 'cancel-match': return '取消匹配';
    case 'ready': return '准备';
    case 'cancel-ready': return '取消准备';
    case 'bid': return '叫地主';
    case 'play': return '出牌';
    case 'pass': return '不出';
    case 'leave-room': return '离开房间';
    case 'chat': return '发送聊天';
    case 'stats': return '获取战绩';
    case 'leaderboard': return '获取排行榜';
    case 'room-list': return '刷新房间列表';
  }
}
