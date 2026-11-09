import type { PlayerInfo } from '../../protocol/types';
import scholar from '../../assets/bot-avatars/scholar.webp';
import jade from '../../assets/bot-avatars/jade.webp';
import uncle from '../../assets/bot-avatars/uncle.webp';
import blossom from '../../assets/bot-avatars/blossom.webp';
import sunshine from '../../assets/bot-avatars/sunshine.webp';
import grandma from '../../assets/bot-avatars/grandma.webp';

export const BOT_AVATARS = [scholar, jade, uncle, blossom, sunshine, grandma] as const;

function hashId(value: string): number {
  let hash = 2166136261;
  for (let i = 0; i < value.length; i++) {
    hash = Math.imul(hash ^ value.charCodeAt(i), 16777619);
  }
  return hash >>> 0;
}

/** Random bot UUIDs provide variety each match, without storage or render-time randomness.
 * Sorting IDs and drawing without replacement keeps both seats distinct and makes
 * refresh, reconnect, seat order, name/role changes and redeals reproduce the same pair.
 */
export function assignBotAvatars(players: readonly PlayerInfo[]): Map<string, string> {
  const ids = [...new Set(players.filter(player => player.is_bot && player.id).map(player => player.id))].sort();
  const available: string[] = [...BOT_AVATARS];
  const assignments = new Map<string, string>();
  for (const id of ids) {
    if (!available.length) break;
    const index = hashId(id) % available.length;
    assignments.set(id, available.splice(index, 1)[0]);
  }
  return assignments;
}
