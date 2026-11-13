import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { GameTable } from '../src/features/table/GameTable';
import { useAppStore } from '../src/stores/appStore';
import { seedDemoState } from '../src/stores/demoState';
import { cardKey } from '../src/shared/cards/cardModel';
import { MsgType, type CardInfo } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

vi.mock('../src/shared/cards/Hand', () => ({ Hand: () => null }));
const card = (rank: number, suit = 0): CardInfo => ({ rank, suit, color: suit % 2 });
beforeEach(() => {
  seedDemoState('table');
  useAppStore.setState({ hand: [card(5), card(5, 1), card(6), card(6, 1), card(7), card(7, 1)],
    selectedCards: new Set(), lastPlayed: [card(4), card(4, 1)], turnToken: 'one', mustPlay: false });
});
afterEach(cleanup);
const hint = () => fireEvent.click(screen.getByRole('button', { name: /^提示$/ }));
const selectedRanks = () => {
  const state = useAppStore.getState();
  return state.hand.filter(card => state.selectedCards.has(cardKey(card))).map(card => card.rank);
};
function setup() {
  const send = vi.fn();
  render(<GameTable socket={{ send } as unknown as GameSocket} />);
  return send;
}

it('cycles all suggestions, wraps, and sends only when the player confirms', () => {
  const send = setup();
  for (const [rank, position] of [[5, 1], [6, 2], [7, 3], [5, 1]]) {
    hint();
    expect(selectedRanks()).toEqual([rank, rank]);
    expect(screen.getByText(`提示 ${position}/3：对子 · 再点切换`)).toBeTruthy();
  }
  expect(send).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: /^出牌$/ }));
  expect(send).toHaveBeenCalledWith(MsgType.PlayCards, { cards: [card(5), card(5, 1)], turn_token: 'one' });
});

it('clears stale text on manual range selection, advances from a matching hand, and restarts after reset', () => {
  setup();
  hint();
  act(() => useAppStore.getState().setSelection([cardKey(card(6)), cardKey(card(6, 1))]));
  expect(useAppStore.getState().tableMessage).toBe('');
  hint();
  expect(selectedRanks()).toEqual([7, 7]);
  act(() => useAppStore.getState().toggleCard(cardKey(card(7))));
  expect(useAppStore.getState().tableMessage).toBe('');
  hint();
  expect(selectedRanks()).toEqual([5, 5]);
  hint();
  fireEvent.click(screen.getByRole('button', { name: '重选' }));
  hint();
  expect(selectedRanks()).toEqual([5, 5]);
});

it('matches equivalent suits without repeating the same rank combination', () => {
  useAppStore.setState({ hand: [card(5), card(5, 1), card(5, 2), card(6), card(6, 1)] });
  setup();
  hint();
  act(() => useAppStore.getState().setSelection([cardKey(card(5, 1)), cardKey(card(5, 2))]));
  hint();
  expect(selectedRanks()).toEqual([6, 6]);
});

it.each(['turn', 'hand', 'target', 'freeLead', 'round', 'reconnect'])('resets the session after %s changes', change => {
  setup(); hint(); hint();
  act(() => {
    const state = useAppStore.getState();
    if (change === 'turn') state.handleMessage({ type: MsgType.PlayTurn,
      payload: { player_id: 'p1', turn_token: 'two', must_play: false, can_beat: true, timeout: 30 } });
    if (change === 'hand') useAppStore.setState({ hand: [...state.hand, card(8), card(8, 1)] });
    if (change === 'target') useAppStore.setState({ lastPlayed: [card(3), card(3, 1)] });
    if (change === 'freeLead') useAppStore.setState({ mustPlay: true });
    if (change === 'round') useAppStore.setState({ roundId: 'new-round' });
    if (change === 'reconnect') state.handleMessage({ type: MsgType.Reconnected, payload: { player_id: 'p1', game_state: {
      phase: 'playing', players: state.players, hand: state.hand, current_turn: 'p1', turn_token: 'one',
      last_played: state.lastPlayed, last_player_id: 'p3', must_play: false, can_beat: true
    } } } as never);
  });
  hint();
  expect(selectedRanks()).toEqual(change === 'freeLead' ? [5] : [5, 5]);
});

it('cycles freely led hands including pairs and sequences', () => {
  useAppStore.setState({ hand: [card(3), card(3, 1), card(4), card(5), card(6), card(7)], mustPlay: true });
  setup();
  const signatures = new Set<string>();
  for (let i = 0; i < 8; i++) { hint(); signatures.add(selectedRanks().join(',')); }
  expect(signatures).toContain('3,3');
  expect(signatures).toContain('3,4,5,6,7');
});

it('handles one or no suggestion without pretending there is another choice', () => {
  const rejection = { submission: 'rejected', submissionMessage: '上次操作未发送' } as const;
  useAppStore.setState({ hand: [card(5), card(5, 1)], ...rejection });
  setup(); hint(); hint();
  expect(selectedRanks()).toEqual([5, 5]);
  expect(screen.getByText('提示 1/1：对子')).toBeTruthy();
  act(() => useAppStore.setState({ lastPlayed: [card(8), card(8, 1)], selectedCards: new Set(), ...rejection }));
  hint();
  expect(selectedRanks()).toEqual([]);
  expect(screen.getByText('暂无提示，请手动选牌')).toBeTruthy();
});
