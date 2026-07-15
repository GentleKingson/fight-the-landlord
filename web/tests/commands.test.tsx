import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Lobby } from '../src/features/lobby/Lobby';
import { MsgType, WireMessageType, type CardInfo } from '../src/protocol/types';
import { cardKey } from '../src/shared/cards/cardModel';
import { BusinessErrorBanner } from '../src/shared/ui/BusinessErrorBanner';
import { useAppStore } from '../src/stores/appStore';
import { createChatCommand, dispatchCommand, retryBusinessCommand } from '../src/transport/commandDispatcher';
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
    expect(send).toHaveBeenCalledWith(MsgType.CreateRoom, undefined, expect.objectContaining({ request_id: expect.any(String) }));
    expect(useAppStore.getState().pendingCommands['create-room']).toBeDefined();
  });

  it('disables a command button after the first click', () => {
    render(<Lobby socket={socket} />);
    const createRoom = screen.getByRole('button', { name: /创建房间/ });

    fireEvent.click(createRoom);
    fireEvent.click(createRoom);

    expect(createRoom).toBeDisabled();
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith(MsgType.CreateRoom, undefined, expect.objectContaining({ request_id: expect.any(String) }));
  });

  it('routes leaderboard, stats, and room-list UI requests through correlated dispatch', () => {
    render(<Lobby socket={socket} />);

    fireEvent.click(screen.getByRole('button', { name: '战绩榜' }));
    fireEvent.click(screen.getByRole('button', { name: '我的战绩' }));
    fireEvent.click(screen.getByRole('button', { name: '大厅' }));
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));

    expect(send).toHaveBeenCalledWith(
      MsgType.GetLeaderboard,
      { type: 'total', offset: 0, limit: 30 },
      expect.objectContaining({ request_id: expect.any(String) })
    );
    expect(send).toHaveBeenCalledWith(
      MsgType.GetStats,
      undefined,
      expect.objectContaining({ request_id: expect.any(String) })
    );
    expect(send).toHaveBeenCalledWith(
      MsgType.GetRoomList,
      undefined,
      expect.objectContaining({ request_id: expect.any(String) })
    );
    expect(useAppStore.getState().pendingCommands).toMatchObject({
      leaderboard: { requestId: expect.any(String) },
      stats: { requestId: expect.any(String) },
      'room-list': { requestId: expect.any(String) }
    });
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

  it('keeps domain events separate from correlated command acknowledgements', () => {
    const player = {
      id: 'p1', name: '青竹', seat: 0, ready: false, is_landlord: false,
      cards_count: 2, online: true, is_bot: false
    };

    const result = dispatchCommand(socket, { kind: 'create-room' });
    if (!result.ok) throw new Error('expected create-room dispatch');
    useAppStore.getState().handleMessage({
      type: MsgType.RoomCreated,
      payload: { room_code: '100001', player }
    });
    expect(useAppStore.getState().pendingCommands['create-room']?.requestId).toBe(result.requestId);

    useAppStore.getState().handleMessage({
      type: MsgType.CommandAck,
      payload: { request_id: result.requestId, command_type: WireMessageType.MSG_CREATE_ROOM }
    });
    expect(useAppStore.getState().pendingCommands['create-room']).toBeUndefined();
  });

  it('clears the rejected command by server command_type and retains selected cards', () => {
    const cards = hand(7, 7);
    const selectedCards = new Set(cards.map(cardKey));
    useAppStore.setState({ selectedCards });
    const result = dispatchCommand(socket, { kind: 'play', cards });
    if (!result.ok) throw new Error('expected play dispatch');

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Error,
      payload: {
        code: 3004,
        message: '当前牌型压不过上一手',
        command_type: WireMessageType.MSG_PLAY_CARDS,
        request_id: result.requestId
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

  it('keeps pending commands intact for an uncorrelated server error', () => {
    useAppStore.getState().beginCommand({ kind: 'chat', content: 'hi', scope: 'lobby', messageId: 'm1' }, 'req-chat', 5_000, false);
    useAppStore.getState().beginCommand({ kind: 'ready' }, 'req-ready', 5_000, true);

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Error,
      payload: { code: 1000, message: '服务器无法识别请求来源', request_id: '' }
    }));

    expect(Object.keys(useAppStore.getState().pendingCommands)).toEqual(expect.arrayContaining(['chat', 'ready']));
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
    expect(send).toHaveBeenCalledWith(MsgType.Ready, undefined, expect.objectContaining({ request_id: expect.any(String) }));
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

    const matchRequestId = useAppStore.getState().pendingCommands['quick-match']?.requestId;
    if (!matchRequestId) throw new Error('expected pending quick match');
    act(() => useAppStore.getState().handleMessage({
      type: MsgType.CommandAck,
      payload: { request_id: matchRequestId, command_type: WireMessageType.MSG_QUICK_MATCH }
    }));

    fireEvent.click(screen.getByRole('button', { name: '取消匹配' }));
    expect(send).toHaveBeenLastCalledWith(MsgType.CancelMatch, undefined, expect.objectContaining({ request_id: expect.any(String) }));
    expect(useAppStore.getState().phase).toBe('matching');

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.MatchCancelled,
      payload: { reason: 'cancelled' }
    }));
    expect(useAppStore.getState().phase).toBe('lobby');
    expect(useAppStore.getState().pendingCommands['cancel-match']).toBeDefined();
    const cancelRequestId = useAppStore.getState().pendingCommands['cancel-match']?.requestId;
    if (!cancelRequestId) throw new Error('expected pending cancel match');
    act(() => useAppStore.getState().handleMessage({
      type: MsgType.CommandAck,
      payload: { request_id: cancelRequestId, command_type: WireMessageType.MSG_CANCEL_MATCH }
    }));
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

  it('correlates chat completion by command request_id, not message_id', () => {
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
    expect(useAppStore.getState().pendingCommands.chat).toBeDefined();

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.CommandAck,
      payload: { request_id: result.requestId, command_type: WireMessageType.MSG_CHAT }
    }));
    expect(useAppStore.getState().pendingCommands.chat).toBeUndefined();
  });

  it('ignores a late acknowledgement from a timed-out attempt after retry', () => {
    vi.useFakeTimers();
    const first = dispatchCommand(socket, { kind: 'quick-match' });
    if (!first.ok) throw new Error('expected first match request');

    act(() => vi.advanceTimersByTime(8_000));
    const retry = retryBusinessCommand(socket);
    if (!retry?.ok) throw new Error('expected retry request');
    expect(retry.requestId).not.toBe(first.requestId);

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.CommandAck,
      payload: { request_id: first.requestId, command_type: WireMessageType.MSG_QUICK_MATCH }
    }));
    expect(useAppStore.getState().pendingCommands['quick-match']?.requestId).toBe(retry.requestId);

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.CommandAck,
      payload: { request_id: retry.requestId, command_type: WireMessageType.MSG_QUICK_MATCH }
    }));
    expect(useAppStore.getState().pendingCommands['quick-match']).toBeUndefined();
  });

  it('does not let an uncorrelated warning disturb a pending command', () => {
    const result = dispatchCommand(socket, { kind: 'ready' });
    if (!result.ok) throw new Error('expected ready request');

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Warning,
      payload: { code: 1002, message: '操作频率较高' }
    }));

    expect(useAppStore.getState().pendingCommands.ready?.requestId).toBe(result.requestId);
    expect(useAppStore.getState().businessError).toMatchObject({
      category: 'rate-limit',
      message: '操作频率较高'
    });
  });

  it('clears only the uniquely correlated command error', () => {
    const stats = dispatchCommand(socket, { kind: 'stats' });
    const leaderboard = dispatchCommand(socket, {
      kind: 'leaderboard', leaderboardType: 'total', offset: 0, limit: 30
    });
    if (!stats.ok || !leaderboard.ok) throw new Error('expected query requests');
    expect(stats.requestId).not.toBe(leaderboard.requestId);

    act(() => useAppStore.getState().handleMessage({
      type: MsgType.Error,
      payload: {
        code: 1002,
        message: '查询过于频繁',
        command_type: WireMessageType.MSG_GET_STATS,
        request_id: stats.requestId
      }
    }));

    expect(useAppStore.getState().pendingCommands.stats).toBeUndefined();
    expect(useAppStore.getState().pendingCommands.leaderboard?.requestId).toBe(leaderboard.requestId);
  });

  it('attaches the current game and turn to every game mutation', () => {
    useAppStore.setState({ gameId: 'game-42', turnId: 9 });
    dispatchCommand(socket, { kind: 'pass' });

    expect(send).toHaveBeenCalledWith(MsgType.Pass, undefined, expect.objectContaining({
      request_id: expect.any(String),
      expected_game_id: 'game-42',
      expected_turn_id: 9
    }));
  });
});

function hand(...ranks: number[]): CardInfo[] {
  return ranks.map((rank, index) => ({ suit: index, rank, color: index % 2 }));
}
