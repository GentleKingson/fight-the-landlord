import { beforeEach, describe, expect, it } from 'vitest';
import { MsgType } from '../src/protocol/types';
import { useAppStore } from '../src/stores/appStore';

describe('reconnect preserves recoverable table state', () => {
  beforeEach(() => useAppStore.getState().leaveLocalRoom());

  it.each([1, 4, 8])('restores authoritative x%i multiplier through a playing snapshot and turn replay', (multiplier) => {
    useAppStore.setState({ playerId: 'farmer', phase: 'playing', multiplier: 999 });
    const receive = useAppStore.getState().handleMessage;
    receive({ type: MsgType.Reconnected, payload: {
      player_id: 'farmer', game_state: { phase: 'playing', players: [], hand: [], multiplier }
    } });
    expect(useAppStore.getState().multiplier).toBe(multiplier);
    receive({ type: MsgType.PlayTurn, payload: { player_id: 'landlord', timeout: 30, must_play: true, can_beat: true } });
    expect(useAppStore.getState().multiplier).toBe(multiplier);
  });

  it.each(['dealing', 'bidding'])('initializes a farmer counter on %s reconnect and deducts later plays', (phase) => {
    const hand = Array.from({ length: 17 }, (_, i) => ({ suit: i % 4, rank: 3 + Math.floor(i / 4), color: 0 }));
    useAppStore.setState({ playerId: 'farmer', multiplier: 4, cardCounter: {} });
    const receive = useAppStore.getState().handleMessage;
    receive({ type: MsgType.Reconnected, payload: {
      player_id: 'farmer', game_state: { phase, players: [], hand, bottom_cards: [] }
    } });
    expect(useAppStore.getState().multiplier).toBe(1);
    expect(useAppStore.getState().cardCounter[3]).toBe(0);
    expect(useAppStore.getState().cardCounter[7]).toBe(3);
    expect(useAppStore.getState().cardCounter[17]).toBe(1);
    expect(Object.values(useAppStore.getState().cardCounter).reduce((sum, count) => sum + count, 0)).toBe(37);
    receive({ type: MsgType.BidTurn, payload: { player_id: 'landlord', timeout: 15, multiplier: 1 } });
    receive({ type: MsgType.Landlord, payload: {
      player_id: 'landlord', bottom_cards: [{ suit: 3, rank: 7, color: 1 }], multiplier: 2
    } });
    receive({ type: MsgType.PlayTurn, payload: { player_id: 'landlord', timeout: 30, must_play: true, can_beat: true } });
    receive({ type: MsgType.CardPlayed, payload: {
      player_id: 'landlord', cards: [{ suit: 3, rank: 7, color: 1 }], cards_left: 19, hand_type: '单张'
    } });
    expect(useAppStore.getState().cardCounter[7]).toBe(2);
    expect(Object.values(useAppStore.getState().cardCounter).reduce((sum, count) => sum + count, 0)).toBe(36);
  });
});

