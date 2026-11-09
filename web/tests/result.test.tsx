import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { useAppStore } from '../src/stores/appStore';
import { END_REVEAL_DURATION_MS } from '../src/stores/endGame';
import { RESULT_ACTION_GUARD_MS } from '../src/shared/resultPresentation';
import { MsgType, type IncomingPayloadMap, type PlayerInfo } from '../src/protocol/types';

const { send } = vi.hoisted(() => ({ send: vi.fn() }));
vi.mock('../src/transport/wsClient', () => ({ createGameSocket: () => ({ connect() {}, close() {}, send, subscribe: () => () => {} }) }));
vi.mock('../src/audio/connectGameAudio', () => ({ connectGameAudio: () => () => {} }));

const players: PlayerInfo[] = [0, 1, 2].map(seat => ({
  id: `p${seat + 1}`, name: ['青竹', '山月', '松风'][seat], seat,
  ready: true, is_landlord: seat === 1, cards_count: seat === 1 ? 20 : 17, online: true
}));
const lastPlayed = [{ suit: 0, rank: 14, color: 0 }];
const receive = (type: string, payload: unknown) => useAppStore.getState().handleMessage({ type, payload });

function result(winnerId = 'p3'): IncomingPayloadMap['game_over'] {
  const winner = players.find(player => player.id === winnerId)!;
  return {
    winner_id: winnerId, winner_name: winner.name, is_landlord: winner.is_landlord, multiplier: 8,
    player_hands: players.map(player => ({
      player_id: player.id, player_name: player.name,
      cards: Array.from({ length: player.id === winnerId ? 0 : player.cards_count }, (_, i) => ({ suit: i % 4, rank: 3 + Math.floor(i / 4), color: i % 2 }))
    })),
    scores: players.map(player => ({ player_id: player.id, player_name: player.name, is_landlord: player.is_landlord, score: player.is_landlord === winner.is_landlord ? 24 : -12 }))
  };
}

function finish(winnerId = 'p3') {
  receive(MsgType.CardPlayed, { player_id: winnerId, cards: lastPlayed, cards_left: 0, hand_type: '单张' });
  receive(MsgType.GameOver, result(winnerId));
}

function reconnect(roundId = 'round-1') {
  receive(MsgType.Reconnected, { player_id: 'p1', player_name: '青竹', room_code: '123456', game_state: {
    phase: 'ended', deal_id: roundId, players, hand: [], bottom_cards: [],
    last_played: lastPlayed, last_player_id: 'p3', last_hand_type: '单张'
  } });
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-11-09T08:00:00Z'));
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  send.mockClear();
  useAppStore.setState(useAppStore.getInitialState(), true);
  useAppStore.setState({ connected: true, playerId: 'p1', roomCode: '123456', phase: 'playing', roundId: 'round-1', players, currentTurn: 'p1', hand: lastPlayed });
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

describe('terminal table snapshot', () => {
  it.each(['p1', 'p2', 'p3'])('reveals all three seats and the winning play for %s only after GameOver', winner => {
    render(<App />);
    expect(screen.queryByRole('region', { name: '终局牌桌' })).not.toBeInTheDocument();
    act(() => finish(winner));
    for (const label of ['我', '上家', '下家']) expect(screen.getByRole('region', { name: `${label}剩余手牌` })).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: '获胜最后一手' })).getByRole('button', { name: 'A♠' })).toBeDisabled();
    expect(screen.getByText('已出完')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '出牌' })).not.toBeInTheDocument();
    expect(screen.queryByRole('option')).not.toBeInTheDocument();
    const state = useAppStore.getState();
    expect(state.revealDeadline).toBe(Date.now() + END_REVEAL_DURATION_MS);
    expect(state.currentTurn).toBe('');
    expect(state.selectedCards.size).toBe(0);
  });

  it('copies the result and keeps it independent of live presence updates', () => {
    const payload = result();
    receive(MsgType.GameOver, payload);
    payload.player_hands[0].cards[0].rank = 99;
    payload.scores[0].score = 999;
    receive(MsgType.PlayerLeft, { player_id: 'p1' });
    receive(MsgType.PlayerOffline, { player_id: 'p2' });
    const state = useAppStore.getState();
    expect(state.phase).toBe('game_over');
    expect(state.endSnapshot?.players).toHaveLength(3);
    expect(state.endSnapshot?.players[1].online).toBe(true);
    expect(state.endSnapshot?.playerHands[0].cards.some(card => card.rank === 99)).toBe(false);
    expect(state.endSnapshot?.scores[0].score).toBe(24);
    expect(state.endSnapshot?.lastPlayed).toEqual([]);
  });

  it('ignores duplicate results without restarting the deadline', () => {
    finish();
    const deadline = useAppStore.getState().revealDeadline;
    vi.advanceTimersByTime(4000);
    receive(MsgType.GameOver, result());
    expect(useAppStore.getState().revealDeadline).toBe(deadline);
  });
});

