import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { Lobby } from '../src/features/lobby/Lobby';
import { GameTable } from '../src/features/table/GameTable';
import { GameResult } from '../src/features/table/GameResult';
import { useAppStore } from '../src/stores/appStore';
import { seedDemoState } from '../src/stores/demoState';
import { MsgType, type IncomingMessage } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  seedDemoState('table');
  useAppStore.setState({ cancellingMatch: false, error: '', tableMessage: '' });
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });
function transport() {
  let listener: (message: IncomingMessage) => void = () => {};
  const send = vi.fn(() => true);
  const socket = { send, subscribe: (fn: typeof listener) => { listener = fn; return () => { listener = () => {}; }; } } as unknown as GameSocket;
  return { socket, send, receive: (message: IncomingMessage) => act(() => {
    useAppStore.getState().handleMessage(message);
    listener(message);
  }) };
}

it('keeps matching until the server confirms cancellation and keeps a reserved match on rejection', () => {
  const { socket, send, receive } = transport();
  useAppStore.setState({ phase: 'matching', matchMode: 'online' });
  render(<Lobby socket={socket} />);
  fireEvent.click(screen.getByRole('button', { name: '取消匹配' }));
  expect(send).toHaveBeenCalledWith(MsgType.CancelMatch);
  expect(useAppStore.getState().phase).toBe('matching');
  expect(screen.getByRole('button', { name: '取消匹配' })).toBeDisabled();
  receive({ type: MsgType.Error, payload: { code: 1, message: '正在组桌' } });
  expect(useAppStore.getState().phase).toBe('matching');
  fireEvent.click(screen.getByRole('button', { name: '取消匹配' }));
  receive({ type: MsgType.MatchCanceled, payload: {} });
  expect(useAppStore.getState().phase).toBe('lobby');
});

it('distinguishes loading, empty response, timeout and retry in the leaderboard', () => {
  vi.useFakeTimers();
  const { socket, receive, send } = transport();
  useAppStore.setState({ phase: 'lobby', lobbyPanel: 'leaderboard', leaderboard: [] });
  render(<Lobby socket={socket} />);
  expect(screen.getByText('正在加载…')).toBeVisible();
  expect(screen.queryByText(/尚无玩家积分/)).toBeNull();
  receive({ type: MsgType.LeaderboardResult, payload: { entries: [], type: 'total' } });
  expect(screen.getByText(/尚无玩家积分/)).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: '重新加载' }));
  act(() => vi.advanceTimersByTime(10000));
  expect(screen.getByText('请求超时，请重试')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: '重新加载' }));
  expect(send).toHaveBeenCalledTimes(3);
  expect(screen.getByRole('button', { name: '战绩榜' })).toHaveAttribute('aria-current', 'page');
});

it('shows pending roles, read-only faces, low cards, disconnected status and only a final-five-second pulse', () => {
  vi.useFakeTimers();
  const { socket } = transport();
  useAppStore.setState({ phase: 'bidding', timerStart: Date.now(), timeout: 20 });
  const { container } = render(<GameTable socket={socket} />);
  expect(screen.getAllByText(/身份待定/)).toHaveLength(2);
  expect(within(screen.getByLabelText('底牌')).queryAllByRole('button')).toHaveLength(0);
  act(() => useAppStore.setState({ phase: 'playing', currentTurn: useAppStore.getState().playerId, players: useAppStore.getState().players.map(p => ({ ...p, cards_count: 2 })) }));
  expect(container.querySelectorAll('.cards-warning').length).toBeGreaterThanOrEqual(2);
  expect(container.querySelector('.turn-banner')).not.toHaveClass('is-urgent');
  act(() => vi.advanceTimersByTime(15200));
  expect(container.querySelector('.turn-banner')).toHaveClass('is-urgent');
  act(() => { useAppStore.setState({ latency: 88 }); useAppStore.getState().setConnected(false); });
  expect(screen.getByText('连接中断')).toBeVisible();
  expect(useAppStore.getState().latency).toBe(0);
  expect(screen.queryByText('88ms')).toBeNull();
});

it('announces a teammate win as your victory', () => {
  const { socket } = transport();
  useAppStore.setState({ playerId: 'me', winnerId: 'ally', winnerName: '队友', winnerIsLandlord: false,
    scores: [{ player_id: 'me', player_name: '我', is_landlord: false, score: 3 }], matchMode: 'online' });
  render(<GameResult socket={socket} />);
  expect(screen.getByRole('heading', { name: '你赢了' })).toBeVisible();
  expect(screen.getByRole('button', { name: '重新匹配' })).toBeVisible();
});

it('does not reopen a lobby panel when its response arrives after navigation', () => {
  const { socket, receive } = transport();
  useAppStore.setState({ phase: 'lobby', lobbyPanel: 'leaderboard' });
  render(<Lobby socket={socket} />);
  fireEvent.click(screen.getByRole('button', { name: '大厅' }));
  receive({ type: MsgType.LeaderboardResult, payload: { entries: [], type: 'total' } });
  expect(useAppStore.getState().lobbyPanel).toBe('home');
});
