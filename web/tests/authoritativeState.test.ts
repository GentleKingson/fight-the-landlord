import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MsgType, type CardInfo, type EventMeta, type GameStateDTO, type IncomingMessage, type PlayerInfo } from '../src/protocol/types';
import { useAppStore } from '../src/stores/appStore';
import { buildCardCounter, ledgerFromSnapshot } from '../src/stores/slices/cardCounter';
import { initialServerClock, observePong, remainingSeconds } from '../src/stores/slices/clock';
import { initialConnectionSlice } from '../src/stores/slices/connectionSlice';
import { SEEN_GAME_STREAM_LIMIT, initialGameSlice, reduceGameMessage, restoreGameSnapshot } from '../src/stores/slices/gameSlice';
import { initialLobbySlice } from '../src/stores/slices/lobbySlice';
import { initialRoomSlice } from '../src/stores/slices/roomSlice';
import { initialUiSlice } from '../src/stores/slices/uiSlice';

const players: PlayerInfo[] = [
  { id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: true, cards_count: 0, online: false },
  { id: 'p2', name: '山月', seat: 1, ready: true, is_landlord: false, cards_count: 12, online: true },
  { id: 'p3', name: '松风', seat: 2, ready: true, is_landlord: false, cards_count: 11, online: true }
];

beforeEach(() => {
  vi.useRealTimers();
  localStorage.clear();
  useAppStore.getState().clearPendingCommands();
  useAppStore.setState({
    ...initialConnectionSlice,
    ...initialLobbySlice,
    ...initialRoomSlice,
    ...initialGameSlice,
    ...initialUiSlice
  });
});

