import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Lobby } from '../src/features/lobby/Lobby';
import { GameTable } from '../src/features/table/GameTable';
import { useAppStore } from '../src/stores/appStore';
import { MsgType } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('IME-safe chat and room-code forms', () => {
  it('suppresses Enter submission during IME composition and Safari 229 fallback in lobby chat, then submits once confirmed', () => {
    const send = vi.fn().mockReturnValue(true);
    useAppStore.setState({ phase: 'lobby', lobbyPanel: 'chat', chatInput: 'nihao' });
    render(<Lobby socket={{ subscribe: () => () => {}, send } as unknown as GameSocket} />);

    const input = screen.getByLabelText('大厅聊天消息') as HTMLInputElement;
    const submitButton = screen.getByRole('button', { name: '发送' }) as HTMLButtonElement;
    expect(input.form).not.toBeNull();
    expect(submitButton.type).toBe('submit');

    // Standard IME composition active
    fireEvent.compositionStart(input);
    fireEvent.change(input, { target: { value: '你好' } });
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true, keyCode: 229 });
    expect(send).not.toHaveBeenCalled();
    expect(useAppStore.getState().chatInput).toBe('你好');

    // Safari WebKit bug 165004: compositionend delivered before confirming Enter keydown (keyCode 229)
    fireEvent.compositionEnd(input);
    fireEvent.keyDown(input, { key: 'Enter', isComposing: false, keyCode: 229 });
    expect(send).not.toHaveBeenCalled();

    // Confirmed Enter keystroke after composition completes
    fireEvent.keyDown(input, { key: 'Enter', isComposing: false, keyCode: 13 });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(MsgType.Chat, { content: '你好', scope: 'lobby' });
    expect(useAppStore.getState().chatInput).toBe('');
  });

  it('suppresses Enter submission during IME composition in room table chat and supports form submit button', () => {
    const send = vi.fn().mockReturnValue(true);
    useAppStore.setState({
      phase: 'playing',
      connected: true,
      playerId: 'p1',
      currentTurn: 'p1',
      players: [],
      hand: [],
      selectedCards: new Set(),
      drawer: 'chat',
      chatInput: '快点'
    });
    render(<GameTable socket={{ subscribe: () => () => {}, send } as unknown as GameSocket} />);

    const input = screen.getByLabelText('房间聊天消息') as HTMLInputElement;
    const submitButton = screen.getByRole('button', { name: '发送' }) as HTMLButtonElement;
    expect(input.form).not.toBeNull();
    expect(submitButton.type).toBe('submit');

    fireEvent.compositionStart(input);
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true, keyCode: 229 });
    expect(send).not.toHaveBeenCalled();

    fireEvent.compositionEnd(input);
    fireEvent.click(submitButton);
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(MsgType.Chat, { content: '快点', scope: 'room' });
    expect(useAppStore.getState().chatInput).toBe('');
  });

  it('submits room code via form with enterKeyHint="go" and autoComplete="off"', () => {
    const send = vi.fn().mockReturnValue(true);
    useAppStore.setState({ phase: 'lobby', lobbyPanel: 'home', roomCodeInput: '654321', error: '' });
    render(<Lobby socket={{ subscribe: () => () => {}, send } as unknown as GameSocket} />);

    const input = screen.getByLabelText('加入房间') as HTMLInputElement;
    const submitButton = screen.getByRole('button', { name: '加入' }) as HTMLButtonElement;
    expect(input.form).not.toBeNull();
    expect(input).toHaveAttribute('enterkeyhint', 'go');
    expect(input).toHaveAttribute('autocomplete', 'off');
    expect(submitButton.type).toBe('submit');

    fireEvent.keyDown(input, { key: 'Enter', isComposing: true, keyCode: 229 });
    expect(send).not.toHaveBeenCalled();

    fireEvent.keyDown(input, { key: 'Enter', isComposing: false, keyCode: 13 });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(MsgType.JoinRoom, { room_code: '654321' });
  });
});

it.each(['lobby', 'room'])('retains the %s chat draft when sending fails', (scope) => {
  useAppStore.setState({ phase: scope === 'room' ? 'playing' : 'lobby', lobbyPanel: 'chat',
    drawer: 'chat', hand: [], players: [], chatInput: ' 保留草稿 ', error: '' });
  const socket = { send: vi.fn().mockReturnValue(false) } as unknown as GameSocket;
  render(scope === 'room' ? <GameTable socket={socket} /> : <Lobby socket={socket} />);
  fireEvent.click(screen.getByRole('button', { name: '发送' }));
  expect(useAppStore.getState().chatInput).toBe(' 保留草稿 ');
  expect(useAppStore.getState().error).toContain('草稿已保留');
});
