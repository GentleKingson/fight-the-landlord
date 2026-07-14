import { create } from 'zustand';
import type { ChatPayload } from '../../protocol/generated';

export interface ChatSlice {
  messages: ChatPayload[];
  push: (message: ChatPayload) => void;
  clear: () => void;
}

export const useChatStore = create<ChatSlice>((set, get) => ({
  messages: [],
  push: (message) => set({ messages: [...get().messages, message].slice(-80) }),
  clear: () => set({ messages: [] })
}));
