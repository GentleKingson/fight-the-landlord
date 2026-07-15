import { MsgType, type CardInfo, type EventMeta, type GameStateDTO, type IncomingMessage, type PlayerHand, type PlayerScore, type UtilityDrawer } from '../../protocol/types';
import { cardKey, sortCards } from '../../shared/cards/cardModel';
import { appendPlayedCards, buildCardCounter, ledgerFromSnapshot, type PlayedCardLedger } from './cardCounter';
import { deadlineFromEvent, remainingSeconds } from './clock';
import { normalizeGamePlayers, syncDealtCounts, syncLandlordCounts, type RoomSlice } from './roomSlice';

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

export interface GameSlice {
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
  baseScore: number;
  isGrabTurn: boolean;
  timeout: number;
  timerStart: number;
  turnDeadlineMs: number;
  serverTimeMs: number;
  gameId: string;
  streamId: string;
  eventVersion: number;
  turnId: number;
  seenGameStreams: Record<string, number>;
  winnerId: string;
  winnerName: string;
  winnerIsLandlord: boolean;
  finalMultiplier: number;
  scores: PlayerScore[];
  playerHands: PlayerHand[];
  settlementSyncError: string;
  playedCardsLedger: PlayedCardLedger;
  cardCounter: Record<number, number>;
  recentActions: TableAction[];
  seatActions: Record<string, SeatAction>;
}

export const SEEN_GAME_STREAM_LIMIT = 64;

export class GameSnapshotSyncError extends Error {
  constructor(readonly snapshotPhase: string) {
    super(`游戏状态同步失败：服务器返回未知阶段 "${snapshotPhase || '(empty)'}"`);
    this.name = 'GameSnapshotSyncError';
  }
}

export const initialGameSlice: GameSlice = {
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
  baseScore: 1,
  isGrabTurn: false,
  timeout: 0,
  timerStart: 0,
  turnDeadlineMs: 0,
  serverTimeMs: 0,
  gameId: '',
  streamId: '',
  eventVersion: 0,
  turnId: 0,
  seenGameStreams: {},
  winnerId: '',
  winnerName: '',
  winnerIsLandlord: false,
  finalMultiplier: 1,
  scores: [],
  playerHands: [],
  settlementSyncError: '',
  playedCardsLedger: {},
  cardCounter: {},
  recentActions: [],
  seatActions: {}
};

export interface GameReducerState extends GameSlice, RoomSlice {
  playerId: string;
  selectedCards: Set<string>;
  drawer: UtilityDrawer;
}

export interface GameReducerResult {
  ignored: boolean;
  patch: Partial<GameSlice & RoomSlice & { selectedCards: Set<string>; drawer: UtilityDrawer }>;
  serverTimestamp?: number;
}

export function isGameMessage(message: IncomingMessage): boolean {
  return message.type === MsgType.GameStart
    || message.type === MsgType.DealCards
    || message.type === MsgType.BidTurn
    || message.type === MsgType.BidResult
    || message.type === MsgType.Landlord
    || message.type === MsgType.PlayTurn
    || message.type === MsgType.CardPlayed
    || message.type === MsgType.PlayerPass
    || message.type === MsgType.GameOver;
}

export function reduceGameMessage(
  state: GameReducerState,
  message: IncomingMessage,
  receivedAt: number,
  serverClockOffsetMs: number
): GameReducerResult | null {
  if (!isGameMessage(message)) return null;

  if (message.type === MsgType.GameStart) {
    const eventPatch = gateGameStart(state, message.event);
    if (!eventPatch) return { ignored: true, patch: {} };
    return {
      ignored: false,
      serverTimestamp: message.event?.server_time_ms,
      patch: {
        ...initialGameSlice,
        ...eventPatch,
        players: normalizeGamePlayers(message.payload.players ?? [], state.playerId),
        phase: 'bidding',
        selectedCards: new Set()
      }
    };
  }

  const gate = gateEvent(state, message.event);
  if (!gate.accepted) {
    return { ignored: true, patch: {} };
  }

  const eventPatch = gate.patch;
  const result = reduceAcceptedGameMessage(state, message, receivedAt, serverClockOffsetMs);
  if (!result) return null;
  return {
    ...result,
    patch: { ...eventPatch, ...result.patch },
    serverTimestamp: message.event?.server_time_ms
  };
}