describe('end-screen navigation', () => {
  it('supports early settlement and repeated unlimited read-only review without sending messages', () => {
    finish();
    render(<App />);
    act(() => vi.advanceTimersByTime(RESULT_ACTION_GUARD_MS));
    fireEvent.click(screen.getByRole('button', { name: '查看结算' }));
    expect(screen.getByText('最终倍数 x8')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'A♠' })).not.toBeInTheDocument();
    for (let i = 0; i < 3; i++) {
      fireEvent.click(screen.getByRole('button', { name: '查看本局牌面' }));
      expect(screen.getByRole('heading', { name: '本局牌面' })).toHaveFocus();
      act(() => { vi.advanceTimersByTime(180_000); receive(MsgType.GameOver, result()); });
      expect(screen.getByText('仅供查看 · 不限时')).toBeInTheDocument();
      expect(screen.queryByText(/秒后显示结算/)).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: '返回结算' }));
    }
    expect(send).not.toHaveBeenCalled();
  });

  it('automatically settles at the original deadline despite duplicate messages', () => {
    finish();
    render(<App />);
    act(() => vi.advanceTimersByTime(9000));
    expect(screen.getByText('1 秒后显示结算')).toBeInTheDocument();
    act(() => { receive(MsgType.GameOver, result()); vi.advanceTimersByTime(1000); });
    expect(screen.getByRole('button', { name: '查看本局牌面' })).toBeInTheDocument();
  });

  it('rechecks wall-clock time on visibility changes when background timers were suspended', () => {
    finish();
    render(<App />);
    vi.setSystemTime(Date.now() + 30_000);
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    expect(screen.getByRole('button', { name: '查看本局牌面' })).toBeInTheDocument();
  });

  it('clears the timer and snapshot before starting another game', () => {
    finish();
    render(<App />);
    act(() => vi.advanceTimersByTime(RESULT_ACTION_GUARD_MS));
    fireEvent.click(screen.getByRole('button', { name: '查看结算' }));
    useAppStore.setState({ matchMode: 'practice' });
    fireEvent.click(screen.getByRole('button', { name: '再来一局' }));
    expect(send).toHaveBeenCalledExactlyOnceWith(MsgType.PracticeMatch);
    expect(useAppStore.getState().endSnapshot).toBeNull();
    act(() => { receive(MsgType.GameStart, { players }); vi.advanceTimersByTime(20_000); });
    expect(useAppStore.getState().phase).toBe('dealing');
    expect(useAppStore.getState().revealDeadline).toBeNull();
  });

  it('does not reopen a dismissed result on a delayed duplicate', () => {
    finish();
    useAppStore.getState().leaveLocalRoom();
    receive(MsgType.GameOver, result());
    expect(useAppStore.getState().phase).toBe('lobby');
    expect(useAppStore.getState().endSnapshot).toBeNull();
  });

  it('guards the final play gesture and double tap, then permits keyboard settlement', () => {
    render(<App />);
    const play = screen.getByRole('button', { name: '出牌' });
    play.focus();
    act(() => finish());
    const settle = screen.getByRole('button', { name: '查看结算' });
    expect(settle).not.toBe(play);
    expect(settle).not.toHaveFocus();
    expect(settle).toBeDisabled();
    fireEvent.click(settle);
    expect(screen.getByRole('region', { name: '终局牌桌' })).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(RESULT_ACTION_GUARD_MS));
    expect(settle).toBeEnabled();
    fireEvent.click(settle, { detail: 2 });
    expect(screen.getByRole('region', { name: '终局牌桌' })).toBeInTheDocument();
    fireEvent.click(settle, { detail: 0 });
    expect(screen.getByRole('button', { name: '查看本局牌面' })).toBeInTheDocument();
    expect(send).not.toHaveBeenCalled();
    expect(useAppStore.getState().revealDeadline).toBeNull();
    act(() => vi.advanceTimersByTime(END_REVEAL_DURATION_MS));
    expect(useAppStore.getState().endView).toBe('result');
  });

  it.each(['leave', 'next game', 'unmount'])('cleans up a pending reveal on %s', action => {
    finish();
    const { unmount } = render(<App />);
    act(() => {
      vi.advanceTimersByTime(500);
      if (action === 'leave') useAppStore.getState().leaveLocalRoom();
      else if (action === 'next game') receive(MsgType.GameStart, { players });
      else unmount();
    });
    expect(vi.getTimerCount()).toBe(action === 'next game' ? 1 : 0);
    act(() => vi.advanceTimersByTime(END_REVEAL_DURATION_MS));
    expect(screen.queryByRole('button', { name: '查看本局牌面' })).not.toBeInTheDocument();
  });

  it('an old deadline cannot settle the next game early', () => {
    finish();
    render(<App />);
    act(() => {
      vi.advanceTimersByTime(1000);
      receive(MsgType.GameStart, { players });
      receive(MsgType.PlayTurn, { player_id: 'p2', turn_token: 'new-turn', timeout: 30 });
      finish('p2');
    });
    expect(useAppStore.getState().turnToken).toBe('');
    act(() => vi.advanceTimersByTime(END_REVEAL_DURATION_MS - 1000));
    expect(screen.getByRole('region', { name: '终局牌桌' })).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByRole('button', { name: '查看本局牌面' })).toBeInTheDocument();
  });
});

