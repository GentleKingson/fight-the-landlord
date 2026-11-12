import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { GameResult } from '../src/features/table/GameResult';
import { loadMatchMode, useAppStore } from '../src/stores/appStore';
import { MsgType } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

afterEach(cleanup);

it('replays practice after reloading and reconnecting, then allows switching to online', () => {
  sessionStorage.clear();
  useAppStore.setState({ matchMode: 'online' });
  useAppStore.setState({ matchMode: 'practice' });
  const restoredMode = loadMatchMode();
  expect(restoredMode).toBe('practice');
  useAppStore.setState({ matchMode: restoredMode });
  useAppStore.getState().handleMessage({ type: MsgType.Reconnected, payload: { player_id: 'p1', room_code: '123456', game_state: { phase: 'ended', players: [], hand: [] } } });
  useAppStore.getState().handleMessage({ type: MsgType.GameOver, payload: { winner_name: 'p1' } });
  const send = vi.fn().mockReturnValue(true);
  render(<GameResult socket={{ send } as unknown as GameSocket} />);
  fireEvent.click(screen.getByRole('button', { name: '再来一局' }));
  expect(send).toHaveBeenLastCalledWith(MsgType.PracticeMatch);
  useAppStore.setState({ matchMode: 'online', phase: 'game_over' });
  expect(loadMatchMode()).toBe('online');
  fireEvent.click(screen.getByRole('button', { name: '再来一局' }));
  expect(send).toHaveBeenLastCalledWith(MsgType.QuickMatch);
});


it('keeps the result available when replay matching fails', () => {
  useAppStore.setState({ phase: 'game_over', error: '' });
  render(<GameResult socket={{ send: vi.fn().mockReturnValue(false) } as unknown as GameSocket} />);
  fireEvent.click(screen.getByRole('button', { name: '再来一局' }));
  expect(useAppStore.getState().phase).toBe('game_over');
  expect(useAppStore.getState().error).toContain('匹配请求发送失败');
});