function reduceAcceptedGameMessage(
  state: GameReducerState,
  message: IncomingMessage,
  receivedAt: number,
  serverClockOffsetMs: number
): Omit<GameReducerResult, 'serverTimestamp'> | null {
  switch (message.type) {
    case MsgType.DealCards: {
      const hand = sortCards(message.payload.cards ?? []);
      const bottomCards = message.payload.bottom_cards ?? [];
      const bottomCardsRevealed = state.bottomCardsRevealed;
      return accepted({
        hand,
        bottomCards,
        bottomCardsRevealed,
        players: syncDealtCounts(state.players, state.playerId, hand.length),
        cardCounter: buildCardCounter(hand, bottomCards, bottomCardsRevealed, state.playedCardsLedger)
      });
    }
    case MsgType.BidTurn: {
      const payload = message.payload;
      return accepted({
        phase: 'bidding',
        currentTurn: payload.player_id,
        timeout: payload.timeout ?? 0,
        isGrabTurn: payload.is_grab,
        multiplier: payload.multiplier ?? 1,
        timerStart: receivedAt,
        turnDeadlineMs: deadlineFromEvent(message.event, payload.timeout ?? 0, receivedAt, serverClockOffsetMs)
      });
    }
    case MsgType.BidResult: {
      const payload = message.payload;
      const action: SeatAction = {
        type: 'bid',
        player_id: payload.player_id,
        player_name: payload.player_name,
        label: payload.bid ? (payload.is_grab ? '抢地主' : '叫地主') : (payload.is_grab ? '不抢' : '不叫')
      };
      return accepted({
        multiplier: payload.multiplier ?? state.multiplier,
        recentActions: pushAction(state.recentActions, action),
        seatActions: setSeatAction(state.seatActions, action)
      });
    }
    case MsgType.Landlord: {
      const payload = message.payload;
      const isMe = payload.player_id === state.playerId;
      const bottomCards = payload.bottom_cards ?? [];
      const hand = isMe ? sortCards(mergeUniqueCards(state.hand, bottomCards)) : state.hand;
      return accepted({
        players: syncLandlordCounts(state.players, state.playerId, payload.player_id, hand.length),
        bottomCards,
        bottomCardsRevealed: true,
        multiplier: payload.multiplier ?? 1,
        hand,
        cardCounter: buildCardCounter(hand, bottomCards, true, state.playedCardsLedger),
        seatActions: {},
        recentActions: pushAction(state.recentActions, { type: 'system', label: `${payload.player_name} 成为地主` })
      });
    }
    case MsgType.PlayTurn: {
      const payload = message.payload;
      return accepted({
        phase: 'playing',
        currentTurn: payload.player_id,
        timeout: payload.timeout ?? 0,
        mustPlay: payload.must_play,
        canBeat: payload.can_beat,
        timerStart: receivedAt,
        turnDeadlineMs: deadlineFromEvent(message.event, payload.timeout ?? 0, receivedAt, serverClockOffsetMs)
      });
    }
    case MsgType.CardPlayed: {
      const payload = message.payload;
      const cards = payload.cards ?? [];
      const playedKeys = new Set(cards.map(cardKey));
      const isMe = payload.player_id === state.playerId;
      const hand = isMe ? state.hand.filter((card) => !playedKeys.has(cardKey(card))) : state.hand;
      const playedCardsLedger = appendPlayedCards(state.playedCardsLedger, payload.player_id, cards);
      const action: SeatAction = {
        type: 'play',
        player_id: payload.player_id,
        player_name: payload.player_name,
        cards,
        hand_type: payload.hand_type
      };
      return accepted({
        lastPlayed: cards,
        lastPlayedBy: payload.player_id,
        lastPlayedName: payload.player_name,
        lastHandType: payload.hand_type,
        hand,
        players: state.players.map((player) => player.id === payload.player_id
          ? { ...player, cards_count: payload.cards_left ?? 0 }
          : player),
        playedCardsLedger,
        cardCounter: buildCardCounter(hand, state.bottomCards, state.bottomCardsRevealed, playedCardsLedger),
        recentActions: pushAction(state.recentActions, action),
        seatActions: setSeatAction(state.mustPlay ? {} : state.seatActions, action),
        selectedCards: isMe ? new Set() : state.selectedCards
      });
    }
    case MsgType.PlayerPass: {
      const payload = message.payload;
      const action: SeatAction = {
        type: 'pass',
        player_id: payload.player_id,
        player_name: payload.player_name,
        label: '不出'
      };
      return accepted({
        // Passing belongs in the action lane, but it does not replace the
        // authoritative last non-pass play used for comparison and hints.
        recentActions: pushAction(state.recentActions, action),
        seatActions: setSeatAction(state.seatActions, action)
      });
    }
    case MsgType.GameOver: {
      const payload = message.payload;
      return accepted({
        phase: 'game_over',
        currentTurn: '',
        turnDeadlineMs: 0,
        timeout: 0,
        winnerId: payload.winner_id,
        winnerName: payload.winner_name,
        winnerIsLandlord: payload.is_landlord,
        finalMultiplier: payload.multiplier ?? state.multiplier,
        multiplier: payload.multiplier ?? state.multiplier,
        scores: payload.scores ?? [],
        playerHands: payload.player_hands ?? [],
        settlementSyncError: '',
        drawer: 'none'
      });
    }
    default:
      return null;
  }
}