describe('app store message flow', () => {
  beforeEach(() => {
    sessionStorage.clear();
    useAppStore.setState({
      connected: false,
      playerId: '',
      playerName: '',
      reconnectToken: '',
      phase: 'connecting',
      players: [],
      roomCode: '',
      hand: [],
      bottomCards: [],
      bottomCardsRevealed: false,
      currentTurn: '',
      selectedCards: new Set(),
      recentActions: [],
      seatActions: {},
      cardCounter: {}
    });
  });

  it('keeps revealed bottom cards through the landlord supplemental deal', () => {
    const cards = Array.from({ length: 17 }, (_, i) => ({ suit: i % 4, rank: 3 + Math.floor(i / 4), color: 0 }));
    const bottom = [{ suit: 0, rank: 14, color: 0 }, { suit: 1, rank: 14, color: 1 }, { suit: 2, rank: 14, color: 0 }];
    useAppStore.setState({ playerId: 'p1' });
    const receive = useAppStore.getState().handleMessage;
    receive({ type: MsgType.DealCards, payload: { cards, bottom_cards: [] } });
    receive({ type: MsgType.Landlord, payload: { player_id: 'p1', bottom_cards: bottom } });
    receive({ type: MsgType.DealCards, payload: { cards: [...cards, ...bottom], bottom_cards: bottom } });
    receive({ type: MsgType.PlayTurn, payload: { player_id: 'p1' } });
    expect(useAppStore.getState().bottomCardsRevealed).toBe(true);
    expect(useAppStore.getState().hand).toHaveLength(20);
    receive({ type: MsgType.DealCards, payload: { cards, bottom_cards: [] } });
    expect(useAppStore.getState().bottomCardsRevealed).toBe(false);
  });

  it('clears the previous no-call history and selection when a round is redealt', () => {
    useAppStore.setState({ playerId: 'p1', phase: 'bidding', selectedCards: new Set(['0_3']) });
    const receive = useAppStore.getState().handleMessage;
    for (const id of ['p1', 'p2', 'p3']) {
      receive({ type: MsgType.BidResult, payload: { player_id: id, player_name: id, bid: false, is_grab: false, multiplier: 1 } });
    }
    const cards = Array.from({ length: 17 }, (_, i) => ({ suit: i % 4, rank: 3 + Math.floor(i / 4), color: 0 }));
    receive({ type: MsgType.DealCards, payload: { cards, bottom_cards: [{}, {}, {}] } });
    receive({ type: MsgType.BidTurn, payload: { player_id: 'p2', timeout: 15, is_grab: false, multiplier: 1 } });
    const state = useAppStore.getState();
    expect(state.recentActions).toEqual([]);
    expect(state.seatActions).toEqual({});
    expect(state.selectedCards.size).toBe(0);
    expect(state.bottomCardsRevealed).toBe(false);
    expect(state.currentTurn).toBe('p2');
    expect(state.isGrabTurn).toBe(false);
    expect(state.multiplier).toBe(1);
  });

  it('sets the other landlord count to 20 before their first play', () => {
    useAppStore.setState({ playerId: 'p1', players: [{ id: 'p2', name: '地主', seat: 1, ready: true, is_landlord: false, cards_count: 17, online: true }] });
    useAppStore.getState().handleMessage({ type: MsgType.Landlord, payload: { player_id: 'p2', bottom_cards: [] } });
    expect(useAppStore.getState().players[0].cards_count).toBe(20);
  });

  it('keeps selected cards when the server rejects a play', () => {
    useAppStore.setState({ connected: true, phase: 'playing', selectedCards: new Set(['selected']) });
    useAppStore.getState().handleMessage({ type: MsgType.Error, payload: { message: '非法出牌' } });
    expect(useAppStore.getState().error).toBe('非法出牌');
    expect(useAppStore.getState().selectedCards.has('selected')).toBe(true);
  });

  it('enters lobby after connected', () => {
    useAppStore.getState().handleMessage({
      type: MsgType.Connected,
      payload: { player_id: 'p1', player_name: '青竹', reconnect_token: 'tok' }
    });
    expect(useAppStore.getState().phase).toBe('lobby');
    expect(useAppStore.getState().playerId).toBe('p1');
  });

  it('keeps bottom cards hidden after deal cards before landlord is confirmed', () => {
    useAppStore.getState().handleMessage({
      type: MsgType.DealCards,
      payload: {
        cards: [{ suit: 0, rank: 14, color: 0 }],
        bottom_cards: [
          { suit: 1, rank: 5, color: 1 },
          { suit: 2, rank: 11, color: 0 },
          { suit: 3, rank: 15, color: 1 }
        ]
      }
    });
    expect(useAppStore.getState().bottomCards).toHaveLength(3);
    expect(useAppStore.getState().bottomCardsRevealed).toBe(false);
  });

  it('normalizes missing game player counts and online flags from protobuf defaults', () => {
    useAppStore.setState({ playerId: 'p1' });
    useAppStore.getState().handleMessage({
      type: MsgType.GameStart,
      payload: {
        players: [
          { id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: false, cards_count: 0, online: false },
          { id: 'p2', name: '山月', seat: 1, ready: true, is_landlord: false, cards_count: 0, online: false },
          { id: 'p3', name: '松风', seat: 2, ready: true, is_landlord: false, cards_count: 0, online: false }
        ]
      }
    });
    useAppStore.getState().handleMessage({
      type: MsgType.DealCards,
      payload: {
        cards: [
          { suit: 0, rank: 14, color: 0 },
          { suit: 1, rank: 13, color: 1 }
        ],
        bottom_cards: []
      }
    });
    expect(useAppStore.getState().players.map((player) => player.cards_count)).toEqual([2, 17, 17]);
    expect(useAppStore.getState().players.every((player) => player.online)).toBe(true);
  });

  it('reveals bottom cards and appends them after landlord is confirmed', () => {
    useAppStore.setState({
      playerId: 'p1',
      hand: [{ suit: 0, rank: 14, color: 0 }],
      players: [{ id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: false, cards_count: 17, online: true }]
    });
    useAppStore.getState().handleMessage({
      type: MsgType.Landlord,
      payload: {
        player_id: 'p1',
        player_name: '青竹',
        bottom_cards: [
          { suit: 1, rank: 5, color: 1 },
          { suit: 2, rank: 11, color: 0 },
          { suit: 3, rank: 15, color: 1 }
        ],
        multiplier: 3
      }
    });
    expect(useAppStore.getState().bottomCardsRevealed).toBe(true);
    expect(useAppStore.getState().hand).toHaveLength(4);
    expect(useAppStore.getState().players[0].is_landlord).toBe(true);
  });

  it('updates bid turn and bid action history', () => {
    useAppStore.getState().handleMessage({
      type: MsgType.BidTurn,
      payload: { player_id: 'p1', timeout: 20, is_grab: false, multiplier: 1 }
    });
    expect(useAppStore.getState().phase).toBe('bidding');
    expect(useAppStore.getState().currentTurn).toBe('p1');
    expect(useAppStore.getState().isGrabTurn).toBe(false);

    useAppStore.getState().handleMessage({
      type: MsgType.BidResult,
      payload: { player_id: 'p1', player_name: '青竹', bid: true, is_grab: false, multiplier: 1 }
    });
    expect(useAppStore.getState().recentActions.at(-1)?.label).toBe('叫地主');
  });

  it('restores reconnect game state with bottom-card reveal based on phase', () => {
    useAppStore.getState().handleMessage({
      type: MsgType.Reconnected,
      payload: {
        player_id: 'p1',
        player_name: '青竹',
        room_code: '888888',
        game_state: {
          phase: 'bidding',
          players: [{ id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: false, cards_count: 17, online: true }],
          hand: [{ suit: 0, rank: 14, color: 0 }],
          bottom_cards: [{ suit: 1, rank: 5, color: 1 }],
          current_turn: 'p1',
          last_played: [],
          last_player_id: '',
          must_play: true,
          can_beat: true
        }
      }
    });
    expect(useAppStore.getState().phase).toBe('bidding');
    expect(useAppStore.getState().bottomCardsRevealed).toBe(false);

    useAppStore.getState().handleMessage({
      type: MsgType.Reconnected,
      payload: {
        player_id: 'p1',
        player_name: '青竹',
        room_code: '888888',
        game_state: {
          phase: 'playing',
          players: [{ id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: true, cards_count: 20, online: true }],
          hand: [{ suit: 0, rank: 14, color: 0 }],
          bottom_cards: [{ suit: 1, rank: 5, color: 1 }],
          current_turn: 'p1',
          last_played: [],
          last_player_id: '',
          must_play: true,
          can_beat: true
        }
      }
    });
    expect(useAppStore.getState().phase).toBe('playing');
    expect(useAppStore.getState().bottomCardsRevealed).toBe(true);
    expect(useAppStore.getState().cardCounter).toEqual({});
    useAppStore.getState().handleMessage({ type: MsgType.CardPlayed, payload: { player_id: 'p2', cards: [{ suit: 0, rank: 3, color: 0 }] } });
    expect(useAppStore.getState().cardCounter).toEqual({});
  });

  it.each([
    { scenario: 'another player played while disconnected', lastPlayerId: 'b', hasCards: true, expectedName: 'B' },
    { scenario: 'the last player is missing', lastPlayerId: 'missing', hasCards: true, expectedName: '' },
    { scenario: 'the snapshot has no played cards', lastPlayerId: '', hasCards: false, expectedName: '' }
  ])('restores played-card metadata when $scenario', ({ lastPlayerId, hasCards, expectedName }) => {
    const receive = useAppStore.getState().handleMessage;
    receive({
      type: MsgType.CardPlayed,
      payload: { player_id: 'a', player_name: 'A', cards: [{ suit: 0, rank: 3, color: 0 }], cards_left: 16, hand_type: '单张' }
    });
    const cards = hasCards ? [{ suit: 0, rank: 4, color: 0 }, { suit: 1, rank: 4, color: 1 }] : [];
    receive({
      type: MsgType.Reconnected,
      payload: {
        player_id: 'a',
        player_name: 'A',
        game_state: {
          phase: 'playing',
          players: [{ id: 'b', name: 'B', seat: 1, ready: true, is_landlord: false, cards_count: 15, online: true }],
          hand: [],
          last_played: cards,
          last_player_id: lastPlayerId,
          must_play: false,
          can_beat: true
        }
      }
    });
    expect(useAppStore.getState()).toMatchObject({
      lastPlayed: cards,
      lastPlayedBy: lastPlayerId,
      lastPlayedName: expectedName,
      lastHandType: ''
    });
  });

  it('updates hand and action history after playing cards', () => {
    useAppStore.setState({
      playerId: 'p1',
      hand: [
        { suit: 0, rank: 14, color: 0 },
        { suit: 1, rank: 13, color: 1 }
      ],
      players: [{ id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: false, cards_count: 2, online: true }],
      cardCounter: { 14: 3, 13: 3 }
    });
    useAppStore.getState().handleMessage({
      type: MsgType.CardPlayed,
      payload: {
        player_id: 'p1',
        player_name: '青竹',
        cards: [{ suit: 0, rank: 14, color: 0 }],
        cards_left: 1,
        hand_type: '单张'
      }
    });
    expect(useAppStore.getState().hand).toEqual([{ suit: 1, rank: 13, color: 1 }]);
    expect(useAppStore.getState().players[0].cards_count).toBe(1);
    expect(useAppStore.getState().seatActions.p1?.type).toBe('play');
    expect(useAppStore.getState().recentActions).toHaveLength(1);
  });

  it('keeps seat actions for a trick and clears them on a new lead play', () => {
    useAppStore.setState({
      playerId: 'p1',
      mustPlay: false,
      players: [
        { id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: false, cards_count: 17, online: true },
        { id: 'p2', name: '山月', seat: 1, ready: true, is_landlord: false, cards_count: 17, online: true },
        { id: 'p3', name: '松风', seat: 2, ready: true, is_landlord: false, cards_count: 17, online: true }
      ]
    });

    useAppStore.getState().handleMessage({
      type: MsgType.CardPlayed,
      payload: {
        player_id: 'p2',
        player_name: '山月',
        cards: [{ suit: 1, rank: 9, color: 1 }],
        cards_left: 16,
        hand_type: '单张'
      }
    });
    useAppStore.getState().handleMessage({
      type: MsgType.PlayerPass,
      payload: { player_id: 'p3', player_name: '松风' }
    });
    expect(Object.keys(useAppStore.getState().seatActions).sort()).toEqual(['p2', 'p3']);

    useAppStore.setState({ mustPlay: true });
    useAppStore.getState().handleMessage({
      type: MsgType.CardPlayed,
      payload: {
        player_id: 'p2',
        player_name: '山月',
        cards: [{ suit: 2, rank: 11, color: 0 }],
        cards_left: 15,
        hand_type: '单张'
      }
    });
    expect(Object.keys(useAppStore.getState().seatActions)).toEqual(['p2']);
  });
});

