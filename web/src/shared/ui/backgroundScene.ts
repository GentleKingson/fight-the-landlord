import type { Phase, PlayerScore } from '../../protocol/types';

export type BackgroundScene = 'lobby' | 'table' | 'settlement';
export type SettlementOutcome = 'victory' | 'defeat' | 'neutral';

export function resolveBackgroundScene(phase: Phase): BackgroundScene {
  if (phase === 'game_over') return 'settlement';
  if (phase === 'dealing' || phase === 'bidding' || phase === 'playing') return 'table';
  return 'lobby';
}

export function resolveSettlementOutcome(
  playerId: string,
  winnerId: string,
  winnerIsLandlord: boolean,
  scores: PlayerScore[],
): SettlementOutcome {
  if (!playerId) return 'neutral';
  if (winnerId) {
    if (winnerId === playerId) return 'victory';
    const me = scores.find((score) => score.player_id === playerId);
    if (me) return me.is_landlord === winnerIsLandlord ? 'victory' : 'defeat';
  }
  const me = scores.find((score) => score.player_id === playerId);
  if (!me || me.score === 0) return 'neutral';
  return me.score > 0 ? 'victory' : 'defeat';
}
