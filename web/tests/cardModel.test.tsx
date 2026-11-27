import { expect, it } from 'vitest';
import { normalizeCard } from '../src/shared/cards/cardModel';

it('recognizes jokers by rank even when suit data is inconsistent', () => {
  expect(normalizeCard({ suit: 0, rank: 17, color: 1 })).toMatchObject({
    label: '大王',
    rankLabel: 'JOKER',
    color: 'red',
    isJoker: true
  });
  expect(normalizeCard({ suit: 4, rank: 16, color: 0 })).toMatchObject({
    label: '小王',
    rankLabel: 'JOKER',
    color: 'black',
    isJoker: true
  });
});
