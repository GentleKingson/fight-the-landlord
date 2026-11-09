import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { FINAL_PLAY_DURATION_MS, RESULT_ACTION_GUARD_MS } from '../src/shared/resultPresentation';
import { MsgType } from '../src/protocol/types';
import { useAppStore } from '../src/stores/appStore';

const { send } = vi.hoisted(() => ({ send: vi.fn() }));
vi.mock('../src/transport/wsClient', () => ({ createGameSocket: () => ({ connect() {}, close() {}, send, subscribe: () => () => {} }) }));
vi.mock('../src/features/lobby/Lobby', () => ({ Lobby: () => <div>大厅</div> }));

const players = ['me', 'right', 'left'].map((id, seat) => ({ id, seat, name: id, cards_count: 1, is_landlord: seat === 0, online: true, ready: true }));
const cards = [{ rank: 10, suit: 0, color: 0 }];
const receive = (type: string, payload: unknown) => useAppStore.getState().handleMessage({ type, payload });
const result = (id = 'me') => receive(MsgType.GameOver, { winner_id: id, winner_name: id, is_landlord: id === 'me', multiplier: 2, scores: [], player_hands: [] });
const finish = (id = 'me') => {
  receive(MsgType.CardPlayed, { player_id: id, player_name: id, cards, cards_left: 0, hand_type: '单张' });
  result(id);
};

beforeEach(() => {
  send.mockClear();
  vi.useFakeTimers();
  vi.setSystemTime(500000);
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }));
  useAppStore.setState(useAppStore.getInitialState(), true);
  useAppStore.setState({ connected: true, playerId: 'me', players, phase: 'playing', currentTurn: 'me', hand: cards, timeout: 30, timerStart: Date.now(), selectedCards: new Set(['0_10']) });
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

it.each(['me', 'left', 'right'])('holds the final play at the %s seat for five seconds with gameplay stopped', (id) => {
  render(<App />);
  // Both messages in one React batch, just like consecutive socket events.
  act(() => finish(id));
  expect(useAppStore.getState()).toMatchObject({ phase: 'game_over', currentTurn: '', timeout: 0, timerStart: 0, mustPlay: false, canBeat: false });
  expect(useAppStore.getState().selectedCards.size).toBe(0);
  const seat = screen.getByRole('region', { name: `${id}的出牌` });
  expect(seat).toHaveClass(`played-seat--${id}`);
  expect(within(seat).getByRole('button', { name: '10♠' })).toBeVisible();
  expect(document.querySelector('.felt-table')).toHaveClass('felt-table--seats');
  expect(document.querySelector('.turn-banner time')).toBeNull();
  expect(screen.getByRole('listbox', { name: '手牌' })).toHaveAttribute('aria-disabled', 'true');
  expect(screen.queryByRole('button', { name: /^(出牌|不出|提示|重选)$/ })).not.toBeInTheDocument();
  act(() => vi.advanceTimersByTime(FINAL_PLAY_DURATION_MS - 1));
  expect(screen.getByRole('region', { name: '斗地主牌桌' })).toBeVisible();
  act(() => vi.advanceTimersByTime(1));
  expect(screen.queryByRole('region', { name: '斗地主牌桌' })).not.toBeInTheDocument();
  const replay = screen.getByRole('region', { name: '获胜最后一手' });
  expect(within(replay).getByRole('button', { name: '10♠' })).toBeVisible();
  expect(within(replay).getByText(id)).toBeVisible();
  expect(within(replay).getByText('单张')).toBeVisible();
});

it('does not restart presentation on duplicate settlements', () => {
  render(<App />);
  act(() => finish());
  act(() => vi.advanceTimersByTime(1500));
  act(() => result());
  act(() => vi.advanceTimersByTime(FINAL_PLAY_DURATION_MS - 1500));
  expect(screen.getByRole('button', { name: '再来一局' })).toBeVisible();
  act(() => result());
  expect(screen.getByRole('button', { name: '再来一局' })).toBeVisible();
});

