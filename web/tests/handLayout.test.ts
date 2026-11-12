import { describe, expect, it } from 'vitest';
import type { CardInfo } from '../src/protocol/types';
import { buildHandLayout } from '../src/shared/cards/Hand';

function c(suit: number, rank: number): CardInfo {
  return { suit, rank, color: suit === 1 || suit === 3 ? 1 : 0 };
}

describe('hand layout model', () => {
  it('keeps logical order while grouping equal ranks', () => {
    const layout = buildHandLayout([c(4, 17), c(4, 16), c(0, 14), c(1, 14), c(2, 10)]);

    expect(layout.map((item) => item.index)).toEqual([0, 1, 2, 3, 4]);
    expect(layout[2].groupIndex).toBe(layout[3].groupIndex);
    expect(layout[2].groupSize).toBe(2);
  });

  it('provides independent row coordinates for portrait two-row hands', () => {
    const layout = buildHandLayout([
      c(4, 17), c(4, 16), c(0, 15), c(1, 14), c(2, 13), c(3, 12), c(0, 11), c(1, 10)
    ]);

    expect(new Set(layout.map((item) => item.row))).toEqual(new Set([0, 1]));
    expect(layout.every((item) => Number.isFinite(item.singleX) && Number.isFinite(item.compactX) && Number.isFinite(item.rowX))).toBe(true);
  });
});

it('fits dense rank groups and 20-card hands within narrow portrait containers', () => {
  for (const ranks of [[14,14,14,14,13,13,13,13,12,12,12,12,11,11,11,10,10], Array.from({ length: 20 }, (_, i) => 14 - Math.floor(i / 4))]) {
    const layout = buildHandLayout(ranks.map((rank, i) => c(i % 4, rank)), 280);
    for (const row of [0, 1]) {
      const positions = layout.filter(item => item.row === row).map(item => item.rowX);
      expect(Math.max(...positions) - Math.min(...positions) + 58).toBeLessThanOrEqual(280);
      expect(new Set(positions).size).toBe(positions.length);
    }
  }
});

it('fits all 20 cards and 15 rank groups within a narrow single-row hand', () => {
  const ranks = [17,16,15,14,13,12,11,10,9,8,7,7,6,6,5,5,4,4,3,3];
  const layout = buildHandLayout(ranks.map((rank, i) => c(i % 4, rank)), 414);
  for (const [coordinates, cardWidth] of [[layout.map(item => item.compactX), 58], [layout.map(item => item.singleX), 72]] as const) {
    expect(Math.max(...coordinates) - Math.min(...coordinates) + cardWidth).toBeLessThanOrEqual(414);
    expect(new Set(coordinates).size).toBe(20);
  }
});


it('limits row imbalance without losing rank grouping across a split', () => {
  const layout = buildHandLayout(Array.from({ length: 20 }, (_, i) => c(i % 4, 14 - Math.floor(i / 4))), 280);
  expect(layout.filter(item => item.row === 0)).toHaveLength(10);
  expect(layout.filter(item => item.row === 1)).toHaveLength(10);
  expect(layout[9].groupIndex).toBe(layout[10].groupIndex);
  for (const row of [0, 1]) {
    const positions = layout.filter(item => item.row === row).map(item => item.rowX);
    expect(Math.min(...positions.slice(1).map((x, i) => x - positions[i]))).toBeGreaterThanOrEqual(23);
  }
});
