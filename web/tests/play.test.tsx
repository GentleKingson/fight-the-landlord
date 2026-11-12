import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { GameTable } from '../src/features/table/GameTable';
import { useAppStore } from '../src/stores/appStore';
import { MsgType } from '../src/protocol/types';
import { cardKey } from '../src/shared/cards/cardModel';
import type { GameSocket } from '../src/transport/wsClient';

vi.mock('../src/shared/cards/Hand', () => ({ Hand: () => null }));
afterEach(cleanup);

it.each([
  { name: 'empty selection', ranks: [], target: [], mustPlay: true, allowed: false },
  { name: 'invalid opening hand', ranks: [3, 4], target: [], mustPlay: true, allowed: false },
  { name: 'invalid reply', ranks: [3, 4], target: [3], mustPlay: false, allowed: false },
  { name: 'lower reply', ranks: [3], target: [4], mustPlay: false, allowed: false },
  { name: 'equal reply', ranks: [4], target: [4], mustPlay: false, allowed: false },
  { name: 'different hand type', ranks: [5, 5], target: [4], mustPlay: false, allowed: false },
  { name: 'higher reply', ranks: [5], target: [4], mustPlay: false, allowed: true },
  { name: 'bomb over a pair', ranks: [3, 3, 3, 3], target: [4, 4], mustPlay: false, allowed: true },
  { name: 'rocket over a bomb', ranks: [16, 17], target: [4, 4, 4, 4], mustPlay: false, allowed: true },
  { name: 'bomb below a rocket', ranks: [3, 3, 3, 3], target: [16, 17], mustPlay: false, allowed: false },
  { name: 'opening hand with no previous play', ranks: [3], target: [], mustPlay: true, allowed: true },
  { name: 'new trick with previous cards retained', ranks: [3], target: [4, 4], mustPlay: true, allowed: true },
  { name: 'another player turn', ranks: [5], target: [4], mustPlay: false, allowed: false, currentTurn: 'b' }
])('validates $name before enabling and submitting a play', ({ ranks, target, mustPlay, allowed, currentTurn }) => {
  const cards = (values: number[]) => values.map((rank, index) => ({ rank, suit: index % 4, color: 0 }));
  const hand = cards(ranks);
  useAppStore.setState({ turnToken: 'turn-test', phase: 'playing', connected: true, submission: 'idle', playerId: 'a', currentTurn: currentTurn ?? 'a', hand,
    selectedCards: new Set(hand.map(cardKey)), mustPlay, canBeat: true, players: [],
    lastPlayed: cards(target), drawer: 'none', tableMessage: '' });
  const send = vi.fn();
  render(<GameTable socket={{ send } as unknown as GameSocket} />);
  const button = screen.getByRole('button', { name: /^出牌$/ }) as HTMLButtonElement;
  expect(button.disabled).toBe(!allowed);
  if (ranks.length === 2 && ranks[0] !== ranks[1] && ranks[1] < 16) {
    expect(screen.getByText('牌型错误，请重新选牌')).toBeDefined();
  }
  fireEvent.click(button);
  if (allowed) {
    expect(send).toHaveBeenCalledWith(MsgType.PlayCards, { cards: hand, turn_token: 'turn-test' });
  } else {
    expect(send).not.toHaveBeenCalled();
  }
});

it('allows manually selected replies regardless of the advisory can_beat flag', () => {
  const hand = [4, 5, 6, 7, 8].map(rank => ({ rank, suit: 0, color: 0 }));
  useAppStore.setState({ turnToken: 'turn-test', phase: 'playing', connected: true, submission: 'idle', playerId: 'a', currentTurn: 'a', hand,
    selectedCards: new Set(hand.map(cardKey)), mustPlay: false, canBeat: false, players: [],
    lastPlayed: [3, 4, 5, 6, 7].map(rank => ({ rank, suit: 1, color: 1 })), drawer: 'none', tableMessage: '' });
  const send = vi.fn();
  render(<GameTable socket={{ send } as unknown as GameSocket} />);
  fireEvent.click(screen.getByRole('button', { name: /^出牌$/ }));
  expect(send).toHaveBeenCalledWith(MsgType.PlayCards, { cards: hand, turn_token: 'turn-test' });
  fireEvent.click(screen.getByRole('button', { name: /^提示$/ }));
  expect(useAppStore.getState().tableMessage).toBe('提示 1/1：顺子');
});

