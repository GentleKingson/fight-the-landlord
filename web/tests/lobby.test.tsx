import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import axe from 'axe-core';
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

it('renders leaderboard header, top-rank badges, win stats, and current-player marker with zero axe violations', async () => {
  let listener: ((msg: { type: string }) => void) | undefined;
  const send = vi.fn().mockReturnValue(true);
  const socket = {
    send,
    subscribe: (fn: typeof listener) => {
      listener = fn;
      return () => {};
    }
  } as unknown as GameSocket;

  useAppStore.setState({
    phase: 'lobby',
    lobbyPanel: 'leaderboard',
    connected: true,
    playerId: 'me',
    playerName: '青竹',
    leaderboard: [
      { rank: 1, player_id: 'p1', player_name: '云鹤', score: 268, wins: 42, win_rate: 67.74 },
      { rank: 2, player_id: 'me', player_name: '青竹', score: 186, wins: 29, win_rate: 60.41 },
      { rank: 4, player_id: 'p4', player_name: '山月', score: 152, wins: 24, win_rate: 55.8 }
    ]
  });

  const { container } = render(<Lobby socket={socket} />);
  act(() => listener?.({ type: MsgType.LeaderboardResult }));

  const head = container.querySelector('.sub-panel__head');
  const reload = screen.getByRole('button', { name: '重新加载' });
  expect(head).toContainElement(reload);
  expect(screen.getByText('42 胜 · 胜率 67.7%')).toBeInTheDocument();
  expect(screen.getByText('268 分')).toBeInTheDocument();

  const meRow = container.querySelector('.ranking-row.is-me.is-top-2');
  expect(meRow).toBeTruthy();
  expect(meRow?.textContent).toContain('青竹');
  expect(meRow?.textContent).toContain('我');
  expect(container.querySelector('.ranking-row:last-child .ranking-rank')?.textContent).toBe('#4');

  const results = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } });
  expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([]);
});

it('automatically scrolls the leaderboard to center the current player rank on open and reload', () => {
  let listener: ((msg: { type: string }) => void) | undefined;
  const socket = {
    send: vi.fn().mockReturnValue(true),
    subscribe: (fn: typeof listener) => {
      listener = fn;
      return () => {};
    }
  } as unknown as GameSocket;

  const entries = Array.from({ length: 30 }, (_, idx) => ({
    rank: idx + 1,
    player_id: `p${idx + 1}`,
    player_name: `牌友${idx + 1}`,
    score: 300 - idx * 5,
    wins: 30 - idx,
    win_rate: 55
  }));

  const rowHeight = 52;
  const rowGap = 8;
  const rowStep = rowHeight + rowGap;
  const listViewportHeight = 300;

  const scrollHeightSpy = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLElement) {
    return this.classList.contains('ranking-list') ? entries.length * rowStep - rowGap : 0;
  });
  const clientHeightSpy = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function (this: HTMLElement) {
    return this.classList.contains('ranking-list') ? listViewportHeight : 0;
  });
  const offsetHeightSpy = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
    return this.classList.contains('ranking-row') ? rowHeight : 0;
  });
  const offsetTopSpy = vi.spyOn(HTMLElement.prototype, 'offsetTop', 'get').mockImplementation(function (this: HTMLElement) {
    if (!this.classList.contains('ranking-row') || !this.parentElement) return 0;
    const index = Array.from(this.parentElement.children).indexOf(this);
    return index >= 0 ? index * rowStep : 0;
  });

  try {
    useAppStore.setState({
      phase: 'lobby',
      lobbyPanel: 'leaderboard',
      connected: true,
      playerId: 'p18',
      playerName: '牌友18',
      leaderboard: entries
    });

    const { container } = render(<Lobby socket={socket} />);
    act(() => listener?.({ type: MsgType.LeaderboardResult }));

    const listEl = container.querySelector<HTMLElement>('.ranking-list')!;
    expect(listEl).toBeTruthy();
    expect(listEl.scrollTop).toBe(896);

    act(() => useAppStore.setState({ playerId: 'p2' }));
    expect(listEl.scrollTop).toBe(0);

    act(() => useAppStore.setState({ playerId: 'p30' }));
    expect(listEl.scrollTop).toBe(1492);

    act(() => useAppStore.setState({ playerId: 'unranked' }));
    expect(listEl.scrollTop).toBe(0);

    act(() => useAppStore.setState({ playerId: 'p18' }));
    expect(listEl.scrollTop).toBe(896);
    listEl.scrollTop = 0;
    fireEvent.click(screen.getByRole('button', { name: '重新加载' }));
    act(() => listener?.({ type: MsgType.LeaderboardResult }));
    expect(container.querySelector<HTMLElement>('.ranking-list')!.scrollTop).toBe(896);
  } finally {
    scrollHeightSpy.mockRestore();
    clientHeightSpy.mockRestore();
    offsetHeightSpy.mockRestore();
    offsetTopSpy.mockRestore();
  }
});

