import { describe, expect, it } from 'vitest';
import fixtures from '../../testdata/classic-rules.json';
import type { CardInfo } from '../src/protocol/types';
import { parseHand, canBeat } from '../src/shared/cards/rules';
import { generateSimpleSuggestions, summarizeHand, cardKey } from '../src/shared/cards/cardModel';

function cards(ranks: number[]): CardInfo[] {
  const counts = new Map<number, number>();
  return ranks.map((rank) => {
    const suit = counts.get(rank) ?? 0;
    counts.set(rank, suit + 1);
    return { rank, suit: rank >= 16 ? 4 : suit, color: rank === 17 ? 1 : 0 };
  });
}
const hands = new Map(fixtures.hands.map((fixture) => [fixture.id, cards(fixture.cards)]));

describe('shared classic rule contract', () => {
  for (const fixture of fixtures.hands) it(`parses ${fixture.id}`, () => {
    const hand = cards(fixture.cards);
    if (fixture.type === '无效') {
      expect(parseHand(hand)).toBeNull();
      if (hand.length) expect(summarizeHand(hand)).toBe('无效牌型');
    } else {
      const expected = { type: fixture.type, keyRank: fixture.keyRank, length: fixture.length };
      expect(parseHand(hand)).toEqual(expected);
      expect(parseHand([...hand].reverse())).toEqual(expected);
      expect(summarizeHand(hand)).toBe(fixture.type);
    }
  });
  for (const fixture of fixtures.comparisons) it(`compares ${fixture.play} against ${fixture.target}`, () => {
    expect(canBeat(parseHand(hands.get(fixture.play)!), parseHand(hands.get(fixture.target)!))).toBe(fixture.canBeat);
  });
  for (const fixture of fixtures.replies) it(`suggests legal cards for ${fixture.id}`, () => {
    const hand = cards(fixture.hand);
    const before = [...hand];
    const target = hands.get(fixture.target)!;
    const suggestions = generateSimpleSuggestions(hand, target, false);
    expect(hand).toEqual(before);
    expect(suggestions.length > 0).toBe(fixture.canBeat);
    if (fixture.canBeat) expect(summarizeHand(suggestions[0])).toBe(fixture.type);
    for (const suggestion of suggestions) {
      expect(canBeat(parseHand(suggestion), parseHand(target))).toBe(true);
      const keys = suggestion.map(cardKey);
      expect(new Set(keys).size).toBe(keys.length);
      for (const key of keys) expect(hand.map(cardKey)).toContain(key);
    }
  });
  it('leads with a legal small card even when the previous trick was a rocket', () => {
    expect(generateSimpleSuggestions(cards([9, 3]), cards([16, 17]), true)).toEqual([cards([3]), cards([9])]);
    expect(generateSimpleSuggestions([], [], true)).toEqual([]);
  });
});
