import type { CardInfo } from '../../protocol/generated';
import type { UtilityDrawer } from '../../protocol/types';

export type CommandKind =
  | 'create-room'
  | 'join-room'
  | 'quick-match'
  | 'practice-match'
  | 'cancel-match'
  | 'ready'
  | 'cancel-ready'
  | 'bid'
  | 'play'
  | 'pass'
  | 'leave-room'
  | 'chat';

export type CommandRequest =
  | { kind: 'create-room' }
  | { kind: 'join-room'; roomCode: string }
  | { kind: 'quick-match' }
  | { kind: 'practice-match' }
  | { kind: 'cancel-match' }
  | { kind: 'ready' }
  | { kind: 'cancel-ready' }
  | { kind: 'bid'; bid: boolean }
  | { kind: 'play'; cards: CardInfo[] }
  | { kind: 'pass' }
  | { kind: 'leave-room' }
  | { kind: 'chat'; content: string; scope: string; messageId: string };

export type BusinessErrorCategory = 'validation' | 'rate-limit' | 'maintenance' | 'not-in-room' | 'timeout' | 'network';

export interface PendingCommand {
  kind: CommandKind;
  request: CommandRequest;
  startedAt: number;
  timeoutAt: number;
  retryable: boolean;
}

export interface BusinessError {
  id: number;
  category: BusinessErrorCategory;
  message: string;
  command?: CommandKind;
  retry?: CommandRequest;
}

export interface UiSlice {
  selectedCards: Set<string>;
  drawer: UtilityDrawer;
  chatInput: string;
  tableMessage: string;
  pendingCommands: Partial<Record<CommandKind, PendingCommand>>;
  businessError: BusinessError | null;
}

export const initialUiSlice: UiSlice = {
  selectedCards: new Set<string>(),
  drawer: 'none',
  chatInput: '',
  tableMessage: '',
  pendingCommands: {},
  businessError: null
};
