import type { CardInfo, IncomingPayloadMap, PlayerHand, PlayerInfo, PlayerScore } from '../protocol/types';
import { sortCards } from '../shared/cards/cardModel';

export const END_REVEAL_DURATION_MS = 10_000;
export type EndView = 'reveal' | 'result' | 'review';

export interface EndSnapshot {
  roundId: string;
  roomCode: string;
  playerId: string;
  players: PlayerInfo[];
  playerHands: PlayerHand[];
  lastPlayed: CardInfo[];
  lastHandType: string;
  winnerId: string;
  winnerName: string;
  winnerIsLandlord: boolean;
  finalMultiplier: number;
  scores: PlayerScore[];
}

interface TableSource {
  roundId: string;
  roomCode: string;
  playerId: string;
  players: PlayerInfo[];
  lastPlayed: CardInfo[];
  lastHandType: string;
  lastPlayedBy: string;
}

/**
 * Copies the final table state once so later room presence updates do not
 * rewrite settlement or review history.
 */
export function captureEndSnapshot(state: TableSource, result: IncomingPayloadMap['game_over']): EndSnapshot {
  const playerHands = (result.player_hands ?? []).map((hand) => ({
    ...hand,
    cards: sortCards((hand.cards ?? []).map((card) => ({ ...card })))
  }));
  return {
    roundId: state.roundId,
    roomCode: state.roomCode,
    playerId: state.playerId,
    players: state.players.map((player) => ({
      ...player,
      cards_count: playerHands.find((hand) => hand.player_id === player.id)?.cards.length ?? player.cards_count
    })),
    playerHands,
    // Never label an unrelated earlier play as the winning hand.
    lastPlayed: state.lastPlayedBy === result.winner_id ? state.lastPlayed.map((card) => ({ ...card })) : [],
    lastHandType: state.lastPlayedBy === result.winner_id ? state.lastHandType : '',
    winnerId: result.winner_id,
    winnerName: result.winner_name,
    winnerIsLandlord: result.is_landlord,
    finalMultiplier: result.multiplier || 1,
    scores: (result.scores ?? []).map((score) => ({ ...score }))
  };
}
