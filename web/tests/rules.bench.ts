import { bench, describe } from 'vitest';
import type { CardInfo } from '../src/protocol/types';
import { findSmallestLegalResponse, listLegalResponses } from '../src/game/rules';

const fullHand = cards(
  3, 3, 3, 3,
  4, 4,
  5, 5,
  6, 6,
  8, 8, 8,
  9, 9,
  10,
  11,
  15,
  16,
  17
);
const previousPlaneWithPairs = cards(4, 4, 4, 5, 5, 5, 6, 6, 7, 7);
const enumerationSamples: number[] = [];
const hintSamples: number[] = [];
const longTaskBudgetMs = 50;

describe('full-hand legal response enumeration', () => {
  bench('enumerates every legal response for a 20-card hand', () => {
    const startedAt = performance.now();
    listLegalResponses(fullHand, previousPlaneWithPairs);
    enumerationSamples.push(performance.now() - startedAt);
  }, budgetedBenchmark(enumerationSamples));

  bench('selects the Hint response for a 20-card hand', () => {
    const startedAt = performance.now();
    findSmallestLegalResponse(fullHand, previousPlaneWithPairs);
    hintSamples.push(performance.now() - startedAt);
  }, budgetedBenchmark(hintSamples));
});

function budgetedBenchmark(samples: number[]) {
  return {
    time: 500,
    warmupTime: 100,
    setup: (_task: unknown, mode: 'warmup' | 'run') => {
      if (mode === 'run') samples.length = 0;
    },
    teardown: (_task: unknown, mode: 'warmup' | 'run') => {
      if (mode !== 'run' || samples.length === 0) return;
      const ordered = [...samples].sort((left, right) => left - right);
      const p99 = ordered[Math.max(0, Math.ceil(ordered.length * 0.99) - 1)];
      if (p99 > longTaskBudgetMs) {
        throw new Error(`rule enumeration p99 ${p99.toFixed(2)}ms exceeds ${longTaskBudgetMs}ms long-task budget`);
      }
    }
  };
}

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
