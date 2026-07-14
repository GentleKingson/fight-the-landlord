import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GameTable } from '../src/features/table/GameTable';
import { useAppStore, useChatStore } from '../src/stores/appStore';
import type { GameSocket } from '../src/transport/wsClient';

const socket = {
  send: vi.fn(() => ({ ok: true as const }))
} as unknown as GameSocket;

beforeEach(() => {
  useChatStore.setState({ buckets: {} });
  useAppStore.setState({
    drawer: 'none',
    phase: 'playing',
    playerId: 'p1',
    players: [],
    currentTurn: '',
    hand: [],
    selectedCards: new Set(),
    bottomCards: [],
    bottomCardsRevealed: false,
    lastPlayed: [],
    lastPlayedName: '',
    lastHandType: '',
    seatActions: {},
    recentActions: [],
    cardCounter: {},
    chatInput: ''
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('UtilityDrawer accessibility', () => {
  it('keeps the closed drawer inert and exposes a labelled non-modal dialog when opened', () => {
    render(<GameTable socket={socket} />);

    const drawer = document.getElementById('table-utility-drawer');
    const chatTrigger = screen.getByRole('button', { name: '聊天' });
    expect(drawer).toHaveAttribute('inert');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(chatTrigger).toHaveAttribute('aria-expanded', 'false');
    expect(chatTrigger).toHaveAttribute('aria-controls', 'table-utility-drawer');
    expect(chatTrigger).toHaveAttribute('aria-haspopup', 'dialog');

    fireEvent.click(chatTrigger);

    const dialog = screen.getByRole('dialog', { name: '牌局聊天' });
    expect(dialog.tagName).toBe('ASIDE');
    expect(dialog).toHaveAttribute('aria-modal', 'false');
    expect(dialog).not.toHaveAttribute('inert');
    expect(chatTrigger).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('button', { name: '关闭' })).toHaveFocus();
    expect(screen.getByRole('textbox', { name: '牌局聊天消息' })).toBeInTheDocument();
  });

  it('closes on Escape and returns focus to the drawer trigger', () => {
    render(<GameTable socket={socket} />);
    const chatTrigger = screen.getByRole('button', { name: '聊天' });
    fireEvent.click(chatTrigger);
    expect(screen.getByRole('button', { name: '关闭' })).toHaveFocus();

    fireEvent.keyDown(document, { key: 'Escape' });

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(document.getElementById('table-utility-drawer')).toHaveAttribute('inert');
    expect(chatTrigger).toHaveFocus();
  });

  it('returns focus to the latest trigger after switching drawer panels', () => {
    render(<GameTable socket={socket} />);
    const counterTrigger = screen.getByRole('button', { name: '记牌器' });
    const historyTrigger = screen.getByRole('button', { name: '历史' });

    fireEvent.click(counterTrigger);
    expect(screen.getByRole('dialog', { name: '记牌器' })).toBeInTheDocument();
    fireEvent.click(historyTrigger);
    expect(screen.getByRole('dialog', { name: '动作历史' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '关闭' })).toHaveFocus();

    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(historyTrigger).toHaveFocus();
  });
});
