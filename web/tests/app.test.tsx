import { fireEvent, render, screen, cleanup } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { useAppStore } from '../src/stores/appStore';

vi.mock('../src/transport/wsClient', () => ({ createGameSocket: () => ({ connect() {}, close() {}, send() {}, subscribe: () => () => {} }) }));
vi.mock('../src/features/lobby/Lobby', () => ({ Lobby: () => <div>大厅</div> }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('shows and dismisses business errors while connected', () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }));
  useAppStore.setState({ connected: true, phase: 'lobby', error: '房间不存在' });
  render(<App />);
  expect(screen.getByRole('alert')).toHaveTextContent('房间不存在');
  fireEvent.click(screen.getByRole('button', { name: '关闭' }));
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});
