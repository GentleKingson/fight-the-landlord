import { expect, it } from 'vitest';
import { canBeat, handSignature, parseHand, suggestPlays } from '../src/shared/cards/rules';
import type { CardInfo } from '../src/protocol/types';

function cards(ranks: number[]): CardInfo[] {
  const counts = new Map<number, number>();
  return ranks.map(rank => {
    const suit = rank >= 16 ? 4 : counts.get(rank) ?? 0;
    counts.set(rank, suit + 1);
    return { rank, suit, color: suit % 2 };
  });
}

// Independent bounded oracle: enumerate every rank-count subset, then ask the rule parser.
function legalSubsets(hand: CardInfo[]): CardInfo[][] {
  let subsets: CardInfo[][] = [[]];
  for (const rank of new Set(hand.map(card => card.rank))) {
    const group = hand.filter(card => card.rank === rank);
    subsets = subsets.flatMap(subset => Array.from({ length: group.length + 1 }, (_, n) => [...subset, ...group.slice(0, n)]));
  }
  return subsets.filter(subset => parseHand(subset));
}

it.each([
  [3, 3, 4, 5, 6, 7, 8],
  [3, 4, 5, 5, 5, 6, 6, 6],
  [3, 3, 3, 3, 4, 4, 5, 5, 16, 17],
  [3, 3, 3, 4, 4, 4, 5, 5, 6, 6],
  [3, 3, 3, 4, 4, 4, 5, 5, 5, 6, 6, 6],
  [10, 10, 11, 11, 12, 12, 13, 13, 14, 14, 15, 15, 16, 17],
  [3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 16, 17]
].map(ranks => ({ ranks })))('matches all legal rank combinations for $ranks', ({ ranks }) => {
  const hand = cards(ranks);
  const original = JSON.stringify(hand);
  const all = legalSubsets(hand);
  const suggestions = suggestPlays(hand, [], true);
  expect(new Set(suggestions.map(handSignature))).toEqual(new Set(all.map(handSignature)));
  expect(suggestions).toHaveLength(new Set(suggestions.map(handSignature)).size);
  expect(suggestPlays([...hand].reverse(), [], true)).toEqual(suggestions);
  for (const suggestion of suggestions) {
    expect(new Set(suggestion).size).toBe(suggestion.length);
    for (const card of suggestion) expect(hand).toContain(card);
  }
  // Include a target for every supported shape, with multiple sequence lengths.
  const targets = new Map(all.map(move => {
    const parsed = parseHand(move)!;
    return [`${parsed.type}:${parsed.length}`, move] as const;
  }));
  for (const target of targets.values()) {
    expect(new Set(suggestPlays(hand, target, false).map(handSignature))).toEqual(
      new Set(all.filter(move => canBeat(parseHand(move), parseHand(target))).map(handSignature))
    );
  }
  expect(JSON.stringify(hand)).toBe(original);
});

it('offers every attachment and preserves pairs and bombs before splitting them', () => {
  expect(suggestPlays(cards([3, 4, 5, 5, 5]), cards([4, 4, 4, 6]), false).map(handSignature))
    .toEqual(['3,5,5,5', '4,5,5,5']);
  expect(suggestPlays(cards([5, 5, 6, 7, 7, 7, 7, 16, 17]), cards([4]), false).map(handSignature))
    .toEqual(['6', '5', '16', '17', '7', '7,7,7,7', '16,17']);
});

it('covers long planes and their different wings on a 20-card hand', () => {
  const hand = cards([3, 3, 3, 4, 4, 4, 5, 5, 5, 6, 6, 6, 8, 8, 9, 9, 10, 10, 11, 11]);
  const all = legalSubsets(hand);
  expect(new Set(suggestPlays(hand, [], true).map(handSignature))).toEqual(new Set(all.map(handSignature)));
});

it('does not offer replies to a rocket or invalid target, but can lead after a rocket', () => {
  const hand = cards([3, 3, 3, 3, 16, 17]);
  expect(suggestPlays(hand, cards([16, 17]), false)).toEqual([]);
  expect(suggestPlays(hand, cards([3, 4]), false)).toEqual([]);
  expect(suggestPlays(hand, cards([16, 17]), true)).toEqual(suggestPlays(hand, [], true));
});
