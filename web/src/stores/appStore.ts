import { create } from 'zustand';
import { MsgType, WireMessageType, type CardInfo, type ChatPayload, type GameStateDTO, type IncomingMessage, type LeaderboardEntry, type LobbyPanel, type Phase, type PlayerHand, type PlayerInfo, type PlayerScore, type RoomListItem, type StatsResultPayload, type UtilityDrawer } from '../protocol/types';
import { cardKey, deductCounter, initialCounter, sortCards } from '../shared/cards/cardModel';

export type ConnectionStatus = 'idle' | 'connecting' | 'fresh-connected' | 'reconnecting' | 'reconnected' | 'connected' | 'closing' | 'offline';

export type CommandKind =
  | 'create-room'
  | 'join-room'
  | 'quick-match'
  | 'practice-match'
  | 'cancel-match'
  | 'ready'
  | 'cancel-ready'
  | 'bid'
  | 'play'
  | 'pass'
  | 'leave-room'
  | 'chat';

export type CommandRequest =
  | { kind: 'create-room' }
  | { kind: 'join-room'; roomCode: string }
  | { kind: 'quick-match' }
  | { kind: 'practice-match' }
  | { kind: 'cancel-match' }
  | { kind: 'ready' }
  | { kind: 'cancel-ready' }
  | { kind: 'bid'; bid: boolean }
  | { kind: 'play'; cards: CardInfo[] }
  | { kind: 'pass' }
  | { kind: 'leave-room' }
  | { kind: 'chat'; content: string; scope: string; messageId: string };

export type BusinessErrorCategory = 'validation' | 'rate-limit' | 'maintenance' | 'not-in-room' | 'timeout' | 'network';

export interface PendingCommand {
  kind: CommandKind;
  request: CommandRequest;
  startedAt: number;
  timeoutAt: number;
  retryable: boolean;
}

export interface BusinessError {
  id: number;
  category: BusinessErrorCategory;
  message: string;
  command?: CommandKind;
  retry?: CommandRequest;
}

export interface StoredIdentity {
  id: string;
  token: string;
}

interface ProvisionalIdentity extends StoredIdentity {
  name: string;
}

interface ConnectionSlice {
  connected: boolean;
  connectionStatus: ConnectionStatus;
  playerId: string;
  playerName: string;
  reconnectToken: string;
  reconnectCandidate: StoredIdentity | null;
  provisionalIdentity: ProvisionalIdentity | null;
  reconnectNotice: string;
  latency: number;
  error: string;
  maintenance: boolean;
}

interface LobbySlice {
  phase: Phase;
  roomCode: string;
  players: PlayerInfo[];
  onlineCount: number;
  lobbyPanel: LobbyPanel;
  roomCodeInput: string;
  stats: StatsResultPayload | null;
  leaderboard: LeaderboardEntry[];
  leaderboardType: string;
  roomList: RoomListItem[];
  matchDeadlineMs: number;
  matchPractice: boolean;
}

interface TableSlice {
  hand: CardInfo[];
  bottomCards: CardInfo[];
  bottomCardsRevealed: boolean;
  currentTurn: string;
  lastPlayed: CardInfo[];
  lastPlayedBy: string;
  lastPlayedName: string;
  lastHandType: string;
  mustPlay: boolean;
  canBeat: boolean;
  multiplier: number;
  isGrabTurn: boolean;
  timeout: number;
  timerStart: number;
  winnerId: string;
  winnerName: string;
  winnerIsLandlord: boolean;
  finalMultiplier: number;
  scores: PlayerScore[];
  playerHands: PlayerHand[];
  cardCounter: Record<number, number>;
  recentActions: TableAction[];
  seatActions: Record<string, SeatAction>;
}

interface UiSlice {
  selectedCards: Set<string>;
  drawer: UtilityDrawer;
  chatInput: string;
  tableMessage: string;
  pendingCommands: Partial<Record<CommandKind, PendingCommand>>;
  businessError: BusinessError | null;
}

export interface TableAction {
  type: 'play' | 'pass' | 'bid' | 'system';
  player_id?: string;
  player_name?: string;
  cards?: CardInfo[];
  hand_type?: string;
  label?: string;
}

