import type { CardInfo } from '../protocol/types';

// Reuse the CLI assets. no-inline keeps even small MP3s under /assets, which
// is served by the Go server, without embedding audio in the JavaScript bundle.
const files = import.meta.glob<string>([
  '../../../internal/sound/gaming/voices/male/{single,pair,trio,type}/*.mp3',
  '../../../internal/sound/gaming/effects/{play,straight,plane,bomb,turn,warning,deal,win,lose}.mp3',
  '../../../internal/sound/gaming/voices/male/bid/{bid_call,bid_nocall,bid_grab,bid_nograb}.mp3',
  '../../../internal/sound/gaming/voices/male/pass/pass_buyao.mp3',
  '../../../internal/sound/gaming/voices/male/alert/{last1,last2}.mp3',
], { eager: true, query: '?url&no-inline', import: 'default' });

export const cardSoundUrls: Record<string, string> = Object.fromEntries(
  Object.entries(files).map(([path, url]) => [path.split('/').pop()!.replace('.mp3', ''), url]),
);

const ranks: Record<number, string> = {
  11: 'J', 12: 'Q', 13: 'K', 14: 'A', 15: '2', 16: 'joker_small', 17: 'joker_big',
};
const prefixes: Record<string, string> = { 单张: 'single', 对子: 'pair', 三张: 'trio' };
const combinations: Record<string, string[]> = {
  三带一: ['type_trio_single'], 三带二: ['type_trio_pair'],
  顺子: ['type_straight', 'straight'], 连对: ['type_pairstraight'],
  飞机: ['type_plane', 'plane'], 飞机带单: ['type_plane', 'plane'], 飞机带对: ['type_plane', 'plane'],
  四带二: ['type_four_two'], 四带两对: ['type_four_twopair'],
  炸弹: ['type_bomb', 'bomb'], 王炸: ['type_rocket', 'bomb'],
};

export function cardSoundNames(handType: string, cards: CardInfo[]): string[] {
  const prefix = Object.hasOwn(prefixes, handType) ? prefixes[handType] : undefined;
  if (prefix) {
    const counts = new Map<number, number>();
    let best = 0;
    for (const { rank } of cards) {
      counts.set(rank, (counts.get(rank) ?? 0) + 1);
      if (counts.get(rank)! > (counts.get(best) ?? 0)) best = rank;
    }
    const name = `${prefix}_${ranks[best] ?? best}`;
    return Object.hasOwn(cardSoundUrls, name) ? [name] : ['play'];
  }
  return Object.hasOwn(combinations, handType) ? combinations[handType] : ['play'];
}
