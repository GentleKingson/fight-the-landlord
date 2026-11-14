import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, expect, it, vi } from 'vitest';
import { Lobby } from '../src/features/lobby/Lobby';
import { useAppStore } from '../src/stores/appStore';
import { MsgType } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';
import landlordLogo from '../src/assets/landlord-logo-176.webp';

afterEach(cleanup);

it.each(['lobby', 'matching', 'waiting'] as const)('shows the brand image beside the title while %s', (phase) => {
  useAppStore.setState({ phase, lobbyPanel: 'home', players: [] });
  const { container } = render(<Lobby socket={{ subscribe: () => () => {}, send: vi.fn() } as unknown as GameSocket} />);
  const logo = container.querySelector<HTMLImageElement>('.lobby-header .brand-mark');

  expect(logo?.tagName).toBe('IMG');
  expect(logo?.getAttribute('src')).toBe(landlordLogo);
  // The adjacent heading supplies the name, so the image is decorative.
  expect(logo?.getAttribute('alt')).toBe('');
  expect(logo?.getAttribute('width')).toBe('44');
  expect(logo?.getAttribute('height')).toBe('44');
  expect(screen.getByRole('heading', { name: '斗地主', level: 1 })).toBeTruthy();
});

it('enters matching immediately and sends only one practice request on repeated clicks', () => {
  useAppStore.setState({ phase: 'lobby', lobbyPanel: 'home', matchMode: 'online' });
  const send = vi.fn().mockReturnValue(true);
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

it('shows a vacant middle seat without duplicating the third player', () => {
  useAppStore.setState({ phase: 'waiting', lobbyPanel: 'home', players: [
    { id: 'a', name: 'A', seat: 0, ready: false, is_landlord: false, online: true, cards_count: 0 },
    { id: 'c', name: 'C', seat: 2, ready: false, is_landlord: false, online: true, cards_count: 0 }
  ] });
  render(<Lobby socket={{ subscribe: () => () => {}, send: vi.fn() } as unknown as GameSocket} />);
  expect(screen.getAllByText('C')).toHaveLength(1);
  expect(screen.getAllByText('等待加入')).toHaveLength(1);
});

it.each([/快速开局/, /人机练习/])('keeps the lobby when %s cannot be sent', (name) => {
  useAppStore.setState({ phase: 'lobby', lobbyPanel: 'home', error: '' });
  render(<Lobby socket={{ send: vi.fn().mockReturnValue(false) } as unknown as GameSocket} />);
  fireEvent.click(screen.getByRole('button', { name }));
  expect(useAppStore.getState().phase).toBe('lobby');
  expect(useAppStore.getState().error).toContain('匹配请求发送失败');
});

it('leaves matching when the connection drops', () => {
  useAppStore.setState({ phase: 'matching', connected: true });
  useAppStore.getState().setConnected(false);
  expect(useAppStore.getState().phase).toBe('lobby');
});

it.each(['waiting', 'matching'] as const)('explains why lobby navigation is unavailable while %s', phase => {
  useAppStore.setState({ phase, lobbyPanel: 'rules', matchMode: 'online', connected: true });
  render(<Lobby socket={{ send: vi.fn() } as unknown as GameSocket} />);
  expect(screen.queryByRole('button', { name: '聊天' })).toBeNull();
  const rules = screen.getByRole('button', { name: '规则' });
  expect(rules).toBeDisabled();
  expect(rules).toHaveAccessibleDescription(/可使用大厅导航/);
  expect(rules).not.toHaveAttribute('aria-current');
  expect(screen.getByRole('button', { name: phase === 'waiting' ? '离开房间' : '取消匹配' })).toBeEnabled();
});

it.each([
  [MsgType.CreateRoom, /创建房间/, '创建房间'],
  [MsgType.JoinRoom, /^加入$/, '加入房间'],
  [MsgType.JoinRoom, /123456/, '加入房间']
] as const)('reports failed %s from %s and waits for the response before allowing another request', (type, name, label) => {
  useAppStore.setState({ phase: 'lobby', lobbyPanel: 'home', roomRequest: null, error: '', roomCodeInput: '123456',
    roomList: [{ room_code: '123456', player_count: 1, max_players: 3 }] });
  const send = vi.fn().mockReturnValueOnce(false).mockReturnValue(true);
  render(<Lobby socket={{ send } as unknown as GameSocket} />);
  const button = screen.getByRole('button', { name });
  fireEvent.click(button);
  expect(useAppStore.getState().error).toContain(label + '请求未发送');
  fireEvent.click(button);
  expect(button).toBeDisabled();
  fireEvent.click(button);
  expect(send).toHaveBeenCalledTimes(2);
  expect(send).toHaveBeenLastCalledWith(type, type === MsgType.JoinRoom ? { room_code: '123456' } : undefined);
  for (const request_type of [MsgType.GetStats, undefined]) {
    act(() => useAppStore.getState().handleMessage({ type: MsgType.Error, payload: { message: '其他请求或限流告警', request_type } }));
    fireEvent.click(button);
    expect(button).toBeDisabled();
    expect(send).toHaveBeenCalledTimes(2);
  }
  act(() => useAppStore.getState().handleMessage({ type: MsgType.Error, payload: { message: '服务器拒绝请求', request_type: type } }));
  expect(button).toBeEnabled();
  fireEvent.click(button);
  act(() => useAppStore.getState().handleMessage({ type: type === MsgType.CreateRoom ? MsgType.RoomCreated : MsgType.RoomJoined,
    payload: { room_code: '123456', player: { id: 'me', name: '我', seat: 0 }, players: [] } }));
  expect(screen.getByRole('button', { name: '离开房间' })).toBeEnabled();
});

it('preserves the waiting room on failed leave and accepts a retry after reconnect', () => {
  useAppStore.setState({ phase: 'waiting', roomCode: '123456', roomRequest: null, connected: false });
  const send = vi.fn().mockReturnValueOnce(false).mockReturnValue(true);
  render(<Lobby socket={{ send } as unknown as GameSocket} />);
  const leave = screen.getByRole('button', { name: '离开房间' });
  fireEvent.click(leave);
  expect(useAppStore.getState()).toMatchObject({ phase: 'waiting', roomCode: '123456', error: expect.stringContaining('离房请求未发送') });
  act(() => useAppStore.getState().setConnected(true));
  fireEvent.click(leave);
  expect(useAppStore.getState()).toMatchObject({ phase: 'lobby', roomCode: '' });
});

it('waits for our own ready update and unlocks a pending cancel on disconnect', () => {
  useAppStore.setState({ phase: 'waiting', playerId: 'me', roomRequest: null, players: [
    { id: 'me', name: '我', seat: 0, ready: false, online: true, is_landlord: false, cards_count: 0 },
  ] });
  const send = vi.fn().mockReturnValueOnce(false).mockReturnValue(true);
  render(<Lobby socket={{ send } as unknown as GameSocket} />);
  const ready = screen.getByRole('button', { name: '准备开始' });
  fireEvent.click(ready);
  expect(useAppStore.getState().error).toContain('准备请求未发送');
  fireEvent.click(ready);
  act(() => useAppStore.getState().handleMessage({ type: MsgType.PlayerReady, payload: { player_id: 'peer', ready: true } }));
  act(() => useAppStore.getState().handleMessage({ type: MsgType.PlayerReady, payload: { player_id: 'me', ready: false } }));
  expect(screen.getByRole('button', { name: '正在准备…' })).toBeDisabled();
  act(() => useAppStore.getState().handleMessage({ type: MsgType.PlayerReady, payload: { player_id: 'me', ready: true } }));
  const cancel = screen.getByRole('button', { name: '取消准备' });
  fireEvent.click(cancel);
  expect(send).toHaveBeenLastCalledWith(MsgType.CancelReady, undefined);
  expect(cancel).toBeDisabled();
  act(() => useAppStore.getState().setConnected(false));
  expect(cancel).toBeEnabled();
});