export interface SeatAction {
  type: 'play' | 'pass' | 'bid';
  player_id: string;
  player_name?: string;
  cards?: CardInfo[];
  hand_type?: string;
  label?: string;
}

interface AppActions {
  setConnected: (connected: boolean) => void;
  prepareConnection: (identity: StoredIdentity | null) => void;
  setConnectionStatus: (status: ConnectionStatus) => void;
  clearIdentity: () => void;
  setError: (error: string) => void;
  setBusinessError: (category: BusinessErrorCategory, message: string, retry?: CommandRequest, command?: CommandKind) => void;
  clearBusinessError: () => void;
  beginCommand: (request: CommandRequest, timeoutMs: number, retryable: boolean) => void;
  finishCommand: (kind: CommandKind) => void;
  finishCommands: (kinds: CommandKind[]) => void;
  clearPendingCommands: () => void;
  setLobbyPanel: (panel: LobbyPanel) => void;
  setRoomCodeInput: (value: string) => void;
  setChatInput: (value: string) => void;
  setDrawer: (drawer: UtilityDrawer) => void;
  toggleCard: (key: string) => void;
  setSelection: (keys: string[]) => void;
  clearSelection: () => void;
  leaveLocalRoom: () => void;
  handleMessage: (message: IncomingMessage) => void;
}

export type AppState = ConnectionSlice & LobbySlice & TableSlice & UiSlice & AppActions;

const initialTable: TableSlice = {
  hand: [],
  bottomCards: [],
  bottomCardsRevealed: false,
  currentTurn: '',
  lastPlayed: [],
  lastPlayedBy: '',
  lastPlayedName: '',
  lastHandType: '',
  mustPlay: false,
  canBeat: false,
  multiplier: 1,
  isGrabTurn: false,
  timeout: 0,
  timerStart: 0,
  winnerId: '',
  winnerName: '',
  winnerIsLandlord: false,
  finalMultiplier: 1,
  scores: [],
  playerHands: [],
  cardCounter: {},
  recentActions: [],
  seatActions: {}
};

const commandTimers = new Map<CommandKind, number>();
let nextBusinessErrorID = 1;

