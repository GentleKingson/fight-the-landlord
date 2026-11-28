import type { PlayerScore } from '../../protocol/types';
import type { Phase } from '../../stores/types';

/** Visual backdrop scene identifier for the app shell. */
export type BackgroundScene = 'lobby' | 'table' | 'settlement';

/** Outcome mood for settlement styling. */
export type SettlementOutcome = 'victory' | 'defeat' | 'neutral';

/** Maps the current application phase to its corresponding background scene. */
export function resolveBackgroundScene(phase: Phase): BackgroundScene {
  if (phase === 'game_over') return 'settlement';
  if (phase === 'dealing' || phase === 'bidding' || phase === 'playing') return 'table';
  return 'lobby';
}

/** Determines whether the settlement screen represents a victory, defeat, or neutral state. */
export function resolveSettlementOutcome(
  playerId: string,
  winnerId: string,
  winnerIsLandlord: boolean,
  scores: PlayerScore[]
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
