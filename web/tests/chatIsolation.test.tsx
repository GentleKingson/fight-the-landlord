import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Lobby } from '../src/features/lobby/Lobby';
import { GameTable } from '../src/features/table/GameTable';
import { MsgType, WireMessageType, type ChatPayload } from '../src/protocol/types';
import {
  selectChatMessages,
  useAppStore,
  useChatStore
} from '../src/stores/appStore';
import { createChatCommand, dispatchCommand } from '../src/transport/commandDispatcher';
import type { GameSocket, SendResult } from '../src/transport/wsClient';

const send = vi.fn((): SendResult => ({ ok: true }));
const socket = { send } as unknown as GameSocket;

beforeEach(() => {
  useAppStore.getState().clearPendingCommands();
  useChatStore.getState().clear();
  send.mockReset();
  send.mockReturnValue({ ok: true });
  useAppStore.setState({
    connected: true,
    connectionStatus: 'connected',
    maintenance: false,
    error: '',
    businessError: null,
    pendingCommands: {},
    phase: 'lobby',
    playerId: 'p1',
    playerName: '青竹',
    roomCode: '',
    players: [],
    roomCodeInput: '',
    lobbyPanel: 'chat',
    onlineCount: 3,
    matchDeadlineMs: 0,
    matchPractice: false,
    hand: [],
    selectedCards: new Set(),
    bottomCards: [],
    bottomCardsRevealed: false,
    currentTurn: '',
    lastPlayed: [],
    lastPlayedBy: '',
    lastPlayedName: '',
    lastHandType: '',
    mustPlay: false,
    canBeat: true,
    baseScore: 1,
    turnDeadlineMs: 0,
    serverClockOffsetMs: 0,
    gameId: '',
    streamId: '',
    eventVersion: 0,
    turnId: 0,
    seenGameStreams: {},
    playedCardsLedger: {},
    seatActions: {},
    recentActions: [],
    cardCounter: {},
    tableMessage: '',
    chatInput: '',
    drawer: 'none'
  });
});

afterEach(() => {
  cleanup();
  useAppStore.getState().clearPendingCommands();
  vi.clearAllMocks();
});

describe('authoritative chat isolation', () => {
  it('keeps lobby, room, and game messages in exact independent buckets', () => {
    push(chat('lobby-1', 1, 'lobby'));
    push(chat('room-a', 2, 'room', 'ROOM-A'));
    push(chat('room-b', 3, 'room', 'ROOM-B'));
    push(chat('game-a', 4, 'game', 'ROOM-A', 'GAME-A'));
    push(chat('game-b', 5, 'game', 'ROOM-B', 'GAME-B'));

    expect(ids('lobby')).toEqual(['lobby-1']);
    expect(ids('room:ROOM-A')).toEqual(['room-a']);
    expect(ids('room:ROOM-B')).toEqual(['room-b']);
    expect(ids('game:GAME-A')).toEqual(['game-a']);
    expect(ids('game:GAME-B')).toEqual(['game-b']);

    expect(useChatStore.getState().push(chat('invalid-room', 6, 'room'))).toBe(false);
    expect(useChatStore.getState().push(chat('invalid-game', 7, 'game', 'ROOM-A'))).toBe(false);
    expect(useChatStore.getState().push(chat('spoofed-lobby', 8, 'lobby', 'ROOM-A'))).toBe(false);
  });

  it('deduplicates replayed IDs, sorts by authoritative server time, and caps each bucket at 80', () => {
    push(chat('late', 30, 'lobby'));
    push(chat('tie-first', 20, 'lobby'));
    push(chat('early', 10, 'lobby'));
    push(chat('tie-second', 20, 'lobby'));
    expect(useChatStore.getState().push(chat('early', 99, 'lobby'))).toBe(false);
    expect(ids('lobby')).toEqual(['early', 'tie-first', 'tie-second', 'late']);

    useChatStore.getState().clear('lobby');
    for (let index = 0; index < 81; index += 1) {
      push(chat(`message-${index}`, 1_000 + index, 'lobby'));
    }
    expect(ids('lobby')).toHaveLength(80);
    expect(ids('lobby')[0]).toBe('message-1');
    expect(ids('lobby').at(-1)).toBe('message-80');
  });

  it('stores an authoritative event without treating its message ID as a command ACK', () => {
    const result = dispatchCommand(socket, createChatCommand('你好', 'lobby'));
    if (!result.ok || result.request.kind !== 'chat') throw new Error('expected chat command');
    const messageId = result.request.messageId;

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Chat,
      payload: {
        content: '你好',
        scope: 'lobby',
        message_id: messageId
      }
    }));

    expect(useAppStore.getState().pendingCommands.chat).toBeDefined();
    expect(ids('lobby')).toEqual([]);

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.CommandAck,
      payload: { request_id: result.requestId, command_type: WireMessageType.MSG_CHAT }
    }));
    expect(useAppStore.getState().pendingCommands.chat).toBeUndefined();
  });
});

