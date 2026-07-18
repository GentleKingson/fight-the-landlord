import type { LeaderboardEntry, RoomListItem, StatsResultPayload } from '../../protocol/generated';
import type { LeaderboardType, LobbyPanel } from '../../protocol/types';

export interface LobbySlice {
  onlineCount: number;
  lobbyPanel: LobbyPanel;
  roomCodeInput: string;
  stats: StatsResultPayload | null;
  leaderboard: LeaderboardEntry[];
  leaderboardType: LeaderboardType;
  roomList: RoomListItem[];
  matchDeadlineMs: number;
  matchPractice: boolean;
}

export const initialLobbySlice: LobbySlice = {
  onlineCount: 0,
  lobbyPanel: 'home',
  roomCodeInput: '',
  stats: null,
  leaderboard: [],
  leaderboardType: 'total',
  roomList: [],
  matchDeadlineMs: 0,
  matchPractice: false
};