it.each([0, 1, 2])('keeps previous player left and next player right with their played cards (self seat %i)', (seat) => {
  useAppStore.setState({
    turnToken: 'turn-test', phase: 'playing', connected: true, submission: 'idle',
    playerId: 'p1',
    currentTurn: 'p1',
    hand: [],
    mustPlay: false,
    selectedCards: new Set(),
    seatActions: {},
    drawer: 'none',
    tableMessage: '',
    players: [
      { id: 'p1', name: '我', seat, ready: true, is_landlord: false, cards_count: 17, online: true },
      { id: 'p2', name: '山月', seat: (seat + 1) % 3, ready: true, is_landlord: true, cards_count: 20, online: true },
      { id: 'p3', name: '松风', seat: (seat + 2) % 3, ready: true, is_landlord: false, cards_count: 17, online: true }
    ]
  });
  const receive = useAppStore.getState().handleMessage;
  receive({ type: MsgType.CardPlayed, payload: { player_id: 'p2', player_name: '山月', cards: [{ rank: 9, suit: 1, color: 1 }], cards_left: 19, hand_type: '单张' } });
  receive({ type: MsgType.CardPlayed, payload: { player_id: 'p3', player_name: '松风', cards: [{ rank: 10, suit: 0, color: 0 }], cards_left: 16, hand_type: '单张' } });

  render(<GameTable socket={{ send: vi.fn() } as unknown as GameSocket} />);
  const left = screen.getByRole('region', { name: '松风的出牌' });
  const right = screen.getByRole('region', { name: '山月的出牌' });
  expect(left.classList.contains('played-seat--left')).toBe(true);
  expect(right.classList.contains('played-seat--right')).toBe(true);
  expect(within(left).getByRole('button', { name: '10♠' })).toBeDefined();
  expect(within(right).getByRole('button', { name: '9♥' })).toBeDefined();
});

it('retains preselection and revalidates against another player’s new play', () => {
  const card = { rank: 8, suit: 0, color: 0 };
  useAppStore.setState({ phase: 'playing', connected: true, submission: 'idle', playerId: 'a',
    currentTurn: 'b', hand: [card], selectedCards: new Set([cardKey(card)]), players: [],
    mustPlay: false, lastPlayed: [], drawer: 'none', tableMessage: '' });
  const receive = useAppStore.getState().handleMessage;
  receive({ type: MsgType.CardPlayed, payload: { player_id: 'b', cards: [{ rank: 9, suit: 1, color: 1 }], cards_left: 1 } });
  receive({ type: MsgType.PlayTurn, payload: { player_id: 'a', must_play: false, timeout: 20 } });
  render(<GameTable socket={{ send: vi.fn() } as unknown as GameSocket} />);
  expect(useAppStore.getState().selectedCards.has(cardKey(card))).toBe(true);
  expect(screen.getByText('点数不足，需要大于当前出牌')).toBeDefined();
  expect((screen.getByRole('button', { name: '出牌' }) as HTMLButtonElement).disabled).toBe(true);
  cleanup();
  receive({ type: MsgType.CardPlayed, payload: { player_id: 'b', cards: [{ rank: 7, suit: 1, color: 1 }], cards_left: 0 } });
  render(<GameTable socket={{ send: vi.fn() } as unknown as GameSocket} />);
  expect((screen.getByRole('button', { name: '出牌' }) as HTMLButtonElement).disabled).toBe(false);
  receive({ type: MsgType.CardPlayed, payload: { player_id: 'a', cards: [card], cards_left: 0 } });
  expect(useAppStore.getState().selectedCards.size).toBe(0);
});

it.each([
  { connected: false, submission: 'idle' as const, text: '网络已断开，请等待连接恢复' },
  { connected: true, submission: 'pending' as const, text: '等待服务器确认，请勿重复提交' },
  { connected: true, submission: 'unknown' as const, text: '操作结果待核对，请勿重复提交' }
])('explains blocking state $submission / $connected', ({ connected, submission, text }) => {
  useAppStore.setState({ phase: 'playing', connected, submission, playerId: 'a', currentTurn: 'a',
    hand: [], players: [], selectedCards: new Set(), drawer: 'none', tableMessage: '旧提示' });
  render(<GameTable socket={{ send: vi.fn() } as unknown as GameSocket} />);
  expect(screen.getByText(text)).toBeDefined();
  expect(screen.getByRole('button', { name: '出牌' }).getAttribute('aria-describedby')).toBe('play-action-summary');
});
