import { act, fireEvent, render, screen, cleanup } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { StrictMode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { useAppStore } from '../src/stores/appStore';

const { connect } = vi.hoisted(() => ({ connect: vi.fn() }));
vi.mock('../src/transport/wsClient', () => ({ createGameSocket: () => ({ connect, close() {}, send() {}, subscribe: () => () => {} }) }));
vi.mock('../src/features/lobby/Lobby', () => ({ Lobby: () => <div>大厅</div> }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.clearAllMocks(); vi.useRealTimers(); useAppStore.setState({ submission: 'idle', submissionMessage: '' }); });

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


it('clears successful feedback after three seconds and on returning to lobby', () => {
  vi.useFakeTimers();
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }));
  useAppStore.setState({ connected: true, phase: 'lobby', error: '', submission: 'confirmed', submissionMessage: '服务器已确认操作成功' });
  render(<App demo="lobby" />);
  expect(screen.getByRole('status')).toHaveTextContent('服务器已确认操作成功');
  act(() => vi.advanceTimersByTime(3000));
  expect(useAppStore.getState().submissionMessage).toBe('');
  act(() => {
    useAppStore.setState({ submission: 'confirmed', submissionMessage: '服务器已确认操作成功' });
    useAppStore.getState().leaveLocalRoom();
  });
  expect(useAppStore.getState().submission).toBe('idle');
  expect(screen.queryByText('服务器已确认操作成功')).not.toBeInTheDocument();
});

it('does not dismiss unresolved feedback or let an old success timer clear it', () => {
  vi.useFakeTimers();
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }));
  useAppStore.setState({ connected: true, phase: 'lobby', error: '', submission: 'confirmed', submissionMessage: '成功' });
  render(<App demo="lobby" />);
  act(() => useAppStore.setState({ submission: 'unknown', submissionMessage: '结果待核对' }));
  act(() => vi.advanceTimersByTime(3000));
  expect(screen.getByText('结果待核对')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '关闭提示' })).not.toBeInTheDocument();
  act(() => useAppStore.getState().leaveLocalRoom());
  expect(useAppStore.getState().submission).toBe('unknown');
});