it('keeps the played cards attributed to their owner when another player passes', () => {
  const receive = useAppStore.getState().handleMessage;
  const cards = [{ rank: 3, suit: 0, color: 0 }];
  receive({ type: MsgType.CardPlayed, payload: { player_id: 'a', player_name: 'A', cards, cards_left: 16, hand_type: '单张' } });
  receive({ type: MsgType.PlayerPass, payload: { player_id: 'b', player_name: 'B' } });
  expect(useAppStore.getState().lastPlayed).toEqual(cards);
  expect(useAppStore.getState().lastPlayedBy).toBe('a');
  expect(useAppStore.getState().lastPlayedName).toBe('A');
  expect(useAppStore.getState().seatActions.b.type).toBe('pass');
});

it('preserves offline flags and zero card counts in snapshots and subsequent deal messages', () => {
  const receive = useAppStore.getState().handleMessage;
  const players = [
    { id: 'a', name: 'A', seat: 0, online: true, cards_count: 17 },
    { id: 'b', name: 'B', seat: 1, online: false, cards_count: 0 }
  ];
  receive({ type: MsgType.Reconnected, payload: { player_id: 'a', game_state: { phase: 'playing', players, hand: [] } } });
  expect(useAppStore.getState().players[1].online).toBe(false);
  expect(useAppStore.getState().players[1].cards_count).toBe(0);
  receive({ type: MsgType.Landlord, payload: { player_id: 'a', bottom_cards: [] } });
  receive({ type: MsgType.DealCards, payload: { cards: [], bottom_cards: [] } });
  expect(useAppStore.getState().players[1].online).toBe(false);
  receive({ type: MsgType.PlayerOnline, payload: { player_id: 'b' } });
  expect(useAppStore.getState().players[1].online).toBe(true);
});

