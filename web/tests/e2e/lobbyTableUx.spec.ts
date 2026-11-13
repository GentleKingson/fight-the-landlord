import { expect, test } from '@playwright/test';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: { collect_training_data: true } }));
});

test('narrow lobby notices leave the header, navigation and quick start usable', async ({ page }) => {
  await page.goto('/?demo=lobby');
  const notice = '此账号已在另一个页面继续游戏，本页面已停止重连';
  await page.evaluate(async text => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ path);
    useAppStore.getState().setError(text);
  }, notice);
  for (const viewport of [{ width: 320, height: 568 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    const toast = (await page.getByRole('alert').boundingBox())!;
    const header = (await page.locator('.lobby-header').boundingBox())!;
    expect(toast.y + toast.height).toBeLessThanOrEqual(header.y);
    await expect(page.locator('.bottom-nav')).toBeInViewport({ ratio: 1 });
    await page.locator('.lobby-header-tools button').first().click();
    if (viewport.width === 320) await expect(page.locator('.quick-card__art')).toBeHidden();
    else await expect(page.locator('.quick-card__art')).toBeVisible();
    await expect(page.getByRole('button', { name: '快速开局' })).toBeInViewport({ ratio: 1 });
  }
});

test('all action feedback fits after a failed play and a reselect', async ({ page }) => {
  for (const viewport of [{ width: 320, height: 480 }, { width: 320, height: 568 }, { width: 390, height: 844 }, { width: 667, height: 375 }]) {
    await page.setViewportSize(viewport);
    await page.goto('/?demo=table');
    const hand = page.locator('.hand');
    const before = await hand.boundingBox();
    for (const name of ['提示', '出牌', '重选']) await page.getByRole('button', { name, exact: true }).click();
    const summary = page.locator('#play-action-summary');
    await expect(summary).toContainText('操作未发送');
    await expect(summary).toBeInViewport({ ratio: 1 });
    expect(await summary.evaluate(el => el.scrollHeight <= el.clientHeight && el.scrollWidth <= el.clientWidth)).toBe(true);
    expect(await hand.boundingBox()).toEqual(before);
    await expect(hand).toBeInViewport({ ratio: 1 });
    if (viewport.width === 320) {
      const firstCards = page.getByLabel('手牌').getByRole('option');
      for (const i of [0, 2]) await firstCards.nth(i).click({ position: { x: 5, y: 10 } });
      await expect(summary).toContainText('牌型错误');
      const lines = await summary.evaluate(el => ({
        height: el.clientHeight, content: el.scrollHeight, line: parseFloat(getComputedStyle(el).lineHeight)
      }));
      expect(lines.height).toBeGreaterThan(lines.line);
      expect(lines.content).toBe(lines.height);
      await expect(hand).toBeInViewport({ ratio: 1 });
    }
  }
});

test('short landscape shows the winning cards on entry and in review', async ({ page }) => {
  await page.setViewportSize({ width: 667, height: 375 });
  await page.goto('/?demo=reveal');
  const winner = page.getByRole('region', { name: '获胜最后一手' });
  for (const button of ['查看结算', '返回结算']) {
    await expect(winner.locator('.card').last()).toBeInViewport({ ratio: 1 });
    const last = page.getByRole('region', { name: '我剩余手牌' }).locator('.card').last();
    await last.scrollIntoViewIfNeeded();
    await expect(last).toBeInViewport({ ratio: 1 });
    await page.getByRole('button', { name: button }).click();
    if (button === '查看结算') await page.getByRole('button', { name: '查看本局牌面' }).click();
  }
});

for (const scope of ['lobby', 'room'] as const) {
  test(`${scope} chat follows the tail without taking readers away from older messages`, async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 568 });
    await page.goto(scope === 'room' ? '/?demo=table' : '/?demo=lobby');
    const push = (count: number, own = false) => page.evaluate(async ({ count, scope, own }) => {
      const path = '/src/stores/appStore.ts';
      const { useAppStore } = await import(/* @vite-ignore */ path);
      for (let i = 0; i < count; i++) useAppStore.getState().handleMessage({ type: 'chat',
        payload: { scope, sender_id: own ? 'p1' : 'peer', content: '第一行消息，第二行还有一些内容。'.repeat(3) + i } });
    }, { count, scope, own });
    await push(30);
    await page.getByRole('button', { name: '聊天', exact: true }).click();
    const feed = page.getByRole('log');
    const gap = () => feed.evaluate(el => el.scrollHeight - el.clientHeight - el.scrollTop);
    await expect.poll(gap).toBeLessThanOrEqual(1);
    await push(1);
    await expect.poll(gap).toBeLessThanOrEqual(1);
    const newer = page.getByRole('button', { name: '有新消息，查看最新' });
    for (const own of [false, true]) {
      const anchor = feed.locator('p').nth(5);
      await anchor.evaluate(el => el.scrollIntoView({ block: 'start' }));
      await expect.poll(gap).toBeGreaterThan(100);
      const top = (await anchor.boundingBox())!.y;
      const anchorText = await anchor.textContent();
      await push(1, own);
      if (!own) {
        await expect(newer).toBeVisible();
        const rect = await feed.getByText(anchorText!, { exact: true }).boundingBox();
        expect(Math.abs(rect!.y - top)).toBeLessThan(2);
        await newer.focus();
        await newer.press('Enter');
      }
      await expect.poll(gap).toBeLessThanOrEqual(1);
      await expect(newer).toHaveCount(0);
    }
  });
}
