import { describe, expect, it } from 'vitest';
import type { CardInfo } from '../src/protocol/types';
import {
  HandType,
  canBeat,
  compareHands,
  findSmallestLegalResponse,
  handTypeLabels,
  listLegalResponses,
  parseHand,
  type ParsedHand
} from '../src/game/rules';

describe('parseHand', () => {
  const legalCases: Array<{
    name: string;
    ranks: number[];
    type: HandType;
    keyRank: number;
    length: number;
  }> = [
    { name: 'single', ranks: [5], type: HandType.Single, keyRank: 5, length: 0 },
    { name: 'pair', ranks: [7, 7], type: HandType.Pair, keyRank: 7, length: 0 },
    { name: 'trio', ranks: [9, 9, 9], type: HandType.Trio, keyRank: 9, length: 0 },
    { name: 'trio with single', ranks: [3, 3, 3, 4], type: HandType.TrioWithSingle, keyRank: 3, length: 0 },
    { name: 'trio with pair', ranks: [14, 14, 14, 13, 13], type: HandType.TrioWithPair, keyRank: 14, length: 0 },
    { name: 'straight', ranks: [3, 4, 5, 6, 7], type: HandType.Straight, keyRank: 3, length: 5 },
    { name: 'pair straight', ranks: [8, 8, 9, 9, 10, 10], type: HandType.PairStraight, keyRank: 8, length: 3 },
    { name: 'plane', ranks: [3, 3, 3, 4, 4, 4], type: HandType.Plane, keyRank: 3, length: 2 },
    { name: 'plane with singles', ranks: [5, 5, 5, 6, 6, 6, 7, 8], type: HandType.PlaneWithSingles, keyRank: 5, length: 2 },
    { name: 'plane with pairs', ranks: [9, 9, 9, 10, 10, 10, 11, 11, 12, 12], type: HandType.PlaneWithPairs, keyRank: 9, length: 2 },
    { name: 'bomb', ranks: [8, 8, 8, 8], type: HandType.Bomb, keyRank: 8, length: 0 },
    { name: 'four with two singles', ranks: [4, 4, 4, 4, 5, 6], type: HandType.FourWithTwo, keyRank: 4, length: 0 },
    { name: 'four with one pair', ranks: [4, 4, 4, 4, 5, 5], type: HandType.FourWithTwo, keyRank: 4, length: 0 },
    { name: 'four with two pairs', ranks: [11, 11, 11, 11, 12, 12, 13, 13], type: HandType.FourWithTwoPairs, keyRank: 11, length: 0 },
    { name: 'rocket', ranks: [16, 17], type: HandType.Rocket, keyRank: 17, length: 0 }
  ];

  for (const testCase of legalCases) {
    it(`parses ${testCase.name}`, () => {
      const parsed = parseHand(cards(...testCase.ranks));
      expect(parsed).toMatchObject({
        type: testCase.type,
        keyRank: testCase.keyRank,
        length: testCase.length
      });
      expect(parsed?.cards).toHaveLength(testCase.ranks.length);
      expect(handTypeLabels[testCase.type]).not.toBe('');
    });
  }

  it.each([
    ['an empty selection', []],
    ['a two-card pseudo straight', [3, 4]],
    ['a three-card pseudo straight', [3, 4, 5]],
    ['a four-card pseudo straight', [3, 4, 5, 6]],
    ['only two consecutive pairs', [3, 3, 4, 4]],
    ['a pseudo pair straight with 3/2/1 counts', [3, 3, 3, 4, 4, 5]],
    ['a straight containing 2', [10, 11, 12, 13, 14, 15]],
    ['a straight containing a joker', [10, 11, 12, 13, 16]],
    ['a pair straight containing 2', [13, 13, 14, 14, 15, 15]],
    ['a plane containing 2', [14, 14, 14, 15, 15, 15]],
    ['trio with two unrelated singles', [3, 3, 3, 4, 5]],
    ['plane single wings supplied by one pair', [3, 3, 3, 4, 4, 4, 5, 5]],
    ['four with two pairs supplied by another four', [3, 3, 3, 3, 4, 4, 4, 4]]
  ])('rejects %s', (_name, ranks) => {
    expect(parseHand(cards(...ranks))).toBeNull();
  });
});

