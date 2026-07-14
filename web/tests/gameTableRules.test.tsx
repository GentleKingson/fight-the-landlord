import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GameTable } from '../src/features/table/GameTable';
import { MsgType, type CardInfo } from '../src/protocol/types';
import { cardKey } from '../src/shared/cards/cardModel';
import { useAppStore } from '../src/stores/appStore';
import type { GameSocket, SendResult } from '../src/transport/wsClient';

const send = vi.fn((): SendResult => ({ ok: true }));
const socket = { send } as unknown as GameSocket;

beforeEach(() => {
  send.mockReturnValue({ ok: true });
  useAppStore.setState({
    connected: true,
    connectionStatus: 'connected',
    maintenance: false,
    error: '',
    phase: 'playing',
    playerId: 'p1',
    roomCode: '123456',
    players: [{
      id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: false,
      cards_count: 3, online: true, is_bot: false
    }],
    currentTurn: 'p1',
    hand: [],
    selectedCards: new Set(),
    bottomCards: [],
    bottomCardsRevealed: false,
    lastPlayed: [],
    lastPlayedBy: '',
    lastPlayedName: '',
    lastHandType: '',
    mustPlay: false,
    canBeat: true,
    seatActions: {},
    recentActions: [],
    cardCounter: {},
    tableMessage: '',
    drawer: 'none'
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('GameTable rule-gated actions', () => {
  it('enables play only for a valid selection that beats the active hand', () => {
    const hand = cards(4, 4, 5);
    useAppStore.setState({
      hand,
      lastPlayed: cards(3, 3),
      selectedCards: new Set(hand.slice(0, 2).map(cardKey))
    });
    render(<GameTable socket={socket} />);

    const play = screen.getByRole('button', { name: '出牌' });
    expect(play).toBeEnabled();
    fireEvent.click(play);

    expect(send).toHaveBeenCalledWith(MsgType.PlayCards, { cards: hand.slice(0, 2) });
    expect(useAppStore.getState().selectedCards).toEqual(new Set(hand.slice(0, 2).map(cardKey)));
  });

  it('disables an invalid or non-beating selection even when the server says the hand has an answer', () => {
    const hand = cards(4, 5, 6);
    useAppStore.setState({
      hand,
      lastPlayed: cards(7),
      canBeat: true,
      selectedCards: new Set(hand.slice(0, 2).map(cardKey))
    });
    render(<GameTable socket={socket} />);

    expect(screen.getByRole('button', { name: '出牌' })).toBeDisabled();
    expect(screen.getByText(/无效牌型/)).toBeInTheDocument();
  });

  it('uses the smallest legal same-type hint before bomb and rocket fallbacks', () => {
    const hand = cards(7, 7, 3, 3, 3, 3, 16, 17);
    useAppStore.setState({ hand, lastPlayed: cards(6, 6) });
    render(<GameTable socket={socket} />);

    fireEvent.click(screen.getByRole('button', { name: '提示' }));

    const selectedRanks = hand
      .filter((card) => useAppStore.getState().selectedCards.has(cardKey(card)))
      .map((card) => card.rank);
    expect(selectedRanks).toEqual([7, 7]);
    expect(screen.getByText('提示：对子')).toBeInTheDocument();
  });

  it('keeps selected cards after a send failure or server rejection', () => {
    const hand = cards(4, 4);
    const selectedCards = new Set(hand.map(cardKey));
    useAppStore.setState({ hand, lastPlayed: cards(3, 3), selectedCards });
    send.mockReturnValueOnce({ ok: false, reason: 'not-connected' });
    render(<GameTable socket={socket} />);

    fireEvent.click(screen.getByRole('button', { name: '出牌' }));
    expect(useAppStore.getState().selectedCards).toEqual(selectedCards);
    expect(screen.getByText('出牌发送失败，请检查连接后重试')).toBeInTheDocument();

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Error,
      payload: { code: 3003, message: '所选牌型不能压过上一手' }
    }));
    expect(useAppStore.getState().selectedCards).toEqual(selectedCards);
    expect(screen.getByText('所选牌型不能压过上一手')).toBeInTheDocument();
  });

  it('clears selection only after the server confirms this player played', () => {
    const hand = cards(4, 4, 5);
    const selectedCards = new Set(hand.slice(0, 2).map(cardKey));
    useAppStore.setState({ hand, selectedCards });

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.CardPlayed,
      payload: {
        player_id: 'p2', player_name: '山月', cards: cards(6), cards_left: 16, hand_type: '单张'
      }
    }));
    expect(useAppStore.getState().selectedCards).toEqual(selectedCards);

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.CardPlayed,
      payload: {
        player_id: 'p1', player_name: '青竹', cards: hand.slice(0, 2), cards_left: 1, hand_type: '对子'
      }
    }));
    expect(useAppStore.getState().selectedCards.size).toBe(0);
  });

  it('blocks commands while reconnecting or in maintenance', () => {
    const hand = cards(4);
    useAppStore.setState({ hand, mustPlay: true, selectedCards: new Set(hand.map(cardKey)) });
    const view = render(<GameTable socket={socket} />);
    expect(screen.getByRole('button', { name: '出牌' })).toBeEnabled();

    act(() => useAppStore.setState({ connectionStatus: 'reconnecting' }));
    expect(screen.getByRole('button', { name: '出牌' })).toBeDisabled();

    act(() => useAppStore.setState({ connectionStatus: 'connected', maintenance: true }));
    expect(screen.getByRole('button', { name: '出牌' })).toBeDisabled();
    view.unmount();
  });
});

function cards(...ranks: number[]): CardInfo[] {
  const occurrences = new Map<number, number>();
  return ranks.map((rank) => {
    const occurrence = occurrences.get(rank) ?? 0;
    occurrences.set(rank, occurrence + 1);
    const suit = rank >= 16 ? 4 : occurrence;
    return { suit, rank, color: suit === 1 || suit === 3 || rank === 17 ? 1 : 0 };
  });
}