it('shows a restored ended game immediately, even during a pending live presentation', () => {
  render(<App />);
  act(() => finish());
  act(() => vi.advanceTimersByTime(500));
  act(() => {
    receive(MsgType.Reconnected, { player_id: 'me', player_name: 'me', game_state: { phase: 'ended', players, hand: [], last_played: cards, last_player_id: 'right', last_hand_type: '单张' } });
    result('right');
  });
  expect(screen.getByRole('button', { name: '再来一局' })).toBeVisible();
  expect(within(screen.getByRole('region', { name: '获胜最后一手' })).getByText('right')).toBeVisible();
  expect(useAppStore.getState().resultRevealAt).toBeNull();
  act(() => vi.advanceTimersByTime(FINAL_PLAY_DURATION_MS));
  expect(screen.getByRole('button', { name: '再来一局' })).toBeVisible();
});

it.each(['leave', 'next game', 'unmount'])('cleans up the pending presentation on %s', (action) => {
  const { unmount } = render(<App />);
  act(() => finish());
  act(() => vi.advanceTimersByTime(500));
  act(() => {
    if (action === 'leave') useAppStore.getState().leaveLocalRoom();
    else if (action === 'next game') receive(MsgType.GameStart, { players });
    else unmount();
  });
  expect(vi.getTimerCount()).toBe(action === 'next game' ? 1 : 0); // Only the new table's turn banner may tick.
  act(() => vi.advanceTimersByTime(FINAL_PLAY_DURATION_MS));
  if (action === 'leave') expect(screen.getByText('大厅')).toBeVisible();
  if (action === 'next game') expect(useAppStore.getState().phase).toBe('dealing');
  expect(screen.queryByRole('button', { name: '再来一局' })).not.toBeInTheDocument();
});

it('an old deadline cannot reveal the next game result early', () => {
  render(<App />);
  act(() => finish());
  act(() => vi.advanceTimersByTime(1000));
  act(() => {
    receive(MsgType.GameStart, { players });
    receive(MsgType.PlayTurn, { player_id: 'right', timeout: 30 });
    finish('right');
  });
  act(() => vi.advanceTimersByTime(FINAL_PLAY_DURATION_MS - 1000));
  expect(screen.getByRole('region', { name: 'right的出牌' })).toBeVisible();
  act(() => vi.advanceTimersByTime(1000));
  expect(screen.getByRole('button', { name: '再来一局' })).toBeVisible();
});

it('does not label another player’s cards as the winning hand when the final play is missing', () => {
  useAppStore.setState({ lastPlayed: cards, lastPlayedBy: 'left' });
  render(<App />);
  act(() => result('right'));
  expect(screen.getByRole('button', { name: '再来一局' })).toBeVisible();
  expect(screen.getByText('暂无最后一手记录')).toBeVisible();
});


it('guards the final play gesture, then allows a local early reveal without restarting on duplicates', () => {
  render(<App />);
  const playButton = screen.getByRole('button', { name: '出牌' });
  playButton.focus();
  act(() => finish());
  const reveal = screen.getByRole('button', { name: '查看结算' });
  expect(reveal).not.toBe(playButton);
  expect(reveal).not.toHaveFocus();
  expect(reveal).toBeDisabled();
  expect(screen.getByRole('timer')).toHaveTextContent('5 秒后自动结算');
  fireEvent.click(reveal);
  expect(screen.getByRole('region', { name: '斗地主牌桌' })).toBeVisible();
  act(() => vi.advanceTimersByTime(RESULT_ACTION_GUARD_MS));
  expect(reveal).toBeEnabled();
  fireEvent.click(reveal, { detail: 2 });
  expect(screen.getByRole('region', { name: '斗地主牌桌' })).toBeVisible();
  act(() => vi.advanceTimersByTime(1000 - RESULT_ACTION_GUARD_MS));
  expect(screen.getByRole('timer')).toHaveTextContent('4 秒后自动结算');
  fireEvent.click(reveal); // Keyboard/assistive activation also works (detail = 0).
  expect(screen.getByRole('region', { name: '获胜最后一手' })).toBeVisible();
  expect(useAppStore.getState().resultRevealAt).toBeNull();
  expect(send).not.toHaveBeenCalled();
  expect(vi.getTimerCount()).toBe(0);
  act(() => result());
  act(() => vi.advanceTimersByTime(FINAL_PLAY_DURATION_MS));
  expect(screen.queryByRole('button', { name: '查看结算' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: '再来一局' })).toBeVisible();
});
