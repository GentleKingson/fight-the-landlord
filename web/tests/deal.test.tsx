import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { useAppStore } from '../src/stores/appStore';
import { GameTable } from '../src/features/table/GameTable';
import { MsgType } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

const cards = Array.from({ length: 17 }, (_, i) => ({ rank: i % 13 + 3, suit: i % 4, color: 0 }));
const players = ['me', 'left', 'right'].map((id, seat) => ({ id, seat, name: id, cards_count: 17, is_landlord: false, online: true, ready: true }));
const send = vi.fn();
function receive(type: string, payload: unknown) { act(() => useAppStore.getState().handleMessage({ type, payload })); }
function deal(id = 'round-1', elapsed = 0) {
  // Deliberately different server/client clocks.
  receive(MsgType.DealCards, { cards, bottom_cards: [], deal_id: id, deal_start_at: 100000, deal_end_at: 103870, server_time: 100000 + elapsed });
}
beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  vi.useFakeTimers(); vi.setSystemTime(500000); send.mockClear();
  useAppStore.getState().leaveLocalRoom();
  useAppStore.setState({ connected: true, playerId: 'me', players, phase: 'bidding' });
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

test('deal spends no bidding time and only the server turn opens actions with its token', () => {
  receive(MsgType.BidTurn, { player_id: 'me', timeout: 15 });
  deal();
  render(<GameTable socket={{ send } as unknown as GameSocket} />);
  expect(useAppStore.getState().timeout).toBe(0);
  expect(useAppStore.getState().currentTurn).toBe('');
  expect(screen.queryByRole('button', { name: '叫地主' })).toBeNull();
  expect(document.querySelectorAll('.deal-card')).toHaveLength(52);
  act(() => vi.advanceTimersByTime(4000));
  expect(document.querySelectorAll('.deal-card')).toHaveLength(0);
  expect(screen.getByText('等待叫地主通知…')).toBeTruthy();
  expect(useAppStore.getState().phase).toBe('dealing');
  expect(screen.queryByRole('button', { name: '叫地主' })).toBeNull();
  receive(MsgType.BidTurn, { player_id: 'me', timeout: 15, bid_token: 'turn-1' });
  expect(screen.queryByLabelText('正在发牌')).toBeNull();
  expect(useAppStore.getState().timerStart).toBe(Date.now());
  fireEvent.click(screen.getByRole('button', { name: '叫地主' }));
  expect(send).toHaveBeenCalledWith(MsgType.Bid, { bid: true, bid_token: 'turn-1' });
});

test('an early official bid turn immediately cancels presentation', () => {
  deal(); render(<GameTable socket={{ send } as unknown as GameSocket} />);
  receive(MsgType.BidTurn, { player_id: 'me', timeout: 15 });
  expect(screen.queryByLabelText('正在发牌')).toBeNull();
  expect(screen.getByRole('button', { name: '叫地主' })).toBeTruthy();
});

test('redeals reset old bid state; duplicate deals and landlord hand updates do not replay', () => {
  deal(); const original = useAppStore.getState().deal;
  act(() => vi.advanceTimersByTime(1000)); deal();
  expect(useAppStore.getState().deal).toBe(original);
  receive(MsgType.BidTurn, { player_id: 'me', timeout: 15, bid_token: 'old' });
  deal('round-2');
  expect(useAppStore.getState().deal?.id).toBe('round-2');
  expect(useAppStore.getState().bidToken).toBe('');
  receive(MsgType.BidTurn, { player_id: 'me', timeout: 15, bid_token: 'new' });
  receive(MsgType.Landlord, { player_id: 'me', bottom_cards: cards.slice(0, 3) });
  receive(MsgType.DealCards, { cards: [...cards, ...cards.slice(0, 3)], bottom_cards: cards.slice(0, 3) });
  expect(useAppStore.getState().deal).toBeNull();
  expect(useAppStore.getState().bottomCardsRevealed).toBe(true);
});

test('dealing reconnect resumes remaining time, hides bottom cards, and clears old controls', () => {
  receive(MsgType.Reconnected, { player_id: 'me', player_name: 'Me', game_state: {
    phase: 'dealing', players, hand: cards, bottom_cards: [], deal_id: 'round-1',
    deal_start_at: 100000, deal_end_at: 103870, server_time: 102000
  } });
  expect(useAppStore.getState().deal).toEqual({ id: 'round-1', startAt: 498000, endAt: 501870 });
  expect(useAppStore.getState().bottomCardsRevealed).toBe(false);
  render(<GameTable socket={{ send } as unknown as GameSocket} />);
  act(() => vi.advanceTimersByTime(1870));
  expect(document.querySelectorAll('.deal-card')).toHaveLength(0);
  expect(useAppStore.getState().phase).toBe('dealing');
  receive(MsgType.Reconnected, { player_id: 'me', game_state: { phase: 'bidding', players, hand: cards, bottom_cards: [] } });
  expect(useAppStore.getState().deal).toBeNull();
});
