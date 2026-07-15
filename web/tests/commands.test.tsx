import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Lobby } from '../src/features/lobby/Lobby';
import { MsgType, WireMessageType, type CardInfo } from '../src/protocol/types';
import { cardKey } from '../src/shared/cards/cardModel';
import { BusinessErrorBanner } from '../src/shared/ui/BusinessErrorBanner';
import { useAppStore } from '../src/stores/appStore';
import { createChatCommand, dispatchCommand } from '../src/transport/commandDispatcher';
import type { GameSocket, SendResult } from '../src/transport/wsClient';

const send = vi.fn((): SendResult => ({ ok: true }));
const socket = { send } as unknown as GameSocket;

beforeEach(() => {
  useAppStore.getState().clearPendingCommands();
  send.mockReset();
  send.mockReturnValue({ ok: true });
  useAppStore.setState({
    connected: true,
    connectionStatus: 'connected',
    error: '',
    businessError: null,
    pendingCommands: {},
    maintenance: false,
    phase: 'lobby',
    playerId: 'p1',
    playerName: '青竹',
    roomCode: '',
    players: [],
    roomCodeInput: '',
    lobbyPanel: 'home',
    onlineCount: 3,
    matchDeadlineMs: 0,
    matchPractice: false,
    hand: [],
    selectedCards: new Set(),
    tableMessage: '',
    chatInput: ''
  });
});

afterEach(() => {
  cleanup();
  useAppStore.getState().clearPendingCommands();
  vi.useRealTimers();
});