describe('chat views and input behavior', () => {
  it('renders only lobby messages with stable keys and does not submit during IME composition', () => {
    push(chat('later', 20, 'lobby', undefined, undefined, '稍后消息'));
    push(chat('hidden-game', 30, 'game', 'ROOM-A', 'GAME-A', '牌局消息'));
    render(<Lobby socket={socket} />);

    const laterNode = screen.getByText('稍后消息').closest('p');
    expect(screen.queryByText('牌局消息')).not.toBeInTheDocument();
    expect(screen.getByRole('log', { name: '大厅聊天记录' })).toHaveAttribute('tabindex', '0');

    act(() => push(chat('earlier', 10, 'lobby', undefined, undefined, '较早消息')));
    expect(screen.getByText('稍后消息').closest('p')).toBe(laterNode);

    const input = screen.getByRole('textbox', { name: '大厅聊天消息' });
    expect(input).toHaveAttribute('maxlength', '240');
    fireEvent.change(input, { target: { value: '输入法消息' } });
    fireEvent.compositionStart(input);
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true });
    expect(send).not.toHaveBeenCalled();

    fireEvent.compositionEnd(input);
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(MsgType.Chat, expect.objectContaining({
      content: '输入法消息',
      scope: 'lobby',
      message_id: expect.any(String)
    }), expect.objectContaining({ request_id: expect.any(String) }));
  });

  it('switches exact game views without deleting prior buckets and sends game scope', () => {
    push(chat('game-a', 10, 'game', 'ROOM-A', 'GAME-A', '甲局消息'));
    push(chat('game-b', 20, 'game', 'ROOM-B', 'GAME-B', '乙局消息'));
    push(chat('room-a', 30, 'room', 'ROOM-A', undefined, '房间消息'));
    useAppStore.setState({
      phase: 'playing',
      roomCode: 'ROOM-A',
      gameId: 'GAME-A',
      drawer: 'chat'
    });
    render(<GameTable socket={socket} />);

    expect(screen.getByText('甲局消息')).toBeInTheDocument();
    expect(screen.queryByText('乙局消息')).not.toBeInTheDocument();
    expect(screen.queryByText('房间消息')).not.toBeInTheDocument();
    expect(screen.getByRole('log', { name: '牌局聊天记录' })).toHaveAttribute('tabindex', '0');

    act(() => useAppStore.setState({ roomCode: 'ROOM-B', gameId: 'GAME-B' }));
    expect(screen.getByText('乙局消息')).toBeInTheDocument();
    expect(screen.queryByText('甲局消息')).not.toBeInTheDocument();
    expect(ids('game:GAME-A')).toEqual(['game-a']);

    const input = screen.getByRole('textbox', { name: '牌局聊天消息' });
    fireEvent.change(input, { target: { value: '当前牌局' } });
    fireEvent.click(screen.getByRole('button', { name: '发送' }));
    expect(send).toHaveBeenCalledWith(MsgType.Chat, expect.objectContaining({
      content: '当前牌局',
      scope: 'game',
      message_id: expect.any(String)
    }), expect.objectContaining({ request_id: expect.any(String) }));

    act(() => useAppStore.getState().leaveLocalRoom());
    expect(screen.queryByText('乙局消息')).not.toBeInTheDocument();
    expect(ids('game:GAME-B')).toEqual(['game-b']);
  });
});

function chat(
  messageId: string,
  serverTime: number,
  scope: string,
  roomCode?: string,
  gameId?: string,
  content = messageId
): ChatPayload {
  return {
    sender_id: 'sender',
    sender_name: '玩家',
    content,
    scope,
    message_id: messageId,
    room_code: roomCode,
    game_id: gameId,
    server_time: serverTime
  };
}

function push(message: ChatPayload): void {
  expect(useChatStore.getState().push(message)).toBe(true);
}

function ids(context: 'lobby' | `room:${string}` | `game:${string}`): string[] {
  return selectChatMessages(context)(useChatStore.getState()).map((message) => message.message_id);
}
