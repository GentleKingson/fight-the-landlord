import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import type { CardInfo } from '../src/protocol/types';
import {
  canBeat,
  findSmallestLegalResponse,
  listLegalResponses,
  parseHand
} from '../src/game/rules';

interface RuleGoldenFixture {
  version: number;
  parse_cases: RuleGoldenParseCase[];
  comparison_cases: RuleGoldenComparisonCase[];
  response_cases: RuleGoldenResponseCase[];
}

interface RuleGoldenParseCase {
  name: string;
  ranks: number[];
  expected: {
    type: string;
    key_rank: number;
    length: number;
  } | null;
}

interface RuleGoldenComparisonCase {
  name: string;
  candidate: number[];
  previous: number[];
  can_beat: boolean;
}

interface RuleGoldenResponseCase {
  name: string;
  hand: number[];
  previous: number[];
  responses: number[][];
  smallest: number[] | null;
}

const fixturePath = path.resolve(
  process.cwd(),
  '../internal/game/rule/testdata/rule_golden.json'
);
const fixture = JSON.parse(readFileSync(fixturePath, 'utf8')) as RuleGoldenFixture;

describe('shared Go/TypeScript rule golden fixture', () => {
  it('uses the supported fixture version', () => {
    expect(fixture.version).toBe(1);
  });

  describe('parseHand', () => {
    for (const testCase of fixture.parse_cases) {
      it(testCase.name, () => {
        const parsed = parseHand(cards(testCase.ranks));
        if (testCase.expected === null) {
          expect(parsed).toBeNull();
          return;
        }

        expect(parsed).toMatchObject({
          type: testCase.expected.type,
          keyRank: testCase.expected.key_rank,
          length: testCase.expected.length
        });
      });
    }
  });

  describe('canBeat', () => {
    for (const testCase of fixture.comparison_cases) {
      it(testCase.name, () => {
        expect(canBeat(cards(testCase.candidate), cards(testCase.previous)))
          .toBe(testCase.can_beat);
      });
    }
  });

  describe('legal responses', () => {
    for (const testCase of fixture.response_cases) {
      it(testCase.name, () => {
        const hand = cards(testCase.hand);
        const previous = testCase.previous.length === 0 ? null : cards(testCase.previous);
        const responses = listLegalResponses(hand, previous).map(canonicalRanks);
        expect(responses).toEqual(testCase.responses);

        const smallest = findSmallestLegalResponse(hand, previous);
        expect(smallest === null ? null : canonicalRanks(smallest)).toEqual(testCase.smallest);
      });
    }
  });
});

function cards(ranks: readonly number[]): CardInfo[] {
  const occurrences = new Map<number, number>();
  return ranks.map((rank) => {
    const occurrence = occurrences.get(rank) ?? 0;
    occurrences.set(rank, occurrence + 1);
    const suit = rank >= 16 ? 4 : occurrence % 4;
    return { rank, suit, color: suit % 2 };
  });
}

function canonicalRanks(cardsToNormalize: readonly CardInfo[]): number[] {
  return cardsToNormalize.map(({ rank }) => rank).sort((left, right) => left - right);
}
