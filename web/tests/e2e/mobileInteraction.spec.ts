import { expect, test } from '@playwright/test';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
});

test('touch range crosses rows, retracts and rolls back on cancellation', async ({ page, context }, info) => {
  test.skip(info.project.name !== 'mobile-portrait', 'One touch device covers the two-row gesture');
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=table');
  await page.evaluate(async () => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ path);
    useAppStore.setState({ hand: Array.from({ length: 20 }, (_, i) => ({ rank: 14 - Math.floor(i / 4), suit: i % 4, color: i % 2 })), selectedCards: new Set(), currentTurn: 'p1' });
  });
  const cards = page.locator('.hand .card');
  await expect(cards).toHaveCount(20);
  const points = await cards.evaluateAll(elements => elements.map(element => {
    const box = element.getBoundingClientRect();
    return { x: box.left + 10, y: box.top + 30 };
  }));
  const cdp = await context.newCDPSession(page);
  const touch = async (type: 'touchStart' | 'touchMove' | 'touchEnd' | 'touchCancel', index = 0) => {
    await cdp.send('Input.dispatchTouchEvent', { type, touchPoints: type === 'touchEnd' || type === 'touchCancel' ? [] : [{ ...points[index], id: 1 }] });
  };
  await touch('touchStart', 8);
  await touch('touchMove', 12);
  await expect(page.locator('.hand .card[aria-selected="true"]')).toHaveCount(5);
  await touch('touchMove', 10);
  await expect(page.locator('.hand .card[aria-selected="true"]')).toHaveCount(3);
  await touch('touchEnd');
  await expect(cards.nth(10)).toHaveAttribute('aria-selected', 'true');
  await touch('touchStart', 2);
  await touch('touchMove', 6);
  await touch('touchCancel');
  await expect(page.locator('.hand .card[aria-selected="true"]')).toHaveCount(3);
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
});

test('small viewports retain touch targets and restore after orientation changes', async ({ page }, info) => {
  test.skip(info.project.name !== 'mobile-portrait', 'Viewport sweep runs once');
  await page.goto('/?demo=table');
  for (const viewport of [{ width: 320, height: 480 }, { width: 568, height: 320 }, { width: 390, height: 664 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    for (const locator of [page.locator('.hand'), page.locator('.played-seat--target'), page.locator('.opponent-row'), page.locator('.action-bar')]) {
      await expect(locator).toBeInViewport({ ratio: 1 });
    }
    for (const button of await page.locator('.table-tools button, .action-bar button').all()) {
      const box = (await button.boundingBox())!;
      expect(box.width).toBeGreaterThanOrEqual(44);
      expect(box.height).toBeGreaterThanOrEqual(44);
    }
    expect(await page.evaluate(() => ({ width: document.documentElement.scrollWidth, height: document.documentElement.scrollHeight, y: window.scrollY }))).toEqual({ ...viewport, y: 0 });
  }
  await page.getByRole('button', { name: '聊天', exact: true }).click();
  await page.getByPlaceholder('房间聊天').fill('测试输入');
  // Emulate the visual-viewport contract; real OS keyboard/browser chrome still needs device QA.
  await page.evaluate(() => {
    Object.defineProperty(window.visualViewport!, 'height', { configurable: true, value: 400 });
    window.visualViewport!.dispatchEvent(new Event('resize'));
  });
  const drawer = page.locator('.utility-drawer');
  await expect.poll(async () => { const box = (await drawer.boundingBox())!; return box.y + box.height; }).toBeLessThanOrEqual(400);
  await expect(page.getByRole('button', { name: '关闭', exact: true })).toBeInViewport({ ratio: 1 });
  await page.evaluate(() => {
    delete (window.visualViewport as unknown as { height?: number }).height;
    window.visualViewport!.dispatchEvent(new Event('resize'));
  });
  await page.getByRole('button', { name: '关闭', exact: true }).click();
  await expect(page.locator('.hand')).toBeInViewport({ ratio: 1 });
});

test('utility content scrolls internally while the table stays fixed', async ({ page }, info) => {
  test.skip(info.project.name !== 'mobile-portrait', 'One small touch viewport covers drawer containment');
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=table');
  await page.evaluate(async () => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore, useChatStore } = await import(/* @vite-ignore */ path);
    useChatStore.setState({ messages: Array.from({ length: 24 }, () => ({ scope: 'room', sender_name: '玩家', content: '较长的聊天消息，用来验证内容区域独立滚动。'.repeat(3) })) });
    useAppStore.setState({ recentActions: Array.from({ length: 40 }, () => ({ type: 'pass', player_name: '玩家' })) });
  });
  for (const [drawer, selector] of [['chat', '.chat-feed'], ['history', '.history-list'], ['rules', '.rules-panel']]) {
    await page.evaluate(async drawer => {
      const path = '/src/stores/appStore.ts';
      const { useAppStore } = await import(/* @vite-ignore */ path);
      useAppStore.getState().setDrawer(drawer);
    }, drawer);
    const content = page.locator(`.utility-drawer ${selector}`);
    await expect(content).toBeVisible();
    // The current rules are short; constrain their panel to exercise the same overflow path.
    if (drawer === 'rules') await content.evaluate(element => { (element as HTMLElement).style.maxHeight = '30px'; });
    expect(await content.evaluate(element => {
      element.scrollTop = element.scrollHeight;
      return element.scrollTop;
    })).toBeGreaterThan(0);
    const close = page.getByRole('button', { name: '关闭', exact: true });
    const box = (await close.boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(44);
    expect(box.height).toBeGreaterThanOrEqual(44);
    expect(await page.evaluate(() => window.scrollY)).toBe(0);
    await close.click();
  }
});
