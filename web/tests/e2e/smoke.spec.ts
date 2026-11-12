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

test('sound defaults on and both preferences survive reload and reopening the site', async ({ page, context }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=table');
  const toggle = page.getByRole('button', { name: '关闭音效' });
  await expect(toggle).toBeInViewport({ ratio: 1 });
  await expect(toggle).toHaveAttribute('aria-pressed', 'true');
  await toggle.click();
  await page.reload();
  await expect(page.getByRole('button', { name: '开启音效' })).toHaveAttribute('aria-pressed', 'false');
  const reopened = await context.newPage();
  await reopened.goto('/?demo=table');
  await expect(reopened.getByRole('button', { name: '开启音效' })).toHaveAttribute('aria-pressed', 'false');
  await reopened.getByRole('button', { name: '开启音效' }).click();
  await reopened.close();
  await page.reload();
  await expect(toggle).toHaveAttribute('aria-pressed', 'true');
  for (const name of ['记牌器', '聊天', '历史']) {
    await expect(page.getByRole('button', { name, exact: true })).toBeInViewport({ ratio: 1 });
  }
});

test('hand supports keyboard activation without double toggling pointer clicks', async ({ page }) => {
  await page.goto('/?demo=table');
  const card = page.getByLabel('手牌').getByRole('option').first();
  await card.focus();
  await page.keyboard.press('Enter');
  await expect(card).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Space');
  await expect(card).toHaveAttribute('aria-selected', 'false');
  await card.click();
  await expect(card).toHaveAttribute('aria-selected', 'true');
});

test('small lobby keeps room controls and navigation visible without page scrolling', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=lobby');
  for (const control of [
    page.getByRole('navigation', { name: '大厅导航' }),
    page.getByRole('button', { name: /快速开局/ }),
    page.getByRole('button', { name: /创建房间/ }),
    page.getByRole('button', { name: /人机练习/ }),
    page.getByRole('textbox', { name: '加入房间', exact: true }),
    page.getByRole('region', { name: '可加入房间' }).getByRole('button', { name: '刷新' }),
  ]) await expect(control).toBeInViewport({ ratio: 1 });
  expect(await page.evaluate(() => ({
    width: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth),
    height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight),
    x: window.scrollX,
    y: window.scrollY,
  }))).toEqual({ width: 320, height: 568, x: 0, y: 0 });
  await page.getByRole('textbox', { name: '加入房间', exact: true }).fill('123456');
  await expect(page.getByRole('textbox', { name: '加入房间', exact: true })).toHaveValue('123456');
  await page.getByRole('region', { name: '可加入房间' }).getByRole('button', { name: '刷新' }).click();
  await page.getByRole('button', { name: '我的战绩', exact: true }).click();
  await expect(page.getByRole('button', { name: '我的战绩', exact: true })).toHaveClass('is-active');
});

test('pointer jitter within one card toggles exactly once', async ({ page }) => {
  await page.goto('/?demo=table');
  const card = page.getByLabel('手牌').getByRole('option').first();
  for (const selected of ['true', 'false']) {
    const box = (await card.boundingBox())!;
    await page.mouse.move(box.x + 8, box.y + 40);
    await page.mouse.down();
    await page.mouse.move(box.x + 9, box.y + 41);
    await page.mouse.up();
    await expect(card).toHaveAttribute('aria-selected', selected);
  }
});

test('all dense portrait hand cards remain visible and selectable at 320px', async ({ page, isMobile }) => {
  await page.setViewportSize({ width: 320, height: 568 });
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
      if (isMobile) await page.touchscreen.tap(box.x + 5, box.y + 30);
      else await page.mouse.click(box.x + 5, box.y + 30);
      await expect(card).toHaveAttribute('aria-selected', 'true');
    }
  }
});