describe('authoritative snapshot restoration', () => {
  it.each([
    ['waiting', 'waiting'],
    ['bidding', 'bidding'],
    ['playing', 'playing'],
    ['ended', 'game_over']
  ] as const)('maps the server %s phase explicitly to %s', (serverPhase, clientPhase) => {
    const restored = restoreGameSnapshot(snapshot({ phase: serverPhase }), {
      currentPlayerId: 'p1',
      receivedAt: 9_000
    });
    expect(restored.phase).toBe(clientPhase);
  });

  it('restores every DTO field while preserving false and zero values', () => {
    const dto = snapshot({
      phase: 'playing',
      players,
      bottom_cards_revealed: false,
      current_turn: 'p2',
      turn_id: 0,
      turn_deadline_ms: 11_000,
      server_time_ms: 9_000,
      last_player_id: 'p3',
      last_player_name: '松风',
      last_hand_type: '对子',
      must_play: false,
      can_beat: false,
      is_grab: false,
      multiplier: 0,
      base_score: 0
    });

    const restored = restoreGameSnapshot(dto, { currentPlayerId: 'p1', receivedAt: 9_100 });

    expect(restored).toMatchObject({
      phase: 'playing',
      currentTurn: 'p2',
      turnId: 0,
      turnDeadlineMs: 11_000,
      serverTimeMs: 9_000,
      lastPlayedBy: 'p3',
      lastPlayedName: '松风',
      lastHandType: '对子',
      mustPlay: false,
      canBeat: false,
      isGrabTurn: false,
      multiplier: 0,
      baseScore: 0,
      bottomCardsRevealed: false
    });
    expect(restored.players?.[0]).toMatchObject({ cards_count: 0, online: false });
  });

  it('does not apply the same reconnect snapshot twice', () => {
    const dto = snapshot({ snapshot_version: 7, game_id: 'g1' });
    const reconnect = {
      type: MsgType.Reconnected,
      payload: {
        player_id: 'p1',
        player_name: '青竹',
        room_code: '123456',
        reconnect_token: 'rotated',
        game_state: dto
      },
      event: event('g1', 7, 4)
    } as const;

    useAppStore.getState().handleMessage(reconnect);
    const firstLedger = useAppStore.getState().playedCardsLedger;
    const firstActions = useAppStore.getState().recentActions;
    useAppStore.getState().handleMessage(reconnect);

    expect(useAppStore.getState().eventVersion).toBe(7);
    expect(useAppStore.getState().playedCardsLedger).toBe(firstLedger);
    expect(useAppStore.getState().recentActions).toBe(firstActions);
  });

  it('restores the complete authoritative settlement after GameOver reconnect', () => {
    const restored = restoreGameSnapshot(snapshot({
      phase: 'ended',
      settlement: {
        winner_id: 'p1',
        winner_name: '青竹',
        winner_is_landlord: true,
        multiplier: 4,
        scores: [
          { player_id: 'p1', player_name: '青竹', is_landlord: true, score: -8 },
          { player_id: 'p2', player_name: '山月', is_landlord: false, score: 4 },
          { player_id: 'p3', player_name: '松风', is_landlord: false, score: 4 }
        ],
        player_hands: [
          { player_id: 'p1', player_name: '青竹', cards: [card(0, 3)] },
          { player_id: 'p2', player_name: '山月', cards: [] },
          { player_id: 'p3', player_name: '松风', cards: [card(1, 4)] }
        ]
      }
    }), { currentPlayerId: 'p1', receivedAt: 9_000 });

    expect(restored).toMatchObject({
      phase: 'game_over',
      winnerId: 'p1',
      winnerName: '青竹',
      winnerIsLandlord: true,
      finalMultiplier: 4,
      settlementSyncError: ''
    });
    expect(restored.scores).toHaveLength(3);
    expect(restored.playerHands).toHaveLength(3);
  });

  it('marks a legacy ended snapshot without settlement as unsynchronized', () => {
    const restored = restoreGameSnapshot(snapshot({ phase: 'ended' }), {
      currentPlayerId: 'p1',
      receivedAt: 9_000
    });

    expect(restored).toMatchObject({
      phase: 'game_over',
      winnerId: '',
      winnerName: '',
      scores: [],
      playerHands: [],
      settlementSyncError: '结算数据缺失，请重新连接同步本局结果'
    });
  });

  it('rejects an unknown snapshot phase and requests an authoritative resync', () => {
    useAppStore.setState({ phase: 'playing', gameId: 'g-current', streamId: 'game:g-current' });

    const result = useAppStore.getState().handleMessage({
      type: MsgType.Reconnected,
      payload: {
        player_id: 'p1',
        player_name: '青竹',
        room_code: '123456',
        reconnect_token: 'rotated-token',
        game_state: snapshot({ phase: 'paused', game_id: 'g-unknown', snapshot_version: 1 })
      }
    });

    expect(result).toMatchObject({
      authoritativeResyncRequired: true,
      reason: expect.stringContaining('paused')
    });
    expect(useAppStore.getState()).toMatchObject({
      phase: 'playing',
      gameId: 'g-current',
      error: expect.stringContaining('paused')
    });
  });

  it.each([
    {
      winner_id: '',
      winner_name: '',
      winner_is_landlord: false,
      multiplier: 0,
      scores: [],
      player_hands: []
    },
    {
      winner_id: 'p1',
      winner_name: '青竹',
      winner_is_landlord: true,
      multiplier: 2,
      scores: [{ player_id: 'p1', player_name: '青竹', is_landlord: true, score: 4 }],
      player_hands: [{ player_id: 'p1', player_name: '青竹', cards: [] }]
    }
  ])('rejects a present but incomplete settlement instead of inventing a winner', (settlement) => {
    const restored = restoreGameSnapshot(snapshot({ phase: 'ended', settlement }), {
      currentPlayerId: 'p1',
      receivedAt: 9_000
    });

    expect(restored).toMatchObject({
      winnerId: '',
      winnerName: '',
      scores: [],
      playerHands: [],
      settlementSyncError: '结算数据缺失，请重新连接同步本局结果'
    });
  });
});