it('restores the counter, public history, current trick and timer after a fresh page load', () => {
  useAppStore.getState().leaveLocalRoom();
  const card = (rank: number, suit = 0) => ({ rank, suit, color: suit % 2 });
  const public_plays = [
    { type: 'play', player_id: 'me', player_name: '我', cards: [card(3)], hand_type: '单张', new_trick: true },
    { type: 'pass', player_id: 'b', player_name: 'B', cards: [], hand_type: '', new_trick: false },
    { type: 'pass', player_id: 'c', player_name: 'C', cards: [], hand_type: '', new_trick: false },
    { type: 'play', player_id: 'me', player_name: '我', cards: [card(4)], hand_type: '单张', new_trick: true },
  ];
  useAppStore.getState().handleMessage({ type: MsgType.Reconnected, payload: {
    player_id: 'me', game_state: {
      phase: 'playing', players: [], hand: [card(5)], multiplier: 8,
      public_plays, last_played: [card(4)], last_player_id: 'me', last_hand_type: '单张',
      server_time: 10000, turn_deadline: 17500,
    }
  } } as never);
  const state = useAppStore.getState();
  expect(state.multiplier).toBe(8);
  expect(state.timeout).toBe(7.5);
  expect(state.recentActions).toHaveLength(4);
  expect(Object.keys(state.seatActions)).toEqual(['me']);
  expect(state.lastHandType).toBe('单张');
  expect(state.cardCounter[3]).toBe(3);
  expect(state.cardCounter[4]).toBe(3);
  expect(state.cardCounter[5]).toBe(3);
  expect(Object.values(state.cardCounter).reduce((a,b) => a+b,0)).toBe(51);
});

it('cookie handshake keeps the practice replay preference during reload', () => {
  useAppStore.setState({ matchMode: 'practice' });
  useAppStore.getState().handleMessage({ type: MsgType.Connected, payload: { player_id: 'me', player_name: 'Me', reconnect_token: '' } });
  expect(useAppStore.getState().matchMode).toBe('practice');
});
