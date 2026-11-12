import { act, cleanup, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { GameTable } from '../src/features/table/GameTable';
import { useAppStore } from '../src/stores/appStore';
import { seedDemoState } from '../src/stores/demoState';
import { MsgType } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

vi.mock('../src/shared/cards/Hand', () => ({ Hand: () => null }));
beforeEach(() => seedDemoState('table'));
afterEach(cleanup);
const renderTable = () => render(<GameTable socket={{ send: vi.fn() } as unknown as GameSocket} />);
const receive = useAppStore.getState().handleMessage;
const target = () => screen.queryByText('待压过');
const turn = (player_id: string, must_play = false) => receive({ type: MsgType.PlayTurn,
  payload: { player_id, must_play, can_beat: true, timeout: 30 } });
const pass = (player_id: string) => receive({ type: MsgType.PlayerPass, payload: { player_id } });

it('moves the unique target after a reply, retains it after a pass and clears it for a new trick', () => {
  renderTable();
  expect(within(screen.getByRole('region', { name: '松风的出牌' })).getByText('待压过')).toBeTruthy();
  expect(screen.getByText('跟牌目标：松风')).toBeTruthy();
  act(() => {
    receive({ type: MsgType.CardPlayed, payload: { player_id: 'p1', player_name: '青竹',
      cards: [{ rank: 9, suit: 0, color: 0 }, { rank: 9, suit: 1, color: 1 }], hand_type: '对子', cards_left: 15 } });
    turn('p2');
  });
  expect(screen.getAllByText('待压过')).toHaveLength(1);
  const ownCards = screen.getByRole('region', { name: '青竹的出牌' });
  expect(within(ownCards).getByText('待压过')).toBeTruthy();
  expect(ownCards.classList.contains('played-seat--target')).toBe(true);
  act(() => { pass('p2'); turn('p3'); });
  expect(within(ownCards).getByText('待压过')).toBeTruthy();
  act(() => { pass('p3'); turn('p1', true); });
  expect(target()).toBeNull();
  expect(ownCards.classList.contains('played-seat--target')).toBe(false);
  expect(within(ownCards).getAllByRole('img')).toHaveLength(2);
  expect(screen.getByText('新一轮，请出牌')).toBeTruthy();
});

it.each(['bidding', 'game_over'] as const)('does not mark retained cards during %s', phase => {
  useAppStore.setState({ phase });
  renderTable();
  expect(target()).toBeNull();
  expect(screen.queryByText(/^跟牌目标：/)).toBeNull();
});

it('announces the target rather than stale bidding text and excludes the timer from announcements', () => {
  useAppStore.setState({ recentActions: [{ type: 'bid', player_id: 'p2', player_name: '山月', label: '叫地主' }] });
  renderTable();
  const status = screen.getByText('跟牌目标：松风').closest<HTMLElement>('[role="status"]')!;
  expect(status).not.toBeNull();
  expect(within(status).getByText('跟牌目标：松风')).toBeTruthy();
  expect(status.querySelector('time')).toBeNull();
  expect(screen.queryByText('山月：叫地主')).toBeNull();
});

it.each([false, true])('restores the target from a reconnect snapshot (new trick: %s)', must_play => {
  const state = useAppStore.getState();
  receive({ type: MsgType.Reconnected, payload: { player_id: 'p1', game_state: {
    phase: 'playing', players: state.players, hand: state.hand, current_turn: 'p1',
    last_played: state.lastPlayed, last_player_id: 'p3', last_hand_type: '对子',
    must_play, can_beat: true, multiplier: 3
  } } } as never);
  renderTable();
  if (must_play) {
    expect(target()).toBeNull();
    expect(screen.getByText('新一轮，请出牌')).toBeTruthy();
  } else {
    expect(within(screen.getByRole('region', { name: '松风的出牌' })).getByText('待压过')).toBeTruthy();
    expect(screen.getByText('跟牌目标：松风')).toBeTruthy();
  }
});
