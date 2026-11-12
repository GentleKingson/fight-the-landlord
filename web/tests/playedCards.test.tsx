import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { CardInfo } from '../src/protocol/types';
import { cardLabel, sortPlayedCards } from '../src/shared/cards/cardModel';
import { PlayedCards } from '../src/shared/cards/PlayedCards';

function cards(ranks: number[]): CardInfo[] {
  const counts = new Map<number, number>();
  return ranks.map(rank => {
    const suit = rank >= 16 ? 4 : counts.get(rank) ?? 0;
    counts.set(rank, suit + 1);
    return { rank, suit, color: rank === 17 || suit === 1 || suit === 3 ? 1 : 0 };
  });
}

afterEach(cleanup);

describe('played-card display order', () => {
  it.each([
    { type: '三带二', input: [14, 14, 3, 3, 3], output: [3, 3, 3, 14, 14] },
    { type: '三带一', input: [17, 6, 6, 6], output: [6, 6, 6, 17] },
    { type: '四带二', input: [16, 3, 17, 3, 3, 3], output: [3, 3, 3, 3, 17, 16] },
    { type: '四带二（对子）', input: [14, 3, 14, 3, 3, 3], output: [3, 3, 3, 3, 14, 14] },
    { type: '四带两对', input: [14, 14, 3, 3, 3, 3, 13, 13], output: [3, 3, 3, 3, 14, 14, 13, 13] },
    { type: '飞机带单', input: [3, 3, 3, 14, 4, 4, 4, 15], output: [4, 4, 4, 3, 3, 3, 15, 14] },
    { type: '飞机带单（对子）', input: [14, 14, 3, 3, 3, 4, 4, 4], output: [4, 4, 4, 3, 3, 3, 14, 14] },
    { type: '飞机带单（混合翅膀）', input: [12, 12, 3, 3, 3, 4, 4, 4, 5, 5, 5, 17], output: [5, 5, 5, 4, 4, 4, 3, 3, 3, 17, 12, 12] },
    { type: '飞机带对', input: [14, 14, 3, 3, 3, 13, 13, 4, 4, 4], output: [4, 4, 4, 3, 3, 3, 14, 14, 13, 13] },
    { type: '顺子', input: [3, 7, 5, 6, 4], output: [7, 6, 5, 4, 3] },
    { type: '连对', input: [3, 4, 5, 3, 5, 4], output: [5, 5, 4, 4, 3, 3] },
    { type: '飞机', input: [3, 4, 3, 4, 3, 4], output: [4, 4, 4, 3, 3, 3] },
    { type: '炸弹', input: [3, 3, 3, 3], output: [3, 3, 3, 3] },
    { type: '王炸', input: [16, 17], output: [17, 16] },
    { type: '单张', input: [3], output: [3] },
    { type: '对子', input: [3, 3], output: [3, 3] },
    { type: '三张', input: [3, 3, 3], output: [3, 3, 3] },
  ])('$type places the body first regardless of incoming order', ({ type, input, output }) => {
    const source = cards(input);
    const original = source.map(card => ({ ...card }));
    source.forEach(Object.freeze);
    Object.freeze(source);
    const sorted = sortPlayedCards(source);
    expect(sorted.map(card => card.rank)).toEqual(output);
    expect(sortPlayedCards([...source].reverse())).toEqual(sorted);
    expect(source).toEqual(original);
    expect(sorted).toHaveLength(source.length);
    expect(new Set(sorted)).toEqual(new Set(source));

    render(<PlayedCards cards={source} handType={type} />);
    expect(screen.getAllByRole('img').map(card => card.getAttribute('aria-label'))).toEqual(sorted.map(cardLabel));
  });

  it('handles an empty play', () => {
    expect(sortPlayedCards([])).toEqual([]);
    render(<PlayedCards cards={[]} />);
    expect(screen.queryAllByRole('button')).toHaveLength(0);
    expect(screen.getByText('等待出牌')).toBeTruthy();
  });
});
