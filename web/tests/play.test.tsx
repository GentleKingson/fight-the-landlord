import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { GameTable } from '../src/features/table/GameTable';
import { useAppStore } from '../src/stores/appStore';
import { MsgType } from '../src/protocol/types';
import { cardKey } from '../src/shared/cards/cardModel';
import type { GameSocket } from '../src/transport/wsClient';

vi.mock('../src/shared/cards/Hand', () => ({ Hand: () => null }));
afterEach(cleanup);

it('allows manually selected replies regardless of the advisory can_beat flag', () => {
  const hand = [4, 5, 6, 7, 8].map(rank => ({ rank, suit: 0, color: 0 }));
  useAppStore.setState({ phase: 'playing', playerId: 'a', currentTurn: 'a', hand,
    selectedCards: new Set(hand.map(cardKey)), mustPlay: false, canBeat: false, players: [],
    lastPlayed: [3, 4, 5, 6, 7].map(rank => ({ rank, suit: 1, color: 1 })), drawer: 'none', tableMessage: '' });
  const send = vi.fn();
  render(<GameTable socket={{ send } as unknown as GameSocket} />);
  fireEvent.click(screen.getByRole('button', { name: /^出牌$/ }));
  expect(send).toHaveBeenCalledWith(MsgType.PlayCards, { cards: hand });
  fireEvent.click(screen.getByRole('button', { name: /^提示$/ }));
  expect(useAppStore.getState().tableMessage).toBe('提示：顺子');
});

it.each([0, 1, 2])('keeps previous player left and next player right with their played cards (self seat %i)', (seat) => {
  useAppStore.setState({
    phase: 'playing',
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
