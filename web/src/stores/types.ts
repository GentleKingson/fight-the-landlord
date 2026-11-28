import type {
  CardInfo,
  IncomingMessage,
  LeaderboardEntry,
  PlayerInfo,
  PlayerScore,
  PlayerHand,
  RoomListItem,
  StatsResultPayload
} from '../protocol/types';
import type { EndSnapshot, EndView } from './endGame';
export type Phase =
  'connecting' | 'lobby' | 'matching' | 'waiting' | 'dealing' | 'bidding' | 'playing' | 'game_over';
export type LobbyPanel = 'home' | 'leaderboard' | 'stats' | 'rules';
export type UtilityDrawer = 'none' | 'counter' | 'history' | 'rules';

interface ConnectionSlice {
  submission: 'idle' | 'pending' | 'unknown' | 'confirmed' | 'rejected';
  submissionMessage: string;
  connected: boolean;
  sessionReplaced: boolean;
  playerId: string;
  playerName: string;
  latency: number;
  error: string;
  maintenance: boolean;
}

interface LobbySlice {
  roomRequest: 'create_room' | 'join_room' | 'ready' | 'cancel_ready' | 'start_game' | null;
  cancellingMatch: boolean;
  matchMode: 'practice' | 'online';
  phase: Phase;
  roomCode: string;
  ownerId: string;
  players: PlayerInfo[];
  onlineCount: number;
  lobbyPanel: LobbyPanel;
  roomCodeInput: string;
  stats: StatsResultPayload | null;
  leaderboard: LeaderboardEntry[];
  leaderboardType: string;
  roomList: RoomListItem[];
}

/** Local wall-clock window for an active card-dealing animation. */
export interface DealStage {
  id: string;
  startAt: number;
  endAt: number;
}

export interface TableSlice {
  roundId: string;
  endSnapshot: EndSnapshot | null;
  endView: EndView;
  revealDeadline: number | null;
  deal: DealStage | null;
  bidToken: string;
  turnToken: string;
  hand: CardInfo[];
  bottomCards: CardInfo[];
  bottomCardsRevealed: boolean;
  currentTurn: string;
  lastPlayed: CardInfo[];
  lastPlayedBy: string;
  lastPlayedName: string;
  lastHandType: string;
  mustPlay: boolean;
  canBeat: boolean;
  multiplier: number;
  isGrabTurn: boolean;
  timeout: number;
  timerStart: number;
  serverDeadline: number;
  serverTimestamp: number;
  winnerId: string;
  winnerName: string;
  winnerIsLandlord: boolean;
  finalMultiplier: number;
  scores: PlayerScore[];
  playerHands: PlayerHand[];
  cardCounter: Record<number, number>;
  recentActions: TableAction[];
  seatActions: Record<string, SeatAction>;
}

interface UiSlice {
  selectedCards: Set<string>;
  drawer: UtilityDrawer;
  tableMessage: string;
}

/** Recent play, pass, bid, or system event recorded in the table history drawer. */
export interface TableAction {
  type: 'play' | 'pass' | 'bid' | 'system';
  player_id?: string;
  player_name?: string;
  cards?: CardInfo[];
  hand_type?: string;
  label?: string;
}

/** Most recent action displayed next to a player's seat on the table. */
export interface SeatAction {
  type: 'play' | 'pass' | 'bid';
  player_id: string;
  player_name?: string;
  cards?: CardInfo[];
  hand_type?: string;
  label?: string;
}

interface AppActions {
  setConnected: (connected: boolean) => void;
  setError: (error: string) => void;
  setLobbyPanel: (panel: LobbyPanel) => void;
  setRoomCodeInput: (value: string) => void;
  setDrawer: (drawer: UtilityDrawer) => void;
  toggleCard: (key: string) => void;
  setSelection: (keys: string[]) => void;
  clearSelection: () => void;
  leaveLocalRoom: () => void;
  showSettlement: () => void;
  reviewEndTable: () => void;
  handleMessage: (message: IncomingMessage) => void;
}

/** Combined application state and store actions. */
export type AppState = ConnectionSlice & LobbySlice & TableSlice & UiSlice & AppActions;
