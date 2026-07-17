import { describe, expect, it } from 'vitest';
import type { CardInfo } from '../src/protocol/types';
import {
  canBeat,
  findSmallestLegalResponse,
  listLegalResponses,
  parseHand
} from '../src/game/rules';

const previousHands: readonly number[][] = [
  [3],
  [8, 8],
  [6, 6, 6],
  [5, 5, 5, 9],
  [4, 4, 4, 7, 7],
  [3, 4, 5, 6, 7],
  [6, 6, 7, 7, 8, 8],
  [3, 3, 3, 4, 4, 4],
  [5, 5, 5, 6, 6, 6, 8, 9],
  [7, 7, 7, 8, 8, 8, 10, 10, 11, 11],
  [9, 9, 9, 9],
  [16, 17]
];

describe('rule enumeration properties', () => {
  it('only returns unique, owned, legal responses and agrees with Hint', () => {
    const random = mulberry32(0x5eedc0de);
    const deck = buildDeck();

    for (let iteration = 0; iteration < 160; iteration += 1) {
      const hand = sample(deck, 12, random);
      const previous = cardsFromRanks(previousHands[iteration % previousHands.length]);
      const responses = listLegalResponses(hand, previous);
      const signatures = responses.map(cardSignature);

      expect(new Set(signatures).size).toBe(responses.length);
      for (const response of responses) {
        expect(isPhysicalSubset(response, hand)).toBe(true);
        expect(parseHand(response)).not.toBeNull();
        expect(canBeat(response, previous)).toBe(true);
      }

      expect(findSmallestLegalResponse(hand, previous)).toEqual(responses[0] ?? null);
    }
  });

  it('always leads with an owned legal card and never answers a rocket', () => {
    const random = mulberry32(0xd0d0cafe);
    const deck = buildDeck();
    const rocket = cardsFromRanks([16, 17]);

    for (let iteration = 0; iteration < 80; iteration += 1) {
      const hand = sample(deck, 10, random);
      const lead = findSmallestLegalResponse(hand, null);

      expect(lead).not.toBeNull();
      expect(isPhysicalSubset(lead ?? [], hand)).toBe(true);
      expect(parseHand(lead ?? [])).not.toBeNull();
      expect(listLegalResponses(hand, rocket)).toEqual([]);
    }
  });
});

function buildDeck(): CardInfo[] {
  const deck: CardInfo[] = [];
  for (let rank = 3; rank <= 15; rank += 1) {
    for (let suit = 0; suit < 4; suit += 1) {
      deck.push({ suit, rank, color: suit === 1 || suit === 3 ? 1 : 0 });
    }
  }
  deck.push({ suit: 4, rank: 16, color: 0 });
  deck.push({ suit: 4, rank: 17, color: 1 });
  return deck;
}

function cardsFromRanks(ranks: readonly number[]): CardInfo[] {
  const occurrences = new Map<number, number>();
  return ranks.map((rank) => {
    const suit = rank >= 16 ? 4 : occurrences.get(rank) ?? 0;
    occurrences.set(rank, suit + 1);
    return { suit, rank, color: rank === 17 || suit === 1 || suit === 3 ? 1 : 0 };
  });
}

function sample(deck: readonly CardInfo[], size: number, random: () => number): CardInfo[] {
  const shuffled = [...deck];
  for (let index = shuffled.length - 1; index > 0; index -= 1) {
    const swapIndex = Math.floor(random() * (index + 1));
    [shuffled[index], shuffled[swapIndex]] = [shuffled[swapIndex], shuffled[index]];
  }
  return shuffled.slice(0, size);
}

function isPhysicalSubset(candidate: readonly CardInfo[], hand: readonly CardInfo[]): boolean {
  const available = new Set(hand.map(physicalCardKey));
  return candidate.every((card) => available.delete(physicalCardKey(card)));
}

function cardSignature(cards: readonly CardInfo[]): string {
  return [...cards].map(physicalCardKey).sort().join('|');
}

function physicalCardKey(card: CardInfo): string {
  return `${card.rank}:${card.suit}`;
}

function mulberry32(seed: number): () => number {
  return () => {
    seed |= 0;
    seed = seed + 0x6d2b79f5 | 0;
    let value = Math.imul(seed ^ seed >>> 15, 1 | seed);
    value = value + Math.imul(value ^ value >>> 7, 61 | value) ^ value;
    return ((value ^ value >>> 14) >>> 0) / 4294967296;
  };
}
