import { turnTiming, observeClock } from '../transport/clock';
import { create } from 'zustand';
import { MsgType, type DealTiming, type CardInfo, type ChatPayload, type GameStateDTO, type IncomingMessage, type LeaderboardEntry, type LobbyPanel, type Phase, type PlayerHand, type PlayerInfo, type PlayerScore, type RoomListItem, type StatsResultPayload, type UtilityDrawer } from '../protocol/types';
import { cardKey, deductCounter, initialCounter, sortCards } from '../shared/cards/cardModel';
import { captureEndSnapshot, END_REVEAL_DURATION_MS, type EndSnapshot, type EndView } from './endGame';

interface ConnectionSlice {
  submission: "idle" | "pending" | "unknown" | "confirmed" | "rejected";
  submissionMessage: string;
  connected: boolean;
  playerId: string;
  playerName: string;
  reconnectToken: string;
  latency: number;
  error: string;
  maintenance: boolean;
}

interface LobbySlice {
  matchMode: 'practice' | 'online';
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
}

export interface DealStage { id: string; startAt: number; endAt: number }

interface TableSlice {
  roundId: string;
  endSnapshot: EndSnapshot | null;
  endView: EndView;
  revealDeadline: number | null;
  deal: DealStage | null;
  bidToken: string;
  turnToken: string;
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
  serverDeadline: number;
  serverTimestamp: number;
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
  setError: (error: string) => void;
  setLobbyPanel: (panel: LobbyPanel) => void;
  setRoomCodeInput: (value: string) => void;
  setChatInput: (value: string) => void;
  setDrawer: (drawer: UtilityDrawer) => void;
  toggleCard: (key: string) => void;
  setSelection: (keys: string[]) => void;
  clearSelection: () => void;
  leaveLocalRoom: () => void;
  showSettlement: () => void;
  reviewEndTable: () => void;
  handleMessage: (message: IncomingMessage) => void;
}

export type AppState = ConnectionSlice & LobbySlice & TableSlice & UiSlice & AppActions;

