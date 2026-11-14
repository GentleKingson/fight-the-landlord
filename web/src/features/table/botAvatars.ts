import blossom from '../../assets/bot-avatars/blossom.webp';
import grandma from '../../assets/bot-avatars/grandma.webp';
import jade from '../../assets/bot-avatars/jade.webp';
import scholar from '../../assets/bot-avatars/scholar.webp';
import sunshine from '../../assets/bot-avatars/sunshine.webp';
import uncle from '../../assets/bot-avatars/uncle.webp';
import type { PlayerInfo } from '../../protocol/types';

const FNV_OFFSET_BASIS = 2166136261;
const FNV_PRIME = 16777619;

/** Ordered catalog of bundled bot avatar illustrations. */
export const BOT_AVATARS = [scholar, jade, uncle, blossom, sunshine, grandma] as const;

function hashId(value: string): number {
  let hash = FNV_OFFSET_BASIS;
  for (let i = 0; i < value.length; i++) {
    hash = Math.imul(hash ^ value.charCodeAt(i), FNV_PRIME);
  }
  return hash >>> 0;
}

/**
 * Deterministically assigns distinct avatar URLs to bot seats by hashing sorted bot IDs.
 * Random bot UUIDs provide variety each match without storage or render-time randomness.
 * Sorting IDs and drawing without replacement keeps both seats distinct and makes
 * refresh, reconnect, seat order, name/role changes, and redeals reproduce the same pair.
 */
export function assignBotAvatars(players: readonly PlayerInfo[]): Map<string, string> {
  const ids = [...new Set(players.filter((player) => player.is_bot && player.id).map((player) => player.id))].sort();
  const available: string[] = [...BOT_AVATARS];
  const assignments = new Map<string, string>();
  for (const id of ids) {
    if (!available.length) break;
    const index = hashId(id) % available.length;
    assignments.set(id, available.splice(index, 1)[0]);
  }
  return assignments;
}