export interface SnapshotRestoreContext {
  currentPlayerId: string;
  receivedAt: number;
  event?: EventMeta;
  seenGameStreams?: Record<string, number>;
}

export function shouldRestoreSnapshot(
  state: Pick<GameSlice, 'gameId' | 'streamId' | 'eventVersion' | 'seenGameStreams'>,
  dto: GameStateDTO,
  event?: EventMeta
): boolean {
  const incomingGameId = event?.game_id || dto.game_id || '';
  const incomingStreamId = event?.stream_id || (incomingGameId ? `game:${incomingGameId}` : '');
  const incomingVersion = event?.event_version ?? dto.snapshot_version ?? 0;
  const sameStream = Boolean(incomingStreamId && state.streamId === incomingStreamId);
  const sameGame = Boolean(incomingGameId && state.gameId === incomingGameId);
  if (incomingStreamId && incomingStreamId !== state.streamId && incomingStreamId in state.seenGameStreams) {
    return false;
  }
  return !(sameStream || sameGame) || incomingVersion > state.eventVersion;
}

export function restoreGameSnapshot(
  dto: GameStateDTO,
  context: SnapshotRestoreContext
): Partial<GameSlice & RoomSlice & { selectedCards: Set<string> }> {
  const phase = mapSnapshotPhase(dto.phase);
  const hand = sortCards(dto.hand ?? []);
  const players = normalizeGamePlayers(dto.players ?? [], context.currentPlayerId, hand.length);
  const bottomCards = dto.bottom_cards ?? [];
  const hasLandlord = players.some((player) => player.is_landlord);
  const bottomCardsRevealed = dto.bottom_cards_revealed
    ?? (phase === 'playing' || phase === 'game_over' || hasLandlord);
  const playedCardsLedger = ledgerFromSnapshot(dto.played_cards);
  const lastPlayed = dto.last_played ?? [];
  const lastPlayedBy = dto.last_player_id ?? '';
  const lastPlayedName = dto.last_player_name ?? '';
  const lastHandType = dto.last_hand_type ?? '';
  const seatActions: Record<string, SeatAction> = lastPlayed.length > 0 && lastPlayedBy
    ? {
        [lastPlayedBy]: {
          type: 'play',
          player_id: lastPlayedBy,
          player_name: lastPlayedName,
          cards: lastPlayed,
          hand_type: lastHandType
        }
      }
    : {};
  const gameId = context.event?.game_id || dto.game_id || '';
  const streamId = context.event?.stream_id || (gameId ? `game:${gameId}` : '');
  const eventVersion = context.event?.event_version ?? dto.snapshot_version ?? 0;
  const turnId = context.event?.turn_id ?? dto.turn_id ?? 0;
  const turnDeadlineMs = context.event?.turn_deadline_ms ?? dto.turn_deadline_ms ?? 0;
  const serverTimeMs = context.event?.server_time_ms ?? dto.server_time_ms ?? 0;
  const settlement = phase === 'game_over' && hasCompleteSettlement(dto)
    ? dto.settlement
    : undefined;
  const settlementPatch: Partial<GameSlice> = phase !== 'game_over'
    ? {}
    : settlement
      ? {
          winnerId: settlement.winner_id,
          winnerName: settlement.winner_name,
          winnerIsLandlord: settlement.winner_is_landlord,
          finalMultiplier: settlement.multiplier,
          multiplier: settlement.multiplier,
          scores: settlement.scores ?? [],
          playerHands: settlement.player_hands ?? [],
          settlementSyncError: ''
        }
      : {
          winnerId: '',
          winnerName: '',
          scores: [],
          playerHands: [],
          settlementSyncError: '结算数据缺失，请重新连接同步本局结果'
        };

  return {
    ...initialGameSlice,
    phase,
    players,
    hand,
    bottomCards,
    bottomCardsRevealed,
    currentTurn: dto.current_turn ?? '',
    lastPlayed,
    lastPlayedBy,
    lastPlayedName,
    lastHandType,
    seatActions,
    mustPlay: dto.must_play,
    canBeat: dto.can_beat,
    isGrabTurn: dto.is_grab ?? false,
    multiplier: dto.multiplier ?? 1,
    baseScore: dto.base_score ?? 1,
    gameId,
    streamId,
    eventVersion,
    turnId,
    seenGameStreams: rememberGameStream(context.seenGameStreams ?? {}, streamId, eventVersion),
    turnDeadlineMs,
    serverTimeMs,
    timeout: turnDeadlineMs && serverTimeMs
      ? remainingSeconds(turnDeadlineMs, 0, serverTimeMs)
      : 0,
    timerStart: context.receivedAt,
    playedCardsLedger,
    cardCounter: buildCardCounter(hand, bottomCards, bottomCardsRevealed, playedCardsLedger),
    recentActions: lastPlayed.length > 0
      ? [{ type: 'play', player_id: lastPlayedBy, player_name: lastPlayedName, cards: lastPlayed, hand_type: lastHandType, label: '上一手' }]
      : [],
    selectedCards: new Set(),
    ...settlementPatch
  };
}