const initialTable: TableSlice = {
  roundId: '',
  endSnapshot: null,
  endView: 'reveal',
  revealDeadline: null,
  deal: null,
  bidToken: '',
  turnToken: '',
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
  timerStart: 0, serverDeadline: 0, serverTimestamp: 0,
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

export const useAppStore = create<AppState>((set, get) => ({
  submission: "idle", submissionMessage: "",
  connected: false,
  playerId: '',
  playerName: '',
  reconnectToken: '',
  latency: 0,
  error: '',
  maintenance: false,
  phase: 'connecting',
  matchMode: loadMatchMode(),
  roomCode: '',
  players: [],
  onlineCount: 0,
  lobbyPanel: 'home',
  roomCodeInput: '',
  stats: null,
  leaderboard: [],
  leaderboardType: 'total',
  roomList: [],
  ...initialTable,
  selectedCards: new Set<string>(),
  drawer: 'none',
  chatInput: '',
  tableMessage: '',

  setConnected: (connected) => set({ connected }),
  setError: (error) => set({ error }),
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
  setSelection: (keys) => set({ selectedCards: new Set(keys), tableMessage: '' }),
  clearSelection: () => set({ selectedCards: new Set(), tableMessage: '' }),
  leaveLocalRoom: () => set({ roomCode: '', players: [], phase: 'lobby', ...initialTable, selectedCards: new Set() }),
  showSettlement: () => {
    if (get().phase === 'game_over' && get().endSnapshot) set({ endView: 'result', revealDeadline: null });
  },
  reviewEndTable: () => {
    if (get().phase === 'game_over' && get().endSnapshot) set({ endView: 'review', revealDeadline: null });
  },
  handleMessage: (message) => {
    const state = get();
    switch (message.type) {
      case MsgType.Connected: {
        const payload = message.payload as { player_id: string; player_name: string; reconnect_token: string };
        const keepResult = state.phase === 'game_over' && state.endSnapshot && state.playerId === payload.player_id;
        set({
          ...(!keepResult ? { ...initialTable, players: [], roomCode: '', selectedCards: new Set<string>() } : {}),
          connected: true,
          error: '',
          phase: keepResult ? 'game_over' : 'lobby',
          playerId: payload.player_id,
          playerName: payload.player_name,
          reconnectToken: payload.reconnect_token
        });
        break;
      }
      case MsgType.Reconnected: {
        const payload = message.payload as { player_id: string; player_name: string; room_code?: string; game_state?: GameStateDTO };
        const snapshot = state.endSnapshot;
        const samePlayer = state.playerId === payload.player_id;
        const sameEndedRound = snapshot && payload.game_state?.phase === 'ended' && snapshot.roomCode === payload.room_code
          && (!payload.game_state.deal_id || !snapshot.roundId || snapshot.roundId === payload.game_state.deal_id);
        // The server may already have expired the room; a local snapshot remains readable.
        if (state.phase === 'game_over' && snapshot && samePlayer && (sameEndedRound || (!payload.game_state && !payload.room_code))) {
          set({ playerName: payload.player_name });
          break;
        }
        if (payload.game_state) {
          set({ playerId: payload.player_id, playerName: payload.player_name, roomCode: payload.room_code ?? '', ...restoreSnapshot(payload.game_state) });
        } else {
          set({ ...initialTable, players: [], playerId: payload.player_id, playerName: payload.player_name, roomCode: payload.room_code ?? '', phase: payload.room_code ? 'waiting' : 'lobby' });
        }
        break;
      }
      case MsgType.Pong: {
        const payload = message.payload as { client_timestamp: number; server_timestamp: number };
        observeClock(payload.client_timestamp, payload.server_timestamp);
        set({ latency: Date.now() - (payload.client_timestamp || Date.now()),
          ...(state.serverDeadline && ['bidding', 'playing'].includes(state.phase)
            ? turnTiming({ turn_deadline: state.serverDeadline, server_time: state.serverTimestamp }) : {}) });
        break;
      }
      case MsgType.Error: {
        const payload = message.payload as { message?: string };
        set({ error: payload.message || '未知错误', phase: state.phase === 'matching' ? 'lobby' : state.phase });
        break;
      }
      case MsgType.OnlineCount:
        set({ onlineCount: (message.payload as { count: number }).count });
        break;
      case MsgType.RoomCreated: {
        const payload = message.payload as { room_code: string; player: PlayerInfo };
        set({ ...initialTable, roomCode: payload.room_code, players: [normalizeLobbyPlayer(payload.player)], phase: 'waiting', lobbyPanel: 'home' });
        break;
      }
      case MsgType.RoomJoined: {
        const payload = message.payload as { room_code: string; players: PlayerInfo[] };
        set({ ...initialTable, roomCode: payload.room_code, players: normalizeLobbyPlayers(payload.players ?? []), phase: 'waiting', lobbyPanel: 'home' });
        break;
      }
      case MsgType.PlayerJoined: {
        const payload = message.payload as { player: PlayerInfo };
        set({ players: mergePlayer(state.players, normalizeLobbyPlayer(payload.player)) });
        break;
      }
      case MsgType.PlayerLeft: {
        const payload = message.payload as { player_id: string };
        if (state.phase === 'game_over' && state.endSnapshot) break;
        if (payload.player_id === state.playerId) get().leaveLocalRoom();
        else set({ players: state.players.filter((player) => player.id !== payload.player_id) });
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
      case MsgType.GameStart: {
        const payload = message.payload as { players: PlayerInfo[] };
        set({ ...initialTable, players: normalizeGamePlayers(payload.players ?? [], state.playerId), phase: 'dealing', selectedCards: new Set() });
        break;
      }
      case MsgType.DealCards: {
        const payload = message.payload as DealTiming & { cards: CardInfo[]; bottom_cards: CardInfo[] };
        const hand = sortCards(payload.cards ?? []);
        const deal = restoreDeal(payload);
        if (deal && state.deal?.id === deal.id) break;
        set({
          ...(deal ? { ...initialTable, deal, phase: 'dealing' as const, selectedCards: new Set<string>(), tableMessage: '' } : {}),
          roundId: payload.deal_id ?? state.roundId,
          hand,
          bottomCards: payload.bottom_cards ?? [],
          bottomCardsRevealed: hand.length === 20 && state.bottomCardsRevealed,
          seatActions: {},
          selectedCards: new Set(),
          recentActions: hand.length === 17 ? [] : state.recentActions,
          players: syncDealtCounts(deal ? state.players.map(player => ({ ...player, is_landlord: false, cards_count: 17 })) : state.players, state.playerId, hand.length),
          cardCounter: initialCounter(hand)
        });
        break;
      }
      case MsgType.BidTurn: {
        const payload = message.payload as { bid_token?: string; player_id: string; timeout: number; is_grab: boolean; multiplier: number };
        set({ deal: null, bidToken: payload.bid_token ?? '', phase: 'bidding', currentTurn: payload.player_id, isGrabTurn: payload.is_grab, multiplier: payload.multiplier || 1, ...turnTiming(payload), tableMessage: '' });
        break;
      }
      case MsgType.BidResult: {
        const payload = message.payload as { player_id: string; player_name: string; bid: boolean; is_grab: boolean; multiplier: number };
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
        const payload = message.payload as { turn_token?: string; player_id: string; timeout: number; must_play: boolean; can_beat: boolean };
        set({ deal: null, bidToken: '', turnToken: payload.turn_token ?? '', phase: 'playing', currentTurn: payload.player_id, mustPlay: payload.must_play, canBeat: payload.can_beat, ...turnTiming(payload), tableMessage: '' });
        break;
      }
      case MsgType.CardPlayed: {
        const payload = message.payload as { player_id: string; player_name: string; cards: CardInfo[]; cards_left: number; hand_type: string };
        const playedKeys = new Set((payload.cards ?? []).map(cardKey));
        const isMe = payload.player_id === state.playerId;
        const action = {
          type: 'play' as const,
          player_id: payload.player_id,
          player_name: payload.player_name,
          cards: payload.cards ?? [],
          hand_type: payload.hand_type
        };
        set({
          multiplier: ['炸弹', '王炸'].includes(payload.hand_type) ? state.multiplier * 2 : state.multiplier,
          lastPlayed: payload.cards ?? [],
          lastPlayedBy: payload.player_id,
          lastPlayedName: payload.player_name,
          lastHandType: payload.hand_type,
          hand: isMe ? state.hand.filter((card) => !playedKeys.has(cardKey(card))) : state.hand,
          players: state.players.map((player) => player.id === payload.player_id ? { ...player, cards_count: payload.cards_left } : player),
          cardCounter: isMe || !Object.keys(state.cardCounter).length ? state.cardCounter : deductCounter(state.cardCounter, payload.cards ?? []),
          recentActions: pushAction(state.recentActions, action),
          seatActions: setSeatAction(state.mustPlay ? {} : state.seatActions, action),
          selectedCards: new Set()
        });
        break;
      }
      case MsgType.PlayerPass: {
        const payload = message.payload as { player_id: string; player_name: string };
        const action = { type: 'pass' as const, player_id: payload.player_id, player_name: payload.player_name, label: '不出' };
        set({
          recentActions: pushAction(state.recentActions, action),
          seatActions: setSeatAction(state.seatActions, action)
        });
        break;
      }
      case MsgType.GameOver: {
        if (!['dealing', 'bidding', 'playing', 'game_over'].includes(state.phase)) break;
        if (state.endSnapshot) break; // A replay must not restart the reveal or interrupt review.
        const payload = message.payload as { winner_id: string; winner_name: string; is_landlord: boolean; multiplier: number; scores: PlayerScore[]; player_hands: PlayerHand[] };
        const endSnapshot = captureEndSnapshot(state, payload);
        set({ deal: null, bidToken: '', turnToken: '', phase: 'game_over', winnerId: endSnapshot.winnerId, winnerName: endSnapshot.winnerName, winnerIsLandlord: endSnapshot.winnerIsLandlord, finalMultiplier: endSnapshot.finalMultiplier, scores: endSnapshot.scores, playerHands: endSnapshot.playerHands, drawer: 'none',
          endSnapshot, endView: 'reveal', revealDeadline: Date.now() + END_REVEAL_DURATION_MS,
          currentTurn: '', timeout: 0, timerStart: 0, mustPlay: false, canBeat: false, selectedCards: new Set(), tableMessage: '' });
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
      case MsgType.Chat:
        useChatStore.getState().push(message.payload as ChatPayload);
        break;
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
      case MsgType.MaintenancePull:
      case MsgType.MaintenancePush:
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

export function loadMatchMode(): 'practice' | 'online' {
  try { return sessionStorage.getItem('ddz_match_mode') === 'practice' ? 'practice' : 'online'; }
  catch { return 'online'; }
}

useAppStore.subscribe((state, previous) => {
  if (state.matchMode !== previous.matchMode) {
    try { sessionStorage.setItem('ddz_match_mode', state.matchMode); } catch { /* Storage may be disabled. */ }
  }
});

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
    cards_count: player.id === currentPlayerId ? handCount : (player.cards_count || 17)
  }));
}

function syncLandlordCounts(players: PlayerInfo[], currentPlayerId: string, landlordId: string, currentHandCount: number): PlayerInfo[] {
  return players.map((player) => {
    const isLandlord = player.id === landlordId;
    const fallbackCount = isLandlord ? 20 : 17;
    return {
      ...player,
      is_landlord: isLandlord,
      cards_count: player.id === currentPlayerId ? currentHandCount : fallbackCount
    };
  });
}

function restoreSnapshot(dto: GameStateDTO): Partial<AppState> {
  const isPlaying = dto.phase === 'playing' || dto.phase === 'ended';
  const hasLandlord = (dto.players ?? []).some((player) => player.is_landlord);
  const hand = sortCards(dto.hand ?? []);
  let seatActions: Record<string, SeatAction> = dto.last_played?.length && dto.last_player_id
    ? { [dto.last_player_id]: { type: 'play' as const, player_id: dto.last_player_id, cards: dto.last_played } }
    : {};
  const plays = dto.public_plays ?? [];
  for (const play of plays) {
    if (play.new_trick) seatActions = {};
    seatActions = setSeatAction(seatActions, { ...play, label: play.type === 'pass' ? '不出' : undefined });
  }
  // Current hand + every public play reconstructs all cards no longer held by opponents.
  const cardCounter = deductCounter(initialCounter(hand), plays.flatMap(play => play.cards ?? []));
  return {
    ...initialTable,
    roundId: dto.deal_id ?? '',
    multiplier: dto.multiplier || 1,
    ...turnTiming({ ...dto, timeout: 0, turn_deadline: dto.turn_paused ? 0 : dto.turn_deadline }),
    deal: dto.phase === 'dealing' ? restoreDeal(dto) : null,
    selectedCards: new Set(),
    phase: dto.phase === 'dealing' ? 'dealing' : dto.phase === 'bidding' ? 'bidding' : dto.phase === 'playing' ? 'playing' : dto.phase === 'ended' ? 'game_over' : 'lobby',
    players: [...(dto.players ?? [])].sort((a, b) => a.seat - b.seat),
    hand,
    bottomCards: dto.bottom_cards ?? [],
    bottomCardsRevealed: isPlaying || hasLandlord,
    currentTurn: dto.current_turn ?? '',
    turnToken: dto.turn_token ?? '',
    lastPlayed: dto.last_played ?? [],
    lastPlayedBy: dto.last_player_id ?? '',
    lastPlayedName: dto.last_played?.length
      ? dto.players?.find((player) => player.id === dto.last_player_id)?.name ?? ''
      : '',
    lastHandType: dto.last_hand_type ?? '',
    seatActions,
    mustPlay: dto.must_play,
    canBeat: dto.can_beat,
    cardCounter: dto.public_plays !== undefined || !isPlaying ? cardCounter : {},
    recentActions: plays.slice(-10).map(play => ({ ...play, label: play.type === 'pass' ? '不出' : undefined }))
  };
}

// Translate server times once on receipt; wall-clock differences do not restart deals.
export function restoreDeal(timing: DealTiming): DealStage | null {
  if (!timing.deal_id || !timing.deal_start_at || !timing.deal_end_at || !timing.server_time) return null;
  const offset = Date.now() - timing.server_time;
  return { id: timing.deal_id, startAt: timing.deal_start_at + offset, endAt: timing.deal_end_at + offset };
}

function pushAction(actions: TableAction[], action: TableAction): TableAction[] {
  return [...actions, action].slice(-10);
}

function setSeatAction(actions: Record<string, SeatAction>, action: SeatAction): Record<string, SeatAction> {
  return { ...actions, [action.player_id]: action };
}