describe('authoritative event ordering', () => {
  it('accepts version gaps and rejects duplicate, stale, cross-game, and retired GameStart events', () => {
    const startOne = {
      type: MsgType.GameStart,
      payload: { players },
      event: event('g1', 1, 0)
    } as const;
    useAppStore.getState().handleMessage(startOne);

    useAppStore.getState().handleMessage({
      type: MsgType.DealCards,
      payload: { cards: [card(0, 3)], bottom_cards: [] },
      event: event('g1', 10, 0)
    });
    expect(useAppStore.getState().eventVersion).toBe(10);
    expect(useAppStore.getState().hand).toEqual([card(0, 3)]);

    useAppStore.getState().handleMessage({
      type: MsgType.DealCards,
      payload: { cards: [card(1, 4)], bottom_cards: [] },
      event: event('g1', 10, 0)
    });
    useAppStore.getState().handleMessage({
      type: MsgType.DealCards,
      payload: { cards: [card(2, 5)], bottom_cards: [] },
      event: event('g1', 9, 0)
    });
    useAppStore.getState().handleMessage({
      type: MsgType.CardPlayed,
      payload: { player_id: 'p2', player_name: '山月', cards: [card(3, 6)], cards_left: 11, hand_type: '单张' },
      event: event('g2', 11, 0)
    });
    expect(useAppStore.getState().hand).toEqual([card(0, 3)]);
    expect(useAppStore.getState().lastPlayed).toEqual([]);

    useAppStore.getState().handleMessage({
      type: MsgType.DealCards,
      payload: { cards: [card(1, 9)], bottom_cards: [] }
    });
    useAppStore.getState().handleMessage({
      type: MsgType.GameStart,
      payload: { players: [] }
    });
    expect(useAppStore.getState().gameId).toBe('g1');
    expect(useAppStore.getState().hand).toEqual([card(0, 3)]);

    useAppStore.getState().handleMessage({
      type: MsgType.GameStart,
      payload: { players },
      event: event('g2', 1, 0)
    });
    expect(useAppStore.getState().gameId).toBe('g2');

    useAppStore.getState().handleMessage(startOne);
    expect(useAppStore.getState().gameId).toBe('g2');
  });

  it('keeps the valid last play when another player passes', () => {
    useAppStore.setState({
      ...initialGameSlice,
      ...initialRoomSlice,
      playerId: 'p1',
      players,
      phase: 'playing',
      gameId: 'g1',
      streamId: 'game:g1',
      eventVersion: 3,
      turnId: 2,
      seenGameStreams: { 'game:g1': 3 },
      lastPlayed: [card(0, 8)],
      lastPlayedBy: 'p2',
      lastPlayedName: '山月',
      lastHandType: '单张'
    });

    useAppStore.getState().handleMessage({
      type: MsgType.PlayerPass,
      payload: { player_id: 'p3', player_name: '松风' },
      event: event('g1', 4, 2)
    });

    expect(useAppStore.getState()).toMatchObject({
      lastPlayedBy: 'p2',
      lastPlayedName: '山月',
      lastHandType: '单张'
    });
    expect(useAppStore.getState().lastPlayed).toEqual([card(0, 8)]);
    expect(useAppStore.getState().seatActions.p3?.type).toBe('pass');
  });

  it('keeps seen game streams in a bounded least-recently-used set', () => {
    const capacity = SEEN_GAME_STREAM_LIMIT;
    const original = Object.fromEntries(
      Array.from({ length: capacity }, (_, index) => [`game:g${index}`, index + 1])
    );

    const refreshed = restoreGameSnapshot(snapshot({ game_id: 'g0', snapshot_version: 100 }), {
      currentPlayerId: 'p1',
      receivedAt: 10_000,
      event: event('g0', 100, 2),
      seenGameStreams: original
    });
    const extended = restoreGameSnapshot(snapshot({ game_id: 'g64', snapshot_version: 1 }), {
      currentPlayerId: 'p1',
      receivedAt: 10_001,
      event: event('g64', 1, 2),
      seenGameStreams: refreshed.seenGameStreams
    });

    expect(Object.keys(extended.seenGameStreams ?? {})).toHaveLength(capacity);
    expect(extended.seenGameStreams).toMatchObject({
      'game:g0': 100,
      'game:g64': 1
    });
    expect(extended.seenGameStreams).not.toHaveProperty('game:g1');
  });
});

