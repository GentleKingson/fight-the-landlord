import { MsgType } from '../protocol/types';
import {
  useAppStore,
  type CommandKind,
  type CommandRequest,
  type PendingCommand
} from '../stores/appStore';
import type { GameSocket, SendResult } from './wsClient';

export type CommandDispatchResult =
  | { ok: true; request: CommandRequest }
  | { ok: false; reason: Extract<SendResult, { ok: false }>['reason'] | 'duplicate' | 'validation' | 'maintenance' };

const SAFE_RETRY_COMMANDS = new Set<CommandKind>([
  'quick-match',
  'cancel-match',
  'ready',
  'cancel-ready',
  'leave-room'
]);

const ROOM_TRANSITION_COMMANDS = new Set<CommandKind>([
  'create-room',
  'join-room',
  'quick-match',
  'practice-match',
  'cancel-match',
  'leave-room'
]);

const READY_COMMANDS = new Set<CommandKind>(['ready', 'cancel-ready']);
const PLAY_COMMANDS = new Set<CommandKind>(['play', 'pass']);

export function dispatchCommand(socket: GameSocket, rawRequest: CommandRequest): CommandDispatchResult {
  const request = normalizeRequest(rawRequest);
  const store = useAppStore.getState();

  if (!request) {
    store.setBusinessError('validation', validationMessage(rawRequest));
    return { ok: false, reason: 'validation' };
  }

  if (hasPendingConflict(request.kind, store.pendingCommands)) {
    return { ok: false, reason: 'duplicate' };
  }

  if (store.maintenance && blockedByMaintenance(request.kind)) {
    store.setBusinessError('maintenance', '服务器维护中，暂时无法执行该操作');
    return { ok: false, reason: 'maintenance' };
  }

  const result = sendRequest(socket, request);
  if (!result.ok) {
    const message = networkFailureMessage(request.kind, result.reason);
    store.setBusinessError(
      'network',
      message,
      SAFE_RETRY_COMMANDS.has(request.kind) ? request : undefined,
      request.kind
    );
    return { ok: false, reason: result.reason };
  }

  store.clearBusinessError();
  store.beginCommand(request, commandTimeoutMs(request.kind), SAFE_RETRY_COMMANDS.has(request.kind));
  return { ok: true, request };
}

export function retryBusinessCommand(socket: GameSocket): CommandDispatchResult | null {
  const retry = useAppStore.getState().businessError?.retry;
  return retry ? dispatchCommand(socket, retry) : null;
}

export function createChatCommand(content: string, scope: string): CommandRequest {
  return {
    kind: 'chat',
    content,
    scope,
    messageId: createMessageID()
  };
}

function sendRequest(socket: GameSocket, request: CommandRequest): SendResult {
  switch (request.kind) {
    case 'create-room': return socket.send(MsgType.CreateRoom);
    case 'join-room': return socket.send(MsgType.JoinRoom, { room_code: request.roomCode });
    case 'quick-match': return socket.send(MsgType.QuickMatch);
    case 'practice-match': return socket.send(MsgType.PracticeMatch);
    case 'cancel-match': return socket.send(MsgType.CancelMatch);
    case 'ready': return socket.send(MsgType.Ready);
    case 'cancel-ready': return socket.send(MsgType.CancelReady);
    case 'bid': return socket.send(MsgType.Bid, { bid: request.bid });
    case 'play': return socket.send(MsgType.PlayCards, { cards: request.cards });
    case 'pass': return socket.send(MsgType.Pass);
    case 'leave-room': return socket.send(MsgType.LeaveRoom);
    case 'chat': return socket.send(MsgType.Chat, {
      content: request.content,
      scope: request.scope,
      message_id: request.messageId
    });
  }
}

function normalizeRequest(request: CommandRequest): CommandRequest | null {
  switch (request.kind) {
    case 'join-room': {
      const roomCode = request.roomCode.trim();
      return roomCode ? { ...request, roomCode } : null;
    }
    case 'play':
      return request.cards.length ? request : null;
    case 'chat': {
      const content = request.content.trim();
      const scope = request.scope.trim();
      return content && scope && request.messageId ? { ...request, content, scope } : null;
    }
    default:
      return request;
  }
}

function validationMessage(request: CommandRequest): string {
  if (request.kind === 'join-room') return '请输入房间号';
  if (request.kind === 'play') return '请选择要出的牌';
  if (request.kind === 'chat') return '请输入聊天内容';
  return '无法提交当前操作';
}

function hasPendingConflict(
  kind: CommandKind,
  pendingCommands: Partial<Record<CommandKind, PendingCommand>>
): boolean {
  if (pendingCommands[kind]) return true;
  const pendingKinds = Object.keys(pendingCommands) as CommandKind[];
  if (ROOM_TRANSITION_COMMANDS.has(kind)) return pendingKinds.some((pending) => ROOM_TRANSITION_COMMANDS.has(pending));
  if (READY_COMMANDS.has(kind)) return pendingKinds.some((pending) => READY_COMMANDS.has(pending));
  if (PLAY_COMMANDS.has(kind)) return pendingKinds.some((pending) => PLAY_COMMANDS.has(pending));
  return false;
}

function blockedByMaintenance(kind: CommandKind): boolean {
  return kind !== 'cancel-match' && kind !== 'leave-room' && kind !== 'chat';
}

function commandTimeoutMs(kind: CommandKind): number {
  if (kind === 'chat' || kind === 'bid' || kind === 'play' || kind === 'pass') return 5_000;
  return 8_000;
}

function networkFailureMessage(kind: CommandKind, reason: 'not-connected' | 'encode-failed' | 'send-failed'): string {
  const action = commandName(kind);
  if (reason === 'not-connected') return `${action}失败：连接尚未恢复`;
  if (reason === 'encode-failed') return `${action}失败：请求格式无效`;
  return `${action}失败：消息未能发送`;
}

function commandName(kind: CommandKind): string {
  switch (kind) {
    case 'create-room': return '创建房间';
    case 'join-room': return '加入房间';
    case 'quick-match': return '快速匹配';
    case 'practice-match': return '人机练习';
    case 'cancel-match': return '取消匹配';
    case 'ready': return '准备';
    case 'cancel-ready': return '取消准备';
    case 'bid': return '叫地主';
    case 'play': return '出牌';
    case 'pass': return '不出';
    case 'leave-room': return '离开房间';
    case 'chat': return '发送消息';
  }
}

function createMessageID(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID();
  return `web-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}