function hasCompleteSettlement(dto: GameStateDTO): boolean {
  const settlement = dto.settlement;
  if (!settlement
    || !settlement.winner_id
    || !settlement.winner_name
    || !Number.isSafeInteger(settlement.multiplier)
    || settlement.multiplier <= 0) {
    return false;
  }

  const playerIds = new Set((dto.players ?? []).map((player) => player.id).filter(Boolean));
  if (playerIds.size === 0
    || settlement.scores.length !== playerIds.size
    || settlement.player_hands.length !== playerIds.size
    || !playerIds.has(settlement.winner_id)) {
    return false;
  }

  const scoreIds = new Set(settlement.scores.map((score) => score.player_id).filter(Boolean));
  const handIds = new Set(settlement.player_hands.map((hand) => hand.player_id).filter(Boolean));
  return scoreIds.size === playerIds.size
    && handIds.size === playerIds.size
    && [...playerIds].every((playerId) => scoreIds.has(playerId) && handIds.has(playerId));
}

export function mapSnapshotPhase(phase: string): RoomSlice['phase'] {
  switch (phase) {
    case 'waiting': return 'waiting';
    case 'bidding': return 'bidding';
    case 'playing': return 'playing';
    case 'ended':
    case 'game_over': return 'game_over';
    default: throw new GameSnapshotSyncError(phase);
  }
}

function gateEvent(
  state: Pick<GameSlice, 'streamId' | 'gameId' | 'eventVersion' | 'turnId' | 'seenGameStreams'>,
  event: EventMeta | undefined
): { accepted: true; patch: Partial<GameSlice> } | { accepted: false } {
  // Allow an entirely legacy stream only until a versioned stream has been
  // established. Once authoritative ordering is active, an unversioned frame
  // cannot safely be distinguished from a delayed event.
  if (!event) return state.streamId ? { accepted: false } : { accepted: true, patch: {} };

  if (!event.stream_id || event.event_version <= 0) return { accepted: false };
  if (state.streamId && event.stream_id !== state.streamId) return { accepted: false };
  if (state.gameId && event.game_id && event.game_id !== state.gameId) return { accepted: false };
  if (event.event_version <= state.eventVersion) return { accepted: false };
  if (event.turn_id < state.turnId) return { accepted: false };
  return { accepted: true, patch: eventWatermark(event, state.seenGameStreams) };
}

function gateGameStart(
  state: Pick<GameSlice, 'streamId' | 'seenGameStreams'>,
  event: EventMeta | undefined
): Partial<GameSlice> | null {
  if (!event) return state.streamId ? null : {};
  if (!event.stream_id || event.event_version <= 0) return null;
  if (state.streamId === event.stream_id) return null;
  if (event.stream_id in state.seenGameStreams) return null;
  return eventWatermark(event, state.seenGameStreams);
}

function eventWatermark(event: EventMeta, seenGameStreams: Record<string, number>): Partial<GameSlice> {
  return {
    streamId: event.stream_id,
    eventVersion: event.event_version,
    gameId: event.game_id,
    turnId: event.turn_id,
    seenGameStreams: rememberGameStream(seenGameStreams, event.stream_id, event.event_version),
    serverTimeMs: event.server_time_ms,
    turnDeadlineMs: event.turn_deadline_ms
  };
}

function rememberGameStream(
  seenGameStreams: Record<string, number>,
  streamId: string,
  eventVersion: number
): Record<string, number> {
  const entries = Object.entries(seenGameStreams)
    .filter(([seenStreamId]) => seenStreamId !== streamId);
  if (streamId) entries.push([streamId, eventVersion]);
  return Object.fromEntries(entries.slice(-SEEN_GAME_STREAM_LIMIT));
}

function accepted(patch: GameReducerResult['patch']): Omit<GameReducerResult, 'serverTimestamp'> {
  return { ignored: false, patch };
}

function pushAction(actions: TableAction[], action: TableAction): TableAction[] {
  return [...actions, action].slice(-10);
}

function setSeatAction(actions: Record<string, SeatAction>, action: SeatAction): Record<string, SeatAction> {
  return { ...actions, [action.player_id]: action };
}

function mergeUniqueCards(current: CardInfo[], incoming: CardInfo[]): CardInfo[] {
  const cards = new Map(current.map((card) => [cardKey(card), card]));
  for (const card of incoming) cards.set(cardKey(card), card);
  return [...cards.values()];
}
