import { create } from 'zustand';
import type { ChatPayload } from '../../protocol/generated';

const MAX_MESSAGES_PER_CONTEXT = 80;
const EMPTY_MESSAGES: readonly AuthoritativeChatMessage[] = Object.freeze([]);

export type ChatContextKey = 'lobby' | `room:${string}` | `game:${string}`;

export type AuthoritativeChatMessage = ChatPayload & {
  message_id: string;
  server_time: number;
};

export interface ChatSlice {
  buckets: Partial<Record<ChatContextKey, AuthoritativeChatMessage[]>>;
  push: (message: ChatPayload) => boolean;
  clear: (context?: ChatContextKey) => void;
}

export const useChatStore = create<ChatSlice>((set, get) => ({
  buckets: {},
  push: (message) => {
    const context = authoritativeChatContext(message);
    if (!context) return false;

    const current = get().buckets[context] ?? [];
    if (current.some((item) => item.message_id === message.message_id)) return false;

    const next = [...current, message as AuthoritativeChatMessage]
      .sort((left, right) => left.server_time - right.server_time)
      .slice(-MAX_MESSAGES_PER_CONTEXT);
    set({ buckets: { ...get().buckets, [context]: next } });
    return true;
  },
  clear: (context) => {
    if (!context) {
      set({ buckets: {} });
      return;
    }
    const buckets = { ...get().buckets };
    delete buckets[context];
    set({ buckets });
  }
}));

export function authoritativeChatContext(message: ChatPayload): ChatContextKey | null {
  if (!message.message_id || !Number.isSafeInteger(message.server_time) || (message.server_time ?? 0) <= 0) {
    return null;
  }

  switch (message.scope) {
    case 'lobby':
      return message.room_code || message.game_id ? null : 'lobby';
    case 'room':
      return message.room_code && !message.game_id ? `room:${message.room_code}` : null;
    case 'game':
      return message.room_code && message.game_id ? `game:${message.game_id}` : null;
    default:
      return null;
  }
}

export function selectChatMessages(context: ChatContextKey | null) {
  return (state: ChatSlice): readonly AuthoritativeChatMessage[] => (
    context ? state.buckets[context] ?? EMPTY_MESSAGES : EMPTY_MESSAGES
  );
}

export function roomChatContext(roomCode: string): ChatContextKey | null {
  return roomCode ? `room:${roomCode}` : null;
}

export function gameChatContext(gameId: string): ChatContextKey | null {
  return gameId ? `game:${gameId}` : null;
}