describe('ended-game recovery', () => {
  it('waits for the official settlement before revealing hands on a fresh reconnect', () => {
    reconnect();
    render(<App />);
    expect(screen.getByRole('heading', { name: '正在恢复本局结果' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: '终局牌桌' })).not.toBeInTheDocument();
    expect(useAppStore.getState().revealDeadline).toBeNull();
    act(() => { vi.advanceTimersByTime(5000); receive(MsgType.GameOver, result()); });
    expect(screen.getByText('10 秒后显示结算')).toBeInTheDocument();
    expect(useAppStore.getState().endSnapshot?.lastPlayed).toEqual(lastPlayed);
  });

  it.each(['reveal', 'result', 'review'] as const)('preserves %s through a same-round reconnect', view => {
    finish();
    if (view === 'result') useAppStore.getState().showSettlement();
    if (view === 'review') useAppStore.getState().reviewEndTable();
    const before = useAppStore.getState();
    vi.advanceTimersByTime(4000);
    reconnect();
    receive(MsgType.GameOver, result());
    expect(useAppStore.getState().endView).toBe(view);
    expect(useAppStore.getState().revealDeadline).toBe(before.revealDeadline);
    expect(useAppStore.getState().endSnapshot).toBe(before.endSnapshot);
  });

  it('does not confuse another round in the same room with the saved result', () => {
    finish();
    useAppStore.getState().reviewEndTable();
    reconnect('round-2');
    expect(useAppStore.getState().endSnapshot).toBeNull();
    receive(MsgType.GameOver, result());
    expect(useAppStore.getState().endSnapshot?.roundId).toBe('round-2');
    expect(useAppStore.getState().endView).toBe('reveal');
  });

  it('retains the local result after the server expires its ended room', () => {
    finish();
    useAppStore.getState().reviewEndTable();
    receive(MsgType.Reconnected, { player_id: 'p1', player_name: '青竹' });
    expect(useAppStore.getState().phase).toBe('game_over');
    expect(useAppStore.getState().endView).toBe('review');
    expect(useAppStore.getState().endSnapshot).not.toBeNull();
  });
});
