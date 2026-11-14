import { expect, test } from '@playwright/test';

test.use({ viewport: { width: 320, height: 568 }, isMobile: true, hasTouch: true });

test('all dense portrait hand cards remain visible and selectable at 320px', async ({ page }) => {
  await page.goto('/?demo=table');
  for (const ranks of [[14,14,14,14,13,13,13,13,12,12,12,12,11,11,11,10,10], Array.from({ length: 20 }, (_, i) => 14 - Math.floor(i / 4))]) {
    await page.evaluate(`import('/src/stores/appStore.ts').then(({ useAppStore }) => useAppStore.setState({ hand: ${JSON.stringify(ranks.map((rank, i) => ({ rank, suit: i % 4, color: i % 2 })))}, selectedCards: new Set(), currentTurn: 'p1' }))`);
    const cards = page.getByLabel('手牌').getByRole('option');
    await expect(cards).toHaveCount(ranks.length);
    for (let index = 0; index < ranks.length; index++) {
      const card = cards.nth(index);
      const box = (await card.boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(320);
      expect(box.y).toBeGreaterThanOrEqual(0);
      expect(box.y + box.height).toBeLessThanOrEqual(568);
      await page.touchscreen.tap(box.x + 5, box.y + 30);
      await expect(card).toHaveAttribute('aria-selected', 'true');
    }
    expect(await cards.evaluateAll(elements => elements.every(card => {
      const rank = card.querySelector('.card__index--top b');
      if (!rank) return true;
      const box = rank.getBoundingClientRect();
      return document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2)?.closest('.card') === card;
    }))).toBe(true);
  }
});
