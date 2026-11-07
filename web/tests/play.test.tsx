import { cleanup, fireEvent, render, screen } from '@testing-library/react';
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
  expect(useAppStore.getState().tableMessage).toBe('暂无提示，请手动选牌');
});
