import { expect, test } from '@playwright/test';

const cards = [
  { suit: 0, rank: 8, color: 0 }, { suit: 1, rank: 8, color: 1 },
  { suit: 2, rank: 7, color: 0 }, { suit: 3, rank: 7, color: 1 },
  { suit: 4, rank: 16, color: 0 }, { suit: 4, rank: 17, color: 1 }
];

test('played and bottom cards retain the same suit colors as the hand', async ({ page }) => {
  await page.goto('/?demo=table');
  await page.evaluate(`import('/src/stores/appStore.ts').then(({ useAppStore }) => {
    const cards = ${JSON.stringify(cards)};
    useAppStore.setState({ hand: cards, bottomCards: cards, seatActions: {
      p1: { type: 'play', cards, hand_type: '出牌' },
      p2: { type: 'play', cards, hand_type: '出牌' }
    } });
  })`);
  for (const zone of ['.hand', '.bottom-cards', '.played-action--self', '.seat-panel--left']) {
    const faces = await page.locator(`${zone} .card`).evaluateAll(elements => elements.map(card => {
      const expected = getComputedStyle(card).color;
      return { expected, colors: [...card.querySelectorAll('span, b, i')].map(part => getComputedStyle(part).color) };
    }));
    expect(faces).toHaveLength(cards.length);
    for (let index = 0; index < cards.length; index++) {
      expect(faces[index].expected).toBe(cards[index].color ? 'rgb(214, 67, 59)' : 'rgb(28, 38, 48)');
      expect(faces[index].colors.every(color => color === faces[index].expected)).toBe(true);
    }
  }
});

for (const count of [1, 6, 20]) {
  test(`self play with ${count} cards stays clear of hand and controls`, async ({ page }) => {
    const playedCards = Array.from({ length: count }, (_, index) => ({
      rank: 3 + Math.floor(index / 2), suit: index % 2, color: index % 2
    }));
    await page.goto('/?demo=table');
    await page.evaluate(`import('/src/stores/appStore.ts').then(({ useAppStore }) => useAppStore.setState({
      seatActions: { p1: { type: 'play', cards: ${JSON.stringify(playedCards)}, hand_type: '出牌' } }
    }))`);
    const played = page.getByLabel('我的出牌');
    await expect(played).toBeVisible();
    const bubble = (await played.locator('.played-action').boundingBox())!;
    const arena = (await page.getByLabel('斗地主牌桌').boundingBox())!;
    expect(bubble.x).toBeGreaterThanOrEqual(arena.x);
    expect(bubble.y).toBeGreaterThanOrEqual(arena.y);
    expect(bubble.x + bubble.width).toBeLessThanOrEqual(arena.x + arena.width);
    expect(bubble.y + bubble.height).toBeLessThanOrEqual(arena.y + arena.height);
    for (const card of await played.locator('.card').all()) {
      const box = (await card.boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(bubble.x);
      expect(box.x + box.width).toBeLessThanOrEqual(bubble.x + bubble.width);
    }
    for (const selector of ['.hand', '.action-bar', '.bottom-cards', '.turn-banner']) {
      const other = (await page.locator(selector).boundingBox())!;
      const overlap = bubble.x < other.x + other.width && bubble.x + bubble.width > other.x
        && bubble.y < other.y + other.height && bubble.y + bubble.height > other.y;
      expect(overlap, `self play overlaps ${selector}`).toBe(false);
    }
  });
}
