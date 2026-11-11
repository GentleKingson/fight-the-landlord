import { fireEvent, render, screen, cleanup } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { StrictMode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { useAppStore } from '../src/stores/appStore';

const { connect } = vi.hoisted(() => ({ connect: vi.fn() }));
vi.mock('../src/transport/wsClient', () => ({ createGameSocket: () => ({ connect, close() {}, send() {}, subscribe: () => () => {} }) }));
vi.mock('../src/features/lobby/Lobby', () => ({ Lobby: () => <div>大厅</div> }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.clearAllMocks(); });

it.each([null, 'lobby'])('connects only outside demo mode (%s) under StrictMode', (demo) => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }));
  useAppStore.setState({ connected: false, phase: 'lobby', error: '' });
  render(<StrictMode><App demo={demo} /></StrictMode>);
  if (demo) {
    expect(connect).not.toHaveBeenCalled();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  } else {
    expect(connect).toHaveBeenCalled();
    expect(screen.getByRole('status')).toHaveTextContent('正在连接服务器');
  }
});

it('shows and dismisses business errors while connected', () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }));
  useAppStore.setState({ connected: true, phase: 'lobby', error: '房间不存在' });
  render(<App />);
  expect(screen.getByRole('alert')).toHaveTextContent('房间不存在');
  fireEvent.click(screen.getByRole('button', { name: '关闭' }));
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});
