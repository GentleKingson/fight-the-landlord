import type { PlayerInfo } from '../../protocol/generated';
import type { Phase } from '../../protocol/types';

export interface RoomSlice {
  phase: Phase;
  roomCode: string;
  players: PlayerInfo[];
}

export const initialRoomSlice: RoomSlice = {
  phase: 'connecting',
  roomCode: '',
  players: []
};

export function mergePlayer(players: PlayerInfo[], next: PlayerInfo): PlayerInfo[] {
  return players.some((player) => player.id === next.id)
    ? players.map((player) => player.id === next.id ? next : player)
    : [...players, next].sort((a, b) => a.seat - b.seat);
}

export function normalizeRoomPlayer(player: PlayerInfo): PlayerInfo {
  return {
    ...player,
    online: player.online ?? true,
    cards_count: player.cards_count ?? 0
  };
}

export function normalizeRoomPlayers(players: PlayerInfo[]): PlayerInfo[] {
  return players.map(normalizeRoomPlayer).sort((a, b) => a.seat - b.seat);
}

export function normalizeGamePlayers(
  players: PlayerInfo[],
  currentPlayerId: string,
  currentHandCount = 17
): PlayerInfo[] {
  return players
    .map((player) => ({
      ...player,
      online: player.online ?? true,
      cards_count: player.cards_count ?? (player.id === currentPlayerId ? currentHandCount : 17)
    }))
    .sort((a, b) => a.seat - b.seat);
}

export function syncDealtCounts(
  players: PlayerInfo[],
  currentPlayerId: string,
  handCount: number
): PlayerInfo[] {
  return players.map((player) => ({
    ...player,
    online: player.online ?? true,
    cards_count: player.id === currentPlayerId ? handCount : (player.cards_count ?? 17)
  }));
}

export function syncLandlordCounts(
  players: PlayerInfo[],
  currentPlayerId: string,
  landlordId: string,
  currentHandCount: number
): PlayerInfo[] {
  return players.map((player) => {
    const isLandlord = player.id === landlordId;
    return {
      ...player,
      online: player.online ?? true,
      is_landlord: isLandlord,
      cards_count: player.id === currentPlayerId
        ? currentHandCount
        : (player.cards_count ?? (isLandlord ? 20 : 17))
    };
  });
}