describe('observable command dispatch', () => {
  it('blocks duplicate submissions and starts pending only after a successful send', () => {
    const first = dispatchCommand(socket, { kind: 'create-room' });
    const duplicate = dispatchCommand(socket, { kind: 'create-room' });

    expect(first.ok).toBe(true);
    expect(duplicate).toEqual({ ok: false, reason: 'duplicate' });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(MsgType.CreateRoom);
    expect(useAppStore.getState().pendingCommands['create-room']).toBeDefined();
  });

  it('disables a command button after the first click', () => {
    render(<Lobby socket={socket} />);
    const createRoom = screen.getByRole('button', { name: /创建房间/ });

    fireEvent.click(createRoom);
    fireEvent.click(createRoom);

    expect(createRoom).toBeDisabled();
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(MsgType.CreateRoom);
  });

  it('does not enter pending on send failure and exposes a network error', () => {
    send.mockReturnValueOnce({ ok: false, reason: 'not-connected' });

    const result = dispatchCommand(socket, { kind: 'join-room', roomCode: ' 778899 ' });

    expect(result).toEqual({ ok: false, reason: 'not-connected' });
    expect(useAppStore.getState().pendingCommands['join-room']).toBeUndefined();
    expect(useAppStore.getState().businessError).toMatchObject({
      category: 'network',
      command: 'join-room'
    });
  });

  it('clears every command on its authoritative success acknowledgement', () => {
    const player = {
      id: 'p1', name: '青竹', seat: 0, ready: false, is_landlord: false,
      cards_count: 2, online: true, is_bot: false
    };

    dispatchCommand(socket, { kind: 'create-room' });
    useAppStore.getState().handleMessage({
      type: MsgType.RoomCreated,
      payload: { room_code: '100001', player }
    });
    expect(useAppStore.getState().pendingCommands['create-room']).toBeUndefined();

    dispatchCommand(socket, { kind: 'join-room', roomCode: '100002' });
    useAppStore.getState().handleMessage({
      type: MsgType.RoomJoined,
      payload: { room_code: '100002', player, players: [player] }
    });
    expect(useAppStore.getState().pendingCommands['join-room']).toBeUndefined();

    dispatchCommand(socket, { kind: 'quick-match' });
    useAppStore.getState().handleMessage({
      type: MsgType.MatchQueued,
      payload: { deadline_ms: Date.now() + 30_000, practice: false }
    });
    expect(useAppStore.getState().pendingCommands['quick-match']).toBeUndefined();

    dispatchCommand(socket, { kind: 'cancel-match' });
    useAppStore.getState().handleMessage({ type: MsgType.MatchCancelled, payload: { reason: 'cancelled' } });
    expect(useAppStore.getState().pendingCommands['cancel-match']).toBeUndefined();

    dispatchCommand(socket, { kind: 'practice-match' });
    useAppStore.getState().handleMessage({
      type: MsgType.MatchQueued,
      payload: { deadline_ms: Date.now() + 10_000, practice: true }
    });
    expect(useAppStore.getState().pendingCommands['practice-match']).toBeUndefined();

    dispatchCommand(socket, { kind: 'ready' });
    useAppStore.getState().handleMessage({ type: MsgType.PlayerReady, payload: { player_id: 'p1', ready: true } });
    expect(useAppStore.getState().pendingCommands.ready).toBeUndefined();

    dispatchCommand(socket, { kind: 'cancel-ready' });
    useAppStore.getState().handleMessage({ type: MsgType.PlayerReady, payload: { player_id: 'p1', ready: false } });
    expect(useAppStore.getState().pendingCommands['cancel-ready']).toBeUndefined();

    dispatchCommand(socket, { kind: 'bid', bid: true });
    useAppStore.getState().handleMessage({
      type: MsgType.BidResult,
      payload: { player_id: 'p1', player_name: '青竹', bid: true, is_grab: false, multiplier: 1 }
    });
    expect(useAppStore.getState().pendingCommands.bid).toBeUndefined();

    const cards = hand(7);
    dispatchCommand(socket, { kind: 'play', cards });
    useAppStore.getState().handleMessage({
      type: MsgType.CardPlayed,
      payload: { player_id: 'p1', player_name: '青竹', cards, cards_left: 1, hand_type: '单张' }
    });
    expect(useAppStore.getState().pendingCommands.play).toBeUndefined();

    dispatchCommand(socket, { kind: 'pass' });
    useAppStore.getState().handleMessage({
      type: MsgType.PlayerPass,
      payload: { player_id: 'p1', player_name: '青竹' }
    });
    expect(useAppStore.getState().pendingCommands.pass).toBeUndefined();

    dispatchCommand(socket, { kind: 'leave-room' });
    useAppStore.getState().handleMessage({ type: MsgType.RoomLeft, payload: { room_code: '100002' } });
    expect(useAppStore.getState().pendingCommands['leave-room']).toBeUndefined();

    const chat = createChatCommand('收到', 'lobby');
    dispatchCommand(socket, chat);
    if (chat.kind !== 'chat') throw new Error('expected chat request');
    useAppStore.getState().handleMessage({
      type: MsgType.Chat,
      payload: { content: chat.content, scope: chat.scope, message_id: chat.messageId }
    });
    expect(useAppStore.getState().pendingCommands.chat).toBeUndefined();
  });

  it('clears the rejected command by server command_type and retains selected cards', () => {
    const cards = hand(7, 7);
    const selectedCards = new Set(cards.map(cardKey));
    useAppStore.setState({ selectedCards });
    dispatchCommand(socket, { kind: 'play', cards });

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Error,
      payload: {
        code: 3004,
        message: '当前牌型压不过上一手',
        command_type: WireMessageType.MSG_PLAY_CARDS
      }
    }));

    expect(useAppStore.getState().pendingCommands.play).toBeUndefined();
    expect(useAppStore.getState().selectedCards).toEqual(selectedCards);
    expect(useAppStore.getState().businessError).toMatchObject({
      category: 'validation',
      message: '当前牌型压不过上一手',
      command: 'play'
    });
  });

  it('clears all pending commands for an uncorrelated server error', () => {
    useAppStore.getState().beginCommand({ kind: 'chat', content: 'hi', scope: 'lobby', messageId: 'm1' }, 5_000, false);
    useAppStore.getState().beginCommand({ kind: 'ready' }, 5_000, true);

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Error,
      payload: { code: 1000, message: '服务器无法识别请求来源' }
    }));

    expect(useAppStore.getState().pendingCommands).toEqual({});
    expect(useAppStore.getState().businessError).toMatchObject({ category: 'validation' });
    expect(useAppStore.getState().businessError?.retry).toBeUndefined();
  });

  it('blocks maintenance-sensitive commands before send', () => {
    useAppStore.setState({ maintenance: true });

    const result = dispatchCommand(socket, { kind: 'practice-match' });

    expect(result).toEqual({ ok: false, reason: 'maintenance' });
    expect(send).not.toHaveBeenCalled();
    expect(useAppStore.getState().pendingCommands['practice-match']).toBeUndefined();
    expect(useAppStore.getState().businessError).toMatchObject({ category: 'maintenance' });
  });

  it('renders connected business errors with clear and safe retry controls', () => {
    useAppStore.getState().setBusinessError('network', '准备请求未发送', { kind: 'ready' }, 'ready');
    render(<BusinessErrorBanner socket={socket} />);

    expect(screen.getByRole('alert')).toHaveTextContent('网络异常');
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(send).toHaveBeenCalledWith(MsgType.Ready);
    expect(useAppStore.getState().pendingCommands.ready).toBeDefined();

    act(() => useAppStore.getState().setBusinessError('validation', '牌型无效'));
    fireEvent.click(screen.getByRole('button', { name: '清除错误' }));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('recovers when the server does not acknowledge a match command', () => {
    vi.useFakeTimers();
    dispatchCommand(socket, { kind: 'quick-match' });

    act(() => vi.advanceTimersByTime(8_000));

    expect(useAppStore.getState().pendingCommands['quick-match']).toBeUndefined();
    expect(useAppStore.getState().phase).toBe('lobby');
    expect(useAppStore.getState().businessError).toMatchObject({
      category: 'timeout',
      command: 'quick-match',
      retry: { kind: 'quick-match' }
    });
  });

  it('enters matching and cancels only on authoritative match events', () => {
    render(<Lobby socket={socket} />);

    fireEvent.click(screen.getByRole('button', { name: /快速开局/ }));
    expect(useAppStore.getState().phase).toBe('lobby');
    expect(screen.getByText('正在提交匹配请求')).toBeInTheDocument();

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.MatchQueued,
      payload: { deadline_ms: Date.now() + 30_000, practice: false }
    }));
    expect(useAppStore.getState().phase).toBe('matching');
    expect(screen.getByText('正在寻找牌友')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '取消匹配' }));
    expect(send).toHaveBeenLastCalledWith(MsgType.CancelMatch);
    expect(useAppStore.getState().phase).toBe('matching');

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.MatchCancelled,
      payload: { reason: 'cancelled' }
    }));
    expect(useAppStore.getState().phase).toBe('lobby');
    expect(useAppStore.getState().pendingCommands['cancel-match']).toBeUndefined();
  });

  it('waits for RoomLeft instead of clearing the room optimistically', () => {
    useAppStore.setState({
      phase: 'waiting',
      roomCode: '778899',
      players: [{
        id: 'p1', name: '青竹', seat: 0, ready: false, is_landlord: false,
        cards_count: 0, online: true, is_bot: false
      }]
    });
    render(<Lobby socket={socket} />);

    fireEvent.click(screen.getByRole('button', { name: '离开房间' }));
    expect(useAppStore.getState().phase).toBe('waiting');
    expect(useAppStore.getState().roomCode).toBe('778899');

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.RoomLeft,
      payload: { room_code: '778899' }
    }));
    expect(useAppStore.getState().phase).toBe('lobby');
    expect(useAppStore.getState().roomCode).toBe('');
  });

  it('ignores a stale RoomLeft after a replacement room has been bound', () => {
    useAppStore.setState({
      phase: 'waiting',
      roomCode: 'new-room',
      players: [{
        id: 'p1', name: '青竹', seat: 0, ready: false, is_landlord: false,
        cards_count: 0, online: true, is_bot: false
      }]
    });

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.RoomLeft,
      payload: { room_code: 'old-room' }
    }));

    expect(useAppStore.getState().phase).toBe('waiting');
    expect(useAppStore.getState().roomCode).toBe('new-room');
    expect(useAppStore.getState().players).toHaveLength(1);
  });

  it('ignores an old RoomLeft after reconnect has returned to matching', () => {
    useAppStore.setState({
      phase: 'matching',
      roomCode: '',
      matchDeadlineMs: Date.now() + 30_000,
      matchPractice: false
    });

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.RoomLeft,
      payload: { room_code: 'retired-room' }
    }));

    expect(useAppStore.getState().phase).toBe('matching');
    expect(useAppStore.getState().matchDeadlineMs).toBeGreaterThan(0);
  });

  it('correlates chat acknowledgement by stable message_id', () => {
    const command = createChatCommand('你好', 'lobby');
    const result = dispatchCommand(socket, command);
    expect(result.ok).toBe(true);
    if (!result.ok || result.request.kind !== 'chat') throw new Error('expected chat request');
    const messageId = result.request.messageId;

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Chat,
      payload: {
        content: '另一条消息',
        scope: 'lobby',
        message_id: 'different-id'
      }
    }));
    expect(useAppStore.getState().pendingCommands.chat).toBeDefined();

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Chat,
      payload: {
        content: '你好',
        scope: 'lobby',
        message_id: messageId
      }
    }));
    expect(useAppStore.getState().pendingCommands.chat).toBeUndefined();
  });
});

function hand(...ranks: number[]): CardInfo[] {
  return ranks.map((rank, index) => ({ suit: index, rank, color: index % 2 }));
}