describe('hand comparison', () => {
  it('compares the key group rather than kickers', () => {
    expect(compareHands(cards(4, 4, 4, 3), cards(3, 3, 3, 14))).toBe(1);
    expect(compareHands(cards(3, 3, 3, 17), cards(4, 4, 4, 5))).toBe(-1);
  });

  it('requires matching ordinary types and matching sequence lengths', () => {
    expect(compareHands(cards(8, 8), cards(7))).toBeNull();
    expect(compareHands(cards(4, 5, 6, 7, 8), cards(3, 4, 5, 6, 7))).toBe(1);
    expect(compareHands(cards(4, 5, 6, 7, 8, 9), cards(3, 4, 5, 6, 7))).toBeNull();
    expect(compareHands(cards(4, 4, 5, 5, 6, 6), cards(3, 3, 4, 4, 5, 5))).toBe(1);
  });

  it('orders bombs and the rocket correctly', () => {
    expect(compareHands(cards(3, 3, 3, 3), cards(15, 15))).toBe(1);
    expect(compareHands(cards(4, 4, 4, 4), cards(3, 3, 3, 3))).toBe(1);
    expect(compareHands(cards(16, 17), cards(15, 15, 15, 15))).toBe(1);
    expect(compareHands(cards(15, 15, 15, 15), cards(16, 17))).toBe(-1);
    expect(compareHands(cards(16, 17), cards(16, 17))).toBe(0);
    expect(canBeat(cards(16, 17), cards(16, 17))).toBe(false);
  });

  it('rejects invalid candidates and permits any valid lead', () => {
    expect(canBeat(cards(3, 4, 5), cards(3))).toBe(false);
    expect(canBeat(cards(3), null)).toBe(true);
    expect(canBeat(cards(3), [])).toBe(true);
    expect(canBeat(cards(3), cards(4))).toBe(false);
  });
});

describe('legal response generation', () => {
  const responseCases: Array<{ name: string; previous: number[]; candidate: number[]; type: HandType }> = [
    { name: 'single', previous: [3], candidate: [4], type: HandType.Single },
    { name: 'pair', previous: [3, 3], candidate: [4, 4], type: HandType.Pair },
    { name: 'trio', previous: [3, 3, 3], candidate: [4, 4, 4], type: HandType.Trio },
    { name: 'trio with single', previous: [3, 3, 3, 14], candidate: [4, 4, 4, 5], type: HandType.TrioWithSingle },
    { name: 'trio with pair', previous: [3, 3, 3, 5, 5], candidate: [4, 4, 4, 6, 6], type: HandType.TrioWithPair },
    { name: 'straight', previous: [3, 4, 5, 6, 7], candidate: [4, 5, 6, 7, 8], type: HandType.Straight },
    { name: 'pair straight', previous: [3, 3, 4, 4, 5, 5], candidate: [4, 4, 5, 5, 6, 6], type: HandType.PairStraight },
    { name: 'plane', previous: [3, 3, 3, 4, 4, 4], candidate: [4, 4, 4, 5, 5, 5], type: HandType.Plane },
    { name: 'plane with singles', previous: [3, 3, 3, 4, 4, 4, 7, 8], candidate: [4, 4, 4, 5, 5, 5, 6, 7], type: HandType.PlaneWithSingles },
    { name: 'plane with pairs', previous: [3, 3, 3, 4, 4, 4, 7, 7, 8, 8], candidate: [4, 4, 4, 5, 5, 5, 6, 6, 7, 7], type: HandType.PlaneWithPairs },
    { name: 'bomb', previous: [3, 3, 3, 3], candidate: [4, 4, 4, 4], type: HandType.Bomb },
    { name: 'four with two', previous: [3, 3, 3, 3, 4, 5], candidate: [4, 4, 4, 4, 5, 6], type: HandType.FourWithTwo },
    { name: 'four with two pairs', previous: [3, 3, 3, 3, 4, 4, 5, 5], candidate: [4, 4, 4, 4, 5, 5, 6, 6], type: HandType.FourWithTwoPairs },
    { name: 'rocket fallback', previous: [15, 15], candidate: [16, 17], type: HandType.Rocket }
  ];

  for (const testCase of responseCases) {
    it(`finds a legal ${testCase.name} response`, () => {
      const responses = listLegalResponses(cards(...testCase.candidate), cards(...testCase.previous));
      expect(responses.some((response) => parseHand(response)?.type === testCase.type)).toBe(true);
      assertAllResponsesAreLegal(responses, cards(...testCase.previous));
    });
  }

  it('orders same-type responses before bomb and rocket fallbacks', () => {
    const previous = cards(6, 6);
    const hand = cards(7, 7, 3, 3, 3, 3, 16, 17);
    const responses = listLegalResponses(hand, previous);

    expect(responses.map((response) => parseHand(response)?.type)).toEqual([
      HandType.Pair,
      HandType.Bomb,
      HandType.Rocket
    ]);
    assertAllResponsesAreLegal(responses, previous);
  });

  it('finds continuous responses and bomb/rocket fallbacks without guessing by card count', () => {
    const previous = cards(3, 4, 5, 6, 7);
    const responses = listLegalResponses(
      cards(4, 5, 6, 7, 8, 3, 3, 3, 3, 16, 17),
      previous
    );

    expect(responses.map((response) => parseHand(response)?.type)).toEqual([
      HandType.Straight,
      HandType.Bomb,
      HandType.Rocket
    ]);
    assertAllResponsesAreLegal(responses, previous);
  });

  it('returns no response when the hand cannot beat, or when the previous hand is a rocket', () => {
    expect(listLegalResponses(cards(3, 4, 5), cards(14, 14))).toEqual([]);
    expect(listLegalResponses(cards(15, 15, 15, 15, 16, 17), cards(16, 17))).toEqual([]);
    expect(listLegalResponses(cards(3, 3), cards(3, 4, 5))).toEqual([]);
  });

  it('returns unique logical responses and validates every result on a full hand', () => {
    const previous = cards(7, 7, 7, 8, 8);
    const hand = cards(3, 3, 3, 3, 4, 4, 5, 5, 6, 6, 8, 8, 8, 9, 9, 10, 11, 15, 16, 17);
    const responses = listLegalResponses(hand, previous);
    const signatures = responses.map(rankSignature);

    expect(new Set(signatures).size).toBe(signatures.length);
    assertAllResponsesAreLegal(responses, previous);
  });
});