export const useAppStore = create<AppState>((set, get) => ({
  connected: false,
  connectionStatus: 'idle',
  playerId: '',
  playerName: '',
  reconnectToken: '',
  reconnectCandidate: null,
  provisionalIdentity: null,
  reconnectNotice: '',
  latency: 0,
  error: '',
  maintenance: false,
  phase: 'connecting',
  roomCode: '',
  players: [],
  onlineCount: 0,
  lobbyPanel: 'home',
  roomCodeInput: '',
  stats: null,
  leaderboard: [],
  leaderboardType: 'total',
  roomList: [],
  matchDeadlineMs: 0,
  matchPractice: false,
  ...initialTable,
  selectedCards: new Set<string>(),
  drawer: 'none',
  chatInput: '',
  tableMessage: '',
  pendingCommands: {},
  businessError: null,

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
  beginCommand: (request, timeoutMs, retryable) => {
    const kind = request.kind;
    const startedAt = Date.now();
    const pending: PendingCommand = {
      kind,
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
      if (!current || current.startedAt !== startedAt) return;
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
  finishCommand: (kind) => {
    cancelCommandTimer(kind);
    const pendingCommands = { ...get().pendingCommands };
    delete pendingCommands[kind];
    set({ pendingCommands });
  },
  finishCommands: (kinds) => {
    const pendingCommands = { ...get().pendingCommands };
    for (const kind of kinds) {
      cancelCommandTimer(kind);
      delete pendingCommands[kind];
    }
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
    ...initialTable,
    selectedCards: new Set()
  }),
  handleMessage: (message) => {
    const state = get();
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
          set({ ...connectionState, playerId: payload.player_id, playerName: payload.player_name, roomCode: payload.room_code ?? '', ...restoreSnapshot(payload.game_state, payload.player_id) });
        } else {
          set({ ...connectionState, playerId: payload.player_id, playerName: payload.player_name, roomCode: payload.room_code ?? '', phase: payload.room_code ? 'waiting' : 'lobby' });
        }
        break;
      }
      case MsgType.Pong: {
        const payload = message.payload as { client_timestamp: number };
        set({ latency: Date.now() - (payload.client_timestamp || Date.now()) });
        break;
      }
      case MsgType.Error: {
        const payload = message.payload as { code?: number; message?: string; command_type?: WireMessageType };
        if (state.connectionStatus === 'reconnecting' && isReconnectFailure(payload.code, payload.message)) {
          acceptProvisionalAfterReconnectFailure(set, state, payload.message || '旧会话已失效');
          break;
        }
        const command = commandKindForMessageType(payload.command_type);
        const pending = command ? state.pendingCommands[command] : undefined;
        if (command) get().finishCommand(command);
        else get().clearPendingCommands();
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
      case MsgType.OnlineCount:
        set({ onlineCount: (message.payload as { count: number }).count });
        break;
      case MsgType.RoomCreated: {
        const payload = message.payload as { room_code: string; player: PlayerInfo };
        get().finishCommand('create-room');
        set({ roomCode: payload.room_code, players: [normalizeLobbyPlayer(payload.player)], phase: 'waiting', lobbyPanel: 'home' });
        break;
      }
      case MsgType.RoomJoined: {
        const payload = message.payload as { room_code: string; players: PlayerInfo[] };
        get().finishCommands(['join-room', 'quick-match', 'practice-match']);
        set({
          roomCode: payload.room_code,
          players: normalizeLobbyPlayers(payload.players ?? []),
          phase: 'waiting',
          lobbyPanel: 'home',
          matchDeadlineMs: 0,
          matchPractice: false
        });
        break;
      }
      case MsgType.PlayerJoined: {
        const payload = message.payload as { player: PlayerInfo };
        set({ players: mergePlayer(state.players, normalizeLobbyPlayer(payload.player)) });
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
        if (payload.player_id === state.playerId) get().finishCommand(payload.ready ? 'ready' : 'cancel-ready');
        set({ players: state.players.map((player) => player.id === payload.player_id ? { ...player, ready: payload.ready } : player) });
        break;
      }
      case MsgType.MatchFound:
        get().finishCommands(['quick-match', 'practice-match']);
        set({ phase: 'waiting' });
        break;
      case MsgType.MatchQueued: {
        const payload = message.payload as { deadline_ms: number; practice: boolean };
        get().finishCommand(payload.practice ? 'practice-match' : 'quick-match');
        set({
          phase: 'matching',
          matchDeadlineMs: payload.deadline_ms,
          matchPractice: payload.practice
        });
        break;
      }
      case MsgType.MatchCancelled: {
        const payload = message.payload as { reason: string };
        get().finishCommands(['quick-match', 'practice-match', 'cancel-match']);
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
      case MsgType.RoomLeft:
        get().finishCommand('leave-room');
        get().leaveLocalRoom();
        break;
      case MsgType.GameStart: {
        const payload = message.payload as { players: PlayerInfo[] };
        get().finishCommands(['quick-match', 'practice-match', 'ready', 'cancel-ready']);
        set({
          ...initialTable,
          players: normalizeGamePlayers(payload.players ?? [], state.playerId),
          phase: 'bidding',
          matchDeadlineMs: 0,
          matchPractice: false,
          selectedCards: new Set()
        });
        break;
      }
      case MsgType.DealCards: {
        const payload = message.payload as { cards: CardInfo[]; bottom_cards: CardInfo[] };
        const hand = sortCards(payload.cards ?? []);
        set({
          hand,
          bottomCards: payload.bottom_cards ?? [],
          bottomCardsRevealed: false,
          seatActions: {},
          players: syncDealtCounts(state.players, state.playerId, hand.length),
          cardCounter: initialCounter(hand)
        });
        break;
      }
      case MsgType.BidTurn: {
        const payload = message.payload as { player_id: string; timeout: number; is_grab: boolean; multiplier: number };
        set({ phase: 'bidding', currentTurn: payload.player_id, timeout: payload.timeout, isGrabTurn: payload.is_grab, multiplier: payload.multiplier || 1, timerStart: Date.now(), tableMessage: '' });
        break;
      }
      case MsgType.BidResult: {
        const payload = message.payload as { player_id: string; player_name: string; bid: boolean; is_grab: boolean; multiplier: number };
        if (payload.player_id === state.playerId) get().finishCommand('bid');
        const seatAction = {
          type: 'bid' as const,
          player_id: payload.player_id,
          player_name: payload.player_name,
          label: payload.bid ? (payload.is_grab ? '抢地主' : '叫地主') : (payload.is_grab ? '不抢' : '不叫')
        };
        set({ seatActions: setSeatAction(state.seatActions, seatAction) });
        set({ multiplier: payload.multiplier || state.multiplier, recentActions: pushAction(state.recentActions, { type: 'bid', player_id: payload.player_id, player_name: payload.player_name, label: payload.bid ? (payload.is_grab ? '抢地主' : '叫地主') : (payload.is_grab ? '不抢' : '不叫') }) });
        break;
      }
      case MsgType.Landlord: {
        const payload = message.payload as { player_id: string; player_name: string; bottom_cards: CardInfo[]; multiplier: number };
        const isMe = payload.player_id === state.playerId;
        const nextHand = isMe ? sortCards([...state.hand, ...(payload.bottom_cards ?? [])]) : state.hand;
        set({
          players: syncLandlordCounts(state.players, state.playerId, payload.player_id, nextHand.length),
          bottomCards: payload.bottom_cards ?? [],
          bottomCardsRevealed: true,
          multiplier: payload.multiplier || 1,
          hand: nextHand,
          seatActions: {},
          recentActions: pushAction(state.recentActions, { type: 'system', label: `${payload.player_name} 成为地主` })
        });
        break;
      }
      case MsgType.PlayTurn: {
        const payload = message.payload as { player_id: string; timeout: number; must_play: boolean; can_beat: boolean };
        set({ phase: 'playing', currentTurn: payload.player_id, timeout: payload.timeout, mustPlay: payload.must_play, canBeat: payload.can_beat, timerStart: Date.now(), tableMessage: '' });
        break;
      }
      case MsgType.CardPlayed: {
        const payload = message.payload as { player_id: string; player_name: string; cards: CardInfo[]; cards_left: number; hand_type: string };
        const playedKeys = new Set((payload.cards ?? []).map(cardKey));
        const isMe = payload.player_id === state.playerId;
        if (isMe) get().finishCommand('play');
        const action = {
          type: 'play' as const,
          player_id: payload.player_id,
          player_name: payload.player_name,
          cards: payload.cards ?? [],
          hand_type: payload.hand_type
        };
        set({
          lastPlayed: payload.cards ?? [],
          lastPlayedBy: payload.player_id,
          lastPlayedName: payload.player_name,
          lastHandType: payload.hand_type,
          hand: isMe ? state.hand.filter((card) => !playedKeys.has(cardKey(card))) : state.hand,
          players: state.players.map((player) => player.id === payload.player_id ? { ...player, cards_count: payload.cards_left } : player),
          cardCounter: isMe ? state.cardCounter : deductCounter(state.cardCounter, payload.cards ?? []),
          recentActions: pushAction(state.recentActions, action),
          seatActions: setSeatAction(state.mustPlay ? {} : state.seatActions, action),
          selectedCards: isMe ? new Set() : state.selectedCards
        });
        break;
      }
      case MsgType.PlayerPass: {
        const payload = message.payload as { player_id: string; player_name: string };
        if (payload.player_id === state.playerId) get().finishCommand('pass');
        const action = { type: 'pass' as const, player_id: payload.player_id, player_name: payload.player_name, label: '不出' };
        set({
          lastPlayedBy: payload.player_id,
          lastPlayedName: payload.player_name,
          recentActions: pushAction(state.recentActions, action),
          seatActions: setSeatAction(state.seatActions, action)
        });
        break;
      }
      case MsgType.GameOver: {
        const payload = message.payload as { winner_id: string; winner_name: string; is_landlord: boolean; multiplier: number; scores: PlayerScore[]; player_hands: PlayerHand[] };
        get().finishCommands(['bid', 'play', 'pass']);
        set({ phase: 'game_over', winnerId: payload.winner_id, winnerName: payload.winner_name, winnerIsLandlord: payload.is_landlord, finalMultiplier: payload.multiplier, scores: payload.scores ?? [], playerHands: payload.player_hands ?? [], drawer: 'none' });
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
        const pendingChat = state.pendingCommands.chat;
        if (pendingChat?.request.kind === 'chat' && payload.message_id === pendingChat.request.messageId) {
          get().finishCommand('chat');
        }
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

export const useChatStore = create<{ messages: ChatPayload[]; push: (message: ChatPayload) => void; clear: () => void }>((set, get) => ({
  messages: [],
  push: (message) => set({ messages: [...get().messages, message].slice(-80) }),
  clear: () => set({ messages: [] })
}));

export function loadReconnect(): { id: string; token: string } | null {
  try {
    const raw = localStorage.getItem('ddz_next_reconnect');
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null || !('id' in parsed) || !('token' in parsed)) return null;
    const { id, token } = parsed as { id?: unknown; token?: unknown };
    return typeof id === 'string' && id && typeof token === 'string' && token ? { id, token } : null;
  } catch {
    return null;
  }
}

function persistReconnect(id: string, token: string): void {
  if (!id || !token) return;
  localStorage.setItem('ddz_next_reconnect', JSON.stringify({ id, token }));
}

export function forgetReconnect(): void {
  localStorage.removeItem('ddz_next_reconnect');
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
    ...initialTable,
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
  }
}

function mergePlayer(players: PlayerInfo[], next: PlayerInfo): PlayerInfo[] {
  return players.some((player) => player.id === next.id)
    ? players.map((player) => player.id === next.id ? next : player)
    : [...players, next].sort((a, b) => a.seat - b.seat);
}

function normalizeLobbyPlayer(player: PlayerInfo): PlayerInfo {
  return { ...player, online: true };
}

function normalizeLobbyPlayers(players: PlayerInfo[]): PlayerInfo[] {
  return players.map(normalizeLobbyPlayer).sort((a, b) => a.seat - b.seat);
}

function normalizeGamePlayers(players: PlayerInfo[], currentPlayerId: string, currentHandCount = 17): PlayerInfo[] {
  return players
    .map((player) => ({
      ...player,
      online: true,
      cards_count: player.cards_count || (player.id === currentPlayerId ? currentHandCount : 17)
    }))
    .sort((a, b) => a.seat - b.seat);
}

function syncDealtCounts(players: PlayerInfo[], currentPlayerId: string, handCount: number): PlayerInfo[] {
  return players.map((player) => ({
    ...player,
    online: true,
    cards_count: player.id === currentPlayerId ? handCount : (player.cards_count || 17)
  }));
}

function syncLandlordCounts(players: PlayerInfo[], currentPlayerId: string, landlordId: string, currentHandCount: number): PlayerInfo[] {
  return players.map((player) => {
    const isLandlord = player.id === landlordId;
    const fallbackCount = isLandlord ? 20 : 17;
    return {
      ...player,
      online: true,
      is_landlord: isLandlord,
      cards_count: player.id === currentPlayerId ? currentHandCount : (player.cards_count || fallbackCount)
    };
  });
}

function restoreSnapshot(dto: GameStateDTO, currentPlayerId: string): Partial<AppState> {
  const isPlaying = dto.phase !== 'bidding';
  const hasLandlord = (dto.players ?? []).some((player) => player.is_landlord);
  const hand = sortCards(dto.hand ?? []);
  const seatActions = dto.last_played?.length && dto.last_player_id
    ? { [dto.last_player_id]: { type: 'play' as const, player_id: dto.last_player_id, cards: dto.last_played } }
    : {};
  return {
    phase: dto.phase === 'bidding' ? 'bidding' : 'playing',
    players: normalizeGamePlayers(dto.players ?? [], currentPlayerId, hand.length || 17),
    hand,
    bottomCards: dto.bottom_cards ?? [],
    bottomCardsRevealed: isPlaying || hasLandlord,
    currentTurn: dto.current_turn ?? '',
    lastPlayed: dto.last_played ?? [],
    lastPlayedBy: dto.last_player_id ?? '',
    seatActions,
    mustPlay: dto.must_play,
    canBeat: dto.can_beat,
    cardCounter: initialCounter(dto.hand ?? []),
    recentActions: dto.last_played?.length ? [{ type: 'play', player_id: dto.last_player_id, cards: dto.last_played, label: '上一手' }] : []
  };
}

function pushAction(actions: TableAction[], action: TableAction): TableAction[] {
  return [...actions, action].slice(-10);
}

function setSeatAction(actions: Record<string, SeatAction>, action: SeatAction): Record<string, SeatAction> {
  return { ...actions, [action.player_id]: action };
}
