import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { Lobby } from '../src/features/lobby/Lobby';
import { useAppStore } from '../src/stores/appStore';
import { MsgType } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

afterEach(cleanup);

it('enters matching immediately and sends only one practice request on repeated clicks', () => {
  useAppStore.setState({ phase: 'lobby', lobbyPanel: 'home', matchMode: 'online' });
  const send = vi.fn();
  render(<Lobby socket={{ send } as unknown as GameSocket} />);
  const button = screen.getByRole('button', { name: /人机练习/ });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(useAppStore.getState().phase).toBe('matching');
  expect(useAppStore.getState().matchMode).toBe('practice');
  expect(screen.queryByRole('button', { name: /人机练习/ })).toBeNull();
  expect(send).toHaveBeenCalledTimes(1);
  expect(send).toHaveBeenCalledWith(MsgType.PracticeMatch);
});
