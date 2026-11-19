import type { CardInfo } from '../protocol/types';

// Keep even short clips as separate files so the browser can fetch them on demand.
const SOUND_FILES = import.meta.glob<string>('../assets/audio/gaming/**/*.mp3', {
  eager: true,
  query: '?url&no-inline',
  import: 'default'
});

/** Lookup table mapping logical clip names to static asset URLs. */
export const cardSoundUrls: Record<string, string> = Object.fromEntries(
  Object.entries(SOUND_FILES).map(([path, url]) => [(path.split('/').pop() ?? '').replace('.mp3', ''), url])
);

const RANK_NAMES: Record<number, string> = {
  11: 'J',
  12: 'Q',
  13: 'K',
  14: 'A',
  15: '2',
  16: 'joker_small',
  17: 'joker_big'
};

const HAND_PREFIXES: Record<string, string> = {
  单张: 'single',
  对子: 'pair',
  三张: 'trio'
};

const COMBINATION_SOUNDS: Record<string, string[]> = {
  三带一: ['type_trio_single'],
  三带二: ['type_trio_pair'],
  顺子: ['type_straight'],
  连对: ['type_pairstraight'],
  飞机: ['type_plane', 'plane'],
  飞机带单: ['type_plane', 'plane'],
  飞机带对: ['type_plane', 'plane'],
  四带二: ['type_four_two'],
  四带两对: ['type_four_twopair'],
  炸弹: ['type_bomb', 'bomb'],
  王炸: ['type_rocket', 'bomb']
};

/** Resolves the ordered sound clip names to play for a given hand type and cards. */
export function cardSoundNames(handType: string, cards: CardInfo[]): string[] {
  const prefix = Object.hasOwn(HAND_PREFIXES, handType) ? HAND_PREFIXES[handType] : undefined;
  if (prefix) {
    const counts = new Map<number, number>();
    let best = 0;
    for (const { rank } of cards) {
      const nextCount = (counts.get(rank) ?? 0) + 1;
      counts.set(rank, nextCount);
      if (nextCount > (counts.get(best) ?? 0)) {
        best = rank;
      }
    }
    const name = `${prefix}_${RANK_NAMES[best] ?? best}`;
    return Object.hasOwn(cardSoundUrls, name) ? [name] : ['play'];
  }
  return Object.hasOwn(COMBINATION_SOUNDS, handType) ? COMBINATION_SOUNDS[handType] : ['play'];
}