describe('smallest legal response', () => {
  it('prefers the smallest same-type response, even when it splits a group', () => {
    const response = findSmallestLegalResponse(cards(4, 4, 10), cards(3));
    expect(response?.map((card) => card.rank)).toEqual([4]);
  });

  it('prefers same type, then the smallest bomb, then the rocket', () => {
    expect(parsedType(findSmallestLegalResponse(cards(7, 7, 3, 3, 3, 3, 16, 17), cards(6, 6))))
      .toBe(HandType.Pair);
    expect(parsedType(findSmallestLegalResponse(cards(3, 3, 3, 3, 16, 17), cards(15, 15))))
      .toBe(HandType.Bomb);
    expect(parsedType(findSmallestLegalResponse(cards(16, 17), cards(15, 15))))
      .toBe(HandType.Rocket);
  });

  it('leads with the smallest available single and returns null when there is no answer', () => {
    expect(findSmallestLegalResponse(cards(14, 3, 3), null)?.[0].rank).toBe(3);
    expect(findSmallestLegalResponse(cards(3, 4), cards(15, 15))).toBeNull();
  });
});

function cards(...ranks: number[]): CardInfo[] {
  const seen = new Map<number, number>();
  return ranks.map((rank) => {
    const occurrence = seen.get(rank) ?? 0;
    seen.set(rank, occurrence + 1);
    const suit = rank >= 16 ? 4 : occurrence % 4;
    return {
      suit,
      rank,
      color: rank === 17 || suit === 1 || suit === 3 ? 1 : 0
    };
  });
}

function assertAllResponsesAreLegal(responses: readonly CardInfo[][], previous: readonly CardInfo[]): void {
  expect(responses.length).toBeGreaterThan(0);
  for (const response of responses) {
    expect(parseHand(response)).not.toBeNull();
    expect(canBeat(response, previous)).toBe(true);
  }
}

function rankSignature(response: readonly CardInfo[]): string {
  return response.map((card) => card.rank).sort((a, b) => a - b).join(',');
}

function parsedType(response: CardInfo[] | null): ParsedHand['type'] | null {
  return response ? parseHand(response)?.type ?? null : null;
}
