import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MsgType } from '../src/protocol/types';
import { clearLegacyReconnectCredential, useAppStore } from '../src/stores/appStore';

describe('app store message flow', () => {
  beforeEach(() => {
    localStorage.clear();
    useAppStore.setState({
      connected: false,
      connectionStatus: 'idle',
      playerId: '',
      playerName: '',
      provisionalIdentity: null,
      reconnectNotice: '',
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

  it('enters lobby after connected', () => {
    useAppStore.getState().handleMessage({
      type: MsgType.Connected,
      payload: { player_id: 'p1', player_name: '青竹', web_session_ticket: 'ticket' }
    });
    expect(useAppStore.getState().phase).toBe('lobby');
    expect(useAppStore.getState().playerId).toBe('p1');
  });

  it('ignores a stale room-left event after entering a replacement room', () => {
    useAppStore.setState({
      phase: 'waiting',
      roomCode: 'new-room',
      players: [{ id: 'p1', name: '青竹', seat: 0, ready: false, is_landlord: false, cards_count: 0, online: true }],
      hand: [{ suit: 0, rank: 14, color: 0 }]
    });

    useAppStore.getState().handleMessage({
      type: MsgType.RoomLeft,
      payload: { room_code: 'old-room' }
    });

    expect(useAppStore.getState().roomCode).toBe('new-room');
    expect(useAppStore.getState().phase).toBe('waiting');
    expect(useAppStore.getState().players).toHaveLength(1);
    expect(useAppStore.getState().hand).toHaveLength(1);

    useAppStore.getState().handleMessage({
      type: MsgType.RoomLeft,
      payload: { room_code: 'new-room' }
    });
    expect(useAppStore.getState().roomCode).toBe('');
    expect(useAppStore.getState().phase).toBe('lobby');
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

  it('preserves explicit zero counts and offline flags while syncing the local hand', () => {
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
    expect(useAppStore.getState().players.map((player) => player.cards_count)).toEqual([2, 0, 0]);
    expect(useAppStore.getState().players.every((player) => player.online === false)).toBe(true);
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
  });

  it('keeps only player metadata while Connected is provisional', () => {
    useAppStore.setState({ playerId: 'old-player', playerName: '旧玩家' });
    useAppStore.getState().prepareConnection();

    useAppStore.getState().handleMessage({
      type: MsgType.Connected,
      payload: {
        player_id: 'temp-player',
        player_name: '临时玩家',
        web_session_ticket: 'temporary-ticket',
        reconnect_available: true
      }
    });

    const state = useAppStore.getState();
    expect(state.connectionStatus).toBe('reconnecting');
    expect(state.playerId).toBe('old-player');
    expect(state.provisionalIdentity).toEqual({ id: 'temp-player', name: '临时玩家' });
  });

  it('accepts restored player metadata confirmed by Reconnected', () => {
    useAppStore.getState().prepareConnection();
    useAppStore.getState().handleMessage({
      type: MsgType.Connected,
      payload: {
        player_id: 'temp-player',
        player_name: '临时玩家',
        web_session_ticket: 'temporary-ticket',
        reconnect_available: true
      }
    });
    useAppStore.getState().handleMessage({
      type: MsgType.Reconnected,
      payload: {
        player_id: 'old-player',
        player_name: '青竹',
        room_code: '',
        web_session_ticket: 'rotated-ticket'
      }
    });

    expect(useAppStore.getState().connectionStatus).toBe('reconnected');
    expect(useAppStore.getState().playerId).toBe('old-player');
    expect(useAppStore.getState().provisionalIdentity).toBeNull();
  });

  it.each([
    [1003, '重连令牌无效'],
    [1004, '重连令牌已过期']
  ])('does not accept the provisional identity after reconnect error %s', (code, message) => {
    useAppStore.setState({ phase: 'playing', roomCode: '888888' });
    useAppStore.getState().prepareConnection();
    useAppStore.getState().handleMessage({
      type: MsgType.Connected,
      payload: {
        player_id: 'temp-player',
        player_name: '临时玩家',
        web_session_ticket: 'temporary-ticket',
        reconnect_available: true
      }
    });
    useAppStore.getState().handleMessage({
      type: MsgType.Error,
      payload: { code, message, request_id: '' }
    });

    const state = useAppStore.getState();
    expect(state.connectionStatus).toBe('reconnecting');
    expect(state.playerId).toBe('');
    expect(state.provisionalIdentity).toEqual({ id: 'temp-player', name: '临时玩家' });
    expect(state.phase).toBe('playing');
    expect(state.roomCode).toBe('888888');
    expect(state.reconnectNotice).toBe('');
    expect(state.error).toBe(message);
  });

  it('removes the legacy credential without reading or replacing it', () => {
    localStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'legacy-player', token: 'legacy-token' }));
    const getItem = vi.spyOn(Storage.prototype, 'getItem');
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    clearLegacyReconnectCredential();
    expect(localStorage.getItem('ddz_next_reconnect')).toBeNull();
    expect(getItem).toHaveBeenCalledTimes(1);
    expect(setItem).not.toHaveBeenCalled();
    getItem.mockRestore();
    setItem.mockRestore();
  });

  it('reports legacy credential cleanup failures without exposing values', () => {
    const removeItem = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new DOMException('blocked sensitive-value', 'SecurityError');
    });
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);

    clearLegacyReconnectCredential();

    expect(warn).toHaveBeenCalledWith(
      'Unable to remove the legacy browser session credential',
      'SecurityError'
    );
    expect(warn.mock.calls.flat().join(' ')).not.toContain('sensitive-value');
    removeItem.mockRestore();
    warn.mockRestore();
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
