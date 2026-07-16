import type { MessageName, PayloadByName, ProtocolMessage } from './generated';

export { MessageType as WireMessageType, MsgType } from './generated';
export type {
  CardInfo,
  ChatPayload,
  CommandAckPayload,
  CommandMeta,
  EventMeta,
  ErrorPayload,
  GameSettlementDTO,
  GameStateDTO,
  LeaderboardEntry,
  PlayerHand,
  PlayerInfo,
  PlayerPlayedCards,
  PlayerScore,
  PongPayload,
  HelloPayload,
  ReconnectedPayload,
  RoomListItem,
  StatsResultPayload,
  WarningPayload
} from './generated';

export type MessageType = MessageName;
export type IncomingPayloadMap = PayloadByName;
export type IncomingMessage = ProtocolMessage;
export type OutgoingPayload = Record<string, unknown> | undefined;

export type Phase = 'connecting' | 'lobby' | 'matching' | 'waiting' | 'bidding' | 'playing' | 'game_over';
export type LobbyPanel = 'home' | 'leaderboard' | 'stats' | 'rules' | 'chat';
export type UtilityDrawer = 'none' | 'chat' | 'counter' | 'history' | 'rules';