test('narrow landscape keeps all 20 cards reachable', async ({ page, isMobile }) => {
  await page.setViewportSize({ width: 667, height: 375 });
  await page.goto('/?demo=table');
  const ranks = [17,16,15,14,13,12,11,10,9,8,7,7,6,6,5,5,4,4,3,3];
  await page.evaluate(`import('/src/stores/appStore.ts').then(({ useAppStore }) => useAppStore.setState({ hand: ${JSON.stringify(ranks.map((rank, i) => ({ rank, suit: i % 4, color: i % 2 })))}, selectedCards: new Set(), currentTurn: 'p1' }))`);
  const cards = page.getByLabel('手牌').getByRole('option');
  await expect(cards).toHaveCount(20);
  for (let index = 0; index < ranks.length; index++) {
    const card = cards.nth(index);
    const box = (await card.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(667);
    expect(box.y).toBeGreaterThanOrEqual(0);
    expect(box.y + box.height).toBeLessThanOrEqual(375);
    if (isMobile) await page.touchscreen.tap(box.x + 5, box.y + 25);
    else await page.mouse.click(box.x + 5, box.y + 25);
    await expect(card).toHaveAttribute('aria-selected', 'true');
  }
});

test('table fits common viewports with collection notice, long plays and an overlay drawer', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop', 'Viewport coverage runs once in the desktop project');
  await page.route('**/api/config', (route) => route.fulfill({ json: { collect_training_data: true } }));
  const airplane = Array.from({ length: 18 }, (_, index) => ({
    rank: 3 + Math.floor(index / 3), suit: index % 3, color: index % 2,
  }));
  for (const viewport of [
    { width: 1366, height: 768 },
    { width: 390, height: 844 },
    { width: 320, height: 568 },
    { width: 667, height: 375 },
  ]) {
    await test.step(`${viewport.width} × ${viewport.height}`, async () => {
      await page.setViewportSize(viewport);
      await page.goto('/?demo=table');
      await expect(page.locator('.collection-notice')).toBeVisible();
      await page.evaluate(`import('/src/stores/appStore.ts').then(({ useAppStore }) => {
        for (const player of useAppStore.getState().players) {
          useAppStore.getState().handleMessage({ type: 'card_played', payload: {
            player_id: player.id, player_name: player.name, cards: ${JSON.stringify(airplane)}, cards_left: 2, hand_type: '飞机',
          } });
        }
      })`);
      const playedCards = page.locator('.felt-table .card--played');
      for (const side of ['left', 'me', 'right']) {
        await expect(page.locator(`.played-seat--${side} .card--played`)).toHaveCount(18);
      }
      // Compact layouts must retain the new status text without stealing the card rows' space.
      await expect(page.locator('.turn-banner__play-hint')).toHaveText('跟牌目标：松风');
      await expect(page.locator('.turn-banner__play-hint')).toBeVisible();
      await expect(page.getByText('待压过', { exact: true })).toBeVisible();
      expect(await page.evaluate(() => ({
        width: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth),
        height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight),
        x: window.scrollX,
        y: window.scrollY,
      }))).toEqual({ ...viewport, x: 0, y: 0 });

      const felt = (await page.locator('.felt-table').boundingBox())!;
      const boxes = await playedCards.evaluateAll(cards => cards.map(card => card.getBoundingClientRect().toJSON()));
      for (const box of boxes) {
        expect(box.x).toBeGreaterThanOrEqual(felt.x);
        expect(box.x + box.width).toBeLessThanOrEqual(felt.x + felt.width + 1);
        expect(box.y).toBeGreaterThanOrEqual(felt.y);
        expect(box.y + box.height).toBeLessThanOrEqual(felt.y + felt.height + 1);
      }
      const zones = await page.locator('.opponent-row, .felt-table .played-cards, .turn-banner').evaluateAll((elements) =>
        elements.map((element) => {
          const { top, right, bottom, left } = element.getBoundingClientRect();
          return { name: element.className, top, right, bottom, left };
        }),
      );
      expect(zones).toHaveLength(5);
      for (let first = 0; first < zones.length; first++) {
        for (let second = first + 1; second < zones.length; second++) {
          const a = zones[first];
          const b = zones[second];
          expect(a.bottom <= b.top || b.bottom <= a.top || a.right <= b.left || b.right <= a.left, `Overlapping zones: ${JSON.stringify({ a, b })}`).toBe(true);
        }
      }

      const tableZones = page.locator('.table-topbar, .table-arena, .action-bar, .hand-zone');
      const before = await tableZones.evaluateAll((elements) => elements.map((element) => element.getBoundingClientRect().toJSON()));
      await expect(page.locator('.utility-drawer')).toHaveCount(0);
      await page.getByRole('button', { name: '聊天', exact: true }).click();
      await expect(page.locator('.utility-drawer')).toBeVisible();
      expect(await tableZones.evaluateAll((elements) => elements.map((element) => element.getBoundingClientRect().toJSON()))).toEqual(before);
      await page.getByRole('button', { name: '关闭', exact: true }).click();
      await expect(page.locator('.utility-drawer')).toHaveCount(0);
    });
  }
});
