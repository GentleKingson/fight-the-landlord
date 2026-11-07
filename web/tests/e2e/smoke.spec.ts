import { expect, test } from '@playwright/test';

test('desktop demo table renders commercial table zones', async ({ page }) => {
  await page.goto('/?demo=table');
  await expect(page.getByText('斗地主').first()).toBeVisible();
  await expect(page.getByRole('button', { name: '出牌' })).toBeVisible();
  await expect(page.getByLabel('手牌')).toBeVisible();
  await expect(page.getByLabel('底牌')).toBeVisible();
});

test('bidding demo hides bottom cards and only shows bidding actions', async ({ page }) => {
  await page.goto('/?demo=bidding');
  await expect(page.getByLabel('底牌')).toContainText('底牌未揭');
  await expect(page.getByRole('button', { name: '叫地主' })).toBeVisible();
  await expect(page.getByRole('button', { name: '不叫' })).toBeVisible();
  await expect(page.getByRole('button', { name: '出牌' })).toHaveCount(0);
});

test('mobile demo table keeps controls and drawers visible', async ({ page }) => {
  await page.goto('/?demo=table');
  await expect(page.getByRole('button', { name: '提示' })).toBeVisible();
  await expect(page.getByRole('button', { name: '聊天' })).toBeVisible();
});

test('hand supports keyboard activation without double toggling pointer clicks', async ({ page }) => {
  await page.goto('/?demo=table');
  const card = page.getByLabel('手牌').getByRole('button').first();
  await card.focus();
  await page.keyboard.press('Enter');
  await expect(card).toHaveAttribute('aria-pressed', 'true');
  await page.keyboard.press('Space');
  await expect(card).toHaveAttribute('aria-pressed', 'false');
  await card.click();
  await expect(card).toHaveAttribute('aria-pressed', 'true');
});

test('small lobby scrolls to room controls and navigation', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=lobby');
  await page.mouse.move(160, 280);
  await page.mouse.wheel(0, 2000);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
  await expect(page.getByRole('navigation', { name: '大厅导航' })).toBeInViewport();
  await page.getByRole('textbox', { name: '加入房间', exact: true }).fill('123456');
  await expect(page.getByRole('textbox', { name: '加入房间', exact: true })).toHaveValue('123456');
  await page.getByRole('region', { name: '可加入房间' }).getByRole('button', { name: '刷新' }).click();
  await page.getByRole('button', { name: '我的战绩', exact: true }).click();
  await expect(page.getByRole('button', { name: '我的战绩', exact: true })).toHaveClass('is-active');
});

test('pointer jitter within one card toggles exactly once', async ({ page }) => {
  await page.goto('/?demo=table');
  const card = page.getByLabel('手牌').getByRole('button').first();
  for (const selected of ['true', 'false']) {
    const box = (await card.boundingBox())!;
    await page.mouse.move(box.x + 8, box.y + 40);
    await page.mouse.down();
    await page.mouse.move(box.x + 9, box.y + 41);
    await page.mouse.up();
    await expect(card).toHaveAttribute('aria-pressed', selected);
  }
});

test('all dense portrait hand cards remain visible and selectable at 320px', async ({ page, isMobile }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=table');
  for (const ranks of [[14,14,14,14,13,13,13,13,12,12,12,12,11,11,11,10,10], Array.from({ length: 20 }, (_, i) => 14 - Math.floor(i / 4))]) {
    await page.evaluate(`import('/src/stores/appStore.ts').then(({ useAppStore }) => useAppStore.setState({ hand: ${JSON.stringify(ranks.map((rank, i) => ({ rank, suit: i % 4, color: i % 2 })))}, selectedCards: new Set(), currentTurn: 'p1' }))`);
    const cards = page.getByLabel('手牌').getByRole('button');
    await expect(cards).toHaveCount(ranks.length);
    for (let index = 0; index < ranks.length; index++) {
      const card = cards.nth(index);
      await card.scrollIntoViewIfNeeded();
      const box = (await card.boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(320);
      if (isMobile) await page.touchscreen.tap(box.x + 5, box.y + 30);
      else await page.mouse.click(box.x + 5, box.y + 30);
      await expect(card).toHaveAttribute('aria-pressed', 'true');
    }
  }
});

test('narrow landscape keeps all 20 cards reachable', async ({ page, isMobile }) => {
  await page.setViewportSize({ width: 667, height: 375 });
  await page.goto('/?demo=table');
  const ranks = [17,16,15,14,13,12,11,10,9,8,7,7,6,6,5,5,4,4,3,3];
  await page.evaluate(`import('/src/stores/appStore.ts').then(({ useAppStore }) => useAppStore.setState({ hand: ${JSON.stringify(ranks.map((rank, i) => ({ rank, suit: i % 4, color: i % 2 })))}, selectedCards: new Set(), currentTurn: 'p1' }))`);
  const cards = page.getByLabel('手牌').getByRole('button');
  await expect(cards).toHaveCount(20);
  for (let index = 0; index < ranks.length; index++) {
    const card = cards.nth(index);
    await card.scrollIntoViewIfNeeded();
    const box = (await card.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(667);
    if (isMobile) await page.touchscreen.tap(box.x + 5, box.y + 25);
    else await page.mouse.click(box.x + 5, box.y + 25);
    await expect(card).toHaveAttribute('aria-pressed', 'true');
  }
});