describe('server clock and card counter', () => {
  it('uses the midpoint offset from the minimum-RTT Pong sample', () => {
    const first = observePong(initialServerClock, {
      client_timestamp: 1_000,
      server_timestamp: 1_120
    }, 1_100);
    expect(first).toEqual({ latency: 100, clockBestRttMs: 100, serverClockOffsetMs: 70 });

    const queued = observePong(first, {
      client_timestamp: 2_000,
      server_timestamp: 2_190
    }, 2_200);
    expect(queued).toEqual({ latency: 200, clockBestRttMs: 100, serverClockOffsetMs: 70 });

    const better = observePong(queued, {
      client_timestamp: 3_000,
      server_timestamp: 3_080
    }, 3_060);
    expect(better).toEqual({ latency: 60, clockBestRttMs: 60, serverClockOffsetMs: 50 });
    expect(remainingSeconds(5_000, better.serverClockOffsetMs, 3_950)).toBe(1);
    expect(remainingSeconds(5_000, better.serverClockOffsetMs, 5_500)).toBe(0);
  });

  it('subtracts each known physical card once across hand, bottom cards, and the played ledger', () => {
    const hand = [card(0, 3), card(1, 3)];
    const bottom = [card(2, 3), card(0, 4)];
    const ledger = ledgerFromSnapshot([
      { player_id: 'p2', cards: [card(0, 5), card(0, 5)] },
      { player_id: 'p3', cards: [card(0, 5)] }
    ]);

    const counter = buildCardCounter(hand, bottom, true, ledger);
    expect(counter[3]).toBe(1);
    expect(counter[4]).toBe(3);
    expect(counter[5]).toBe(3);
  });

  it('does not double-deduct the ledger when an event is replayed after reconnect', () => {
    const dto = snapshot({
      snapshot_version: 5,
      game_id: 'g1',
      hand: [card(0, 3)],
      played_cards: [{ player_id: 'p2', cards: [card(0, 5)] }]
    });
    const restored = restoreGameSnapshot(dto, {
      currentPlayerId: 'p1',
      receivedAt: 1_000,
      event: event('g1', 5, 2)
    });
    const state = {
      ...initialGameSlice,
      ...initialRoomSlice,
      ...restored,
      playerId: 'p1',
      selectedCards: new Set<string>(),
      drawer: 'none' as const
    };
    const replay = {
      type: MsgType.CardPlayed,
      payload: { player_id: 'p2', player_name: '山月', cards: [card(0, 5)], cards_left: 11, hand_type: '单张' },
      event: event('g1', 5, 2)
    } satisfies IncomingMessage;

    const beforeReplay = state.cardCounter[5];
    const result = reduceGameMessage(state, replay, 1_100, 0);
    expect(result?.ignored).toBe(true);
    expect(state.cardCounter[5]).toBe(beforeReplay);
  });
});

function snapshot(overrides: Partial<GameStateDTO> = {}): GameStateDTO {
  return {
    phase: 'playing',
    players,
    hand: [card(0, 3)],
    bottom_cards: [card(1, 4), card(2, 5), card(3, 6)],
    current_turn: 'p1',
    last_played: [card(0, 8)],
    last_player_id: 'p2',
    must_play: false,
    can_beat: true,
    snapshot_version: 3,
    game_id: 'g1',
    bottom_cards_revealed: true,
    turn_id: 2,
    turn_deadline_ms: 12_000,
    server_time_ms: 10_000,
    last_player_name: '山月',
    last_hand_type: '单张',
    is_grab: false,
    multiplier: 2,
    base_score: 1,
    played_cards: [{ player_id: 'p2', cards: [card(0, 8)] }],
    ...overrides
  };
}

function event(gameId: string, version: number, turnId: number): EventMeta {
  return {
    stream_id: `game:${gameId}`,
    event_version: version,
    game_id: gameId,
    turn_id: turnId,
    server_time_ms: 10_000 + version,
    turn_deadline_ms: 20_000
  };
}

function card(suit: number, rank: number): CardInfo {
  return { suit, rank, color: suit === 1 || suit === 3 || rank === 17 ? 1 : 0 };
}
