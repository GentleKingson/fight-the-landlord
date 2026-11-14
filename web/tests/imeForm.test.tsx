import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Lobby } from '../src/features/lobby/Lobby';
import { useAppStore } from '../src/stores/appStore';
import { MsgType } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

afterEach(() => {
  cleanup();
});

describe('IME-safe room-code form', () => {
  it('suppresses Enter submission during IME composition and Safari 229 fallback, then submits room code via form', () => {
    const send = vi.fn().mockReturnValue(true);
    useAppStore.setState({ phase: 'lobby', lobbyPanel: 'home', roomRequest: null, roomCodeInput: '654321', error: '' });
    render(<Lobby socket={{ subscribe: () => () => {}, send } as unknown as GameSocket} />);

    const input = screen.getByLabelText('加入房间') as HTMLInputElement;
    const submitButton = screen.getByRole('button', { name: '加入' }) as HTMLButtonElement;
    expect(input.form).not.toBeNull();
    expect(input).toHaveAttribute('enterkeyhint', 'go');
    expect(input).toHaveAttribute('autocomplete', 'off');
    expect(submitButton.type).toBe('submit');

    // Standard IME composition active
    fireEvent.compositionStart(input);
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true, keyCode: 229 });
    expect(send).not.toHaveBeenCalled();

    // Safari WebKit bug 165004: compositionend delivered before confirming Enter keydown (keyCode 229)
    fireEvent.compositionEnd(input);
    fireEvent.keyDown(input, { key: 'Enter', isComposing: false, keyCode: 229 });
    expect(send).not.toHaveBeenCalled();

    // Confirmed Enter keystroke after composition completes
    fireEvent.keyDown(input, { key: 'Enter', isComposing: false, keyCode: 13 });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(MsgType.JoinRoom, { room_code: '654321' });
  });
});
