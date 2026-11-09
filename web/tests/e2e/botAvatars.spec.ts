import { expect, test, type Page } from '@playwright/test';

const portraits = '.player-avatar img';
const sources = (page: Page) => page.locator(portraits).evaluateAll(images => images.map(image => (image as HTMLImageElement).src));

async function expectLayout(page: Page) {
  for (const avatar of await page.locator('.player-avatar').all()) {
    await expect(avatar).toBeInViewport({ ratio: 1 });
    const box = (await avatar.boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(26);
    expect(box.width).toBe(box.height);
  }
  const geometry = await page.locator('.opponent-row').evaluate(row => {
    const bounds = (selector: string) => row.querySelector(selector)!.getBoundingClientRect();
    const left = bounds('.seat-panel--left .player-badge');
    const right = bounds('.seat-panel--right .player-badge');
    const bottom = bounds('.bottom-cards');
    return { left: left.right, bottomLeft: bottom.left, bottomRight: bottom.right, right: right.left,
      labels: [...row.querySelectorAll('.player-badge > div > span')].map(label => ({ text: label.textContent, scroll: label.scrollWidth, available: label.clientWidth })),
      width: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth),
      height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight) };
  });
  expect(geometry.left).toBeLessThanOrEqual(geometry.bottomLeft);
  expect(geometry.right).toBeGreaterThanOrEqual(geometry.bottomRight);
  expect(geometry.labels.filter(label => label.scroll > label.available), `Role/card-count clipping at ${JSON.stringify(page.viewportSize())}`).toEqual([]);
  expect([geometry.width, geometry.height]).toEqual([page.viewportSize()!.width, page.viewportSize()!.height]);
}

test.beforeEach(async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
});

test('practice loads only two distinct small portraits and preserves them on reload and interaction', async ({ page }) => {
  const imageRequests: string[] = [];
  page.on('request', request => {
    if (request.resourceType() === 'image' && request.url().includes('/bot-avatars/')) imageRequests.push(request.url());
  });
  await page.goto('/?demo=practice');
  await expect(page.locator(portraits)).toHaveCount(2);
  const initial = await sources(page);
  expect(new Set(initial).size).toBe(2);
  await expect.poll(() => imageRequests.length).toBe(2);
  for (const image of await page.locator(portraits).all()) {
    expect(await image.evaluate(async element => {
      const image = element as HTMLImageElement;
      await image.decode();
      const response = await fetch(image.src);
      return { width: image.naturalWidth, height: image.naturalHeight, bytes: (await response.arrayBuffer()).byteLength };
    })).toMatchObject({ width: 192, height: 192 });
    expect(await image.evaluate(async element => (await (await fetch((element as HTMLImageElement).src)).arrayBuffer()).byteLength)).toBeLessThan(12 * 1024);
  }
  await expectLayout(page);
  await page.screenshot({ path: test.info().outputPath('practice-table.png') });
  const card = page.getByLabel('手牌').getByRole('button').first();
  await card.click();
  await expect(card).toHaveAttribute('aria-pressed', 'true');
  for (let i = 0; i < 2; i++) {
    await page.getByRole('button', { name: '聊天', exact: true }).click();
    await page.getByRole('button', { name: '关闭', exact: true }).click();
    expect(await sources(page)).toEqual(initial);
  }
  await page.reload();
  await expect(page.locator(portraits)).toHaveCount(2);
  expect(await sources(page)).toEqual(initial);
  await expectLayout(page);
});

test('avatars, role and card count fit narrow portrait and landscape while rotating', async ({ page }) => {
  await page.goto('/?demo=practice');
  await expect(page.locator(portraits)).toHaveCount(2);
  const initial = await sources(page);
  for (const viewport of [
    { width: 320, height: 568 }, { width: 320, height: 844 }, { width: 375, height: 667 }, { width: 390, height: 844 },
    { width: 844, height: 390 }, { width: 568, height: 320 }, { width: 1440, height: 900 },
    { width: 390, height: 844 }
  ]) {
    await page.setViewportSize(viewport);
    await expectLayout(page);
    expect(await sources(page)).toEqual(initial);
    await expect(page.getByRole('button', { name: '出牌', exact: true })).toBeInViewport({ ratio: 1 });
    await page.screenshot({ path: test.info().outputPath(`practice-${viewport.width}x${viewport.height}.png`) });
  }
  await page.evaluate(async () => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ path);
    const state = useAppStore.getState();
    useAppStore.setState({ phase: 'bidding', bottomCardsRevealed: false,
      players: state.players.map((player: { name: string; id: string }) => ({ ...player, name: '🤖很长的机器人昵称', is_landlord: false })),
      seatActions: { p2: { type: 'bid', player_id: 'p2', label: '不叫' } } });
  });
  await expect(page.getByRole('button', { name: '叫地主', exact: true })).toBeInViewport({ ratio: 1 });
  await expectLayout(page);
  expect(await sources(page)).toEqual(initial);
  await page.screenshot({ path: test.info().outputPath('bidding-long-names.png') });
});

test('failed images retain initials and human opponents never request bot portraits', async ({ page }) => {
  await page.route('**/bot-avatars/*.webp', route => route.abort());
  await page.goto('/?demo=practice');
  await expect(page.locator(portraits)).toHaveCount(0);
  await expect(page.locator('.seat-panel--left .player-avatar')).toHaveText('松');
  await expect(page.locator('.seat-panel--right .player-avatar')).toHaveText('山');
  await expectLayout(page);
  await page.goto('/?demo=table');
  await expect(page.locator(portraits)).toHaveCount(0);
  await expectLayout(page);
  await page.setViewportSize({ width: 320, height: 844 });
  await page.evaluate(async () => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ path);
    useAppStore.setState({ players: useAppStore.getState().players.map((player: { id: string }) => ({ ...player, online: player.id !== 'p2' })) });
  });
  await expect(page.getByText('离线', { exact: true })).toBeVisible();
  await expectLayout(page);
});