it('renders full personal stats dashboard and empty-record CTA cleanly with zero axe violations', async () => {
  let listener: ((msg: { type: string }) => void) | undefined;
  const send = vi.fn().mockReturnValue(true);
  const socket = {
    send,
    subscribe: (fn: typeof listener) => {
      listener = fn;
      return () => {};
    }
  } as unknown as GameSocket;

  useAppStore.setState({
    phase: 'lobby',
    lobbyPanel: 'stats',
    connected: true,
    playerId: 'me',
    playerName: '青竹',
    stats: {
      player_id: 'me',
      player_name: '青竹',
      total_games: 48,
      wins: 29,
      losses: 19,
      win_rate: 60.416,
      landlord_games: 18,
      landlord_wins: 11,
      farmer_games: 30,
      farmer_wins: 18,
      score: 186,
      rank: 3,
      current_streak: 4,
      max_win_streak: 7
    }
  });

  const { container } = render(<Lobby socket={socket} />);
  act(() => listener?.({ type: MsgType.StatsResult }));

  expect(container.querySelector('.sub-panel__head')).toContainElement(screen.getByRole('button', { name: '重新加载' }));
  expect(screen.getByRole('heading', { name: '青竹', level: 3 })).toBeInTheDocument();
  expect(screen.getByText('4 连胜中')).toBeInTheDocument();
  expect(screen.getByText('+186')).toBeInTheDocument();
  expect(screen.getByText('#3')).toBeInTheDocument();
  expect(screen.getByText('60.4%')).toBeInTheDocument();
  expect(screen.getByText('29 / 19')).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: '地主战绩', level: 3 })).toBeInTheDocument();
  expect(screen.getByText('61.1%')).toBeInTheDocument();
  expect(screen.getByText('11 胜 7 负')).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: '农民战绩', level: 3 })).toBeInTheDocument();
  expect(screen.getByText('60.0%')).toBeInTheDocument();
  expect(screen.getByText('18 胜 12 负')).toBeInTheDocument();

  const populatedAxe = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } });
  expect(populatedAxe.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([]);

  // Backend returns a non-null zero-game StatsResultPayload for brand-new players
  act(() => {
    useAppStore.setState({
      stats: {
        player_id: 'me',
        player_name: '青竹',
        total_games: 0,
        wins: 0,
        losses: 0,
        win_rate: 0,
        landlord_games: 0,
        landlord_wins: 0,
        farmer_games: 0,
        farmer_wins: 0,
        score: 0,
        rank: 0,
        current_streak: 0,
        max_win_streak: 0
      }
    });
  });

  expect(screen.getByText(/你还没有对局记录/)).toBeInTheDocument();
  const goLobbyBtn = screen.getByRole('button', { name: /前往大厅开局/ });
  fireEvent.click(goLobbyBtn);
  expect(useAppStore.getState().lobbyPanel).toBe('home');
});
