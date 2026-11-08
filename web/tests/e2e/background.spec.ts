import { expect, test, type Page } from '@playwright/test';

async function expectPageBackground(page: Page) {
  const width = page.viewportSize()!.width;
  const size = width > 960 ? 1600 : 960;
  await expect(page.locator('.app-shell')).toHaveCSS('background-image', new RegExp(`emerald-table-${size}`));
  await expect(page.locator('.app-shell')).toHaveCSS('background-color', 'rgb(0, 59, 41)');
  await expect(page.locator('.app-shell')).toHaveCSS('background-size', 'cover');
  await expect(page.locator('.app-shell')).toHaveCSS('background-position', '50% 50%');
  expect(await page.evaluate(() => ({
    width: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth),
    height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight),
  }))).toEqual(page.viewportSize());
}

test('page background loads one appropriately sized WebP and survives navigation and reload', async ({ page }) => {
  const imageRequests: string[] = [];
  page.on('request', request => {
    if (request.url().includes('emerald-table-')) imageRequests.push(request.url());
  });
  const responsePromise = page.waitForResponse(response => response.url().includes('emerald-table-'));
  await page.goto('/?demo=lobby');
  const response = await responsePromise;
  const size = page.viewportSize()!.width > 960 ? 1600 : 960;
  expect(response.ok()).toBe(true);
  expect(response.url()).toContain(`emerald-table-${size}`);
  expect(response.headers()['content-type']).toContain('image/webp');
  expect((await response.body()).byteLength).toBeLessThan(size === 1600 ? 24 * 1024 : 10 * 1024);
  expect(imageRequests).toHaveLength(1);
  expect(await page.evaluate(async () => {
    const css = getComputedStyle(document.querySelector('.app-shell')!);
    const image = new Image();
    image.src = css.backgroundImage.slice(5, -2);
    await image.decode();
    return [image.naturalWidth, image.naturalHeight];
  })).toEqual(size === 1600 ? [1600, 900] : [960, 540]);

  await expectPageBackground(page);
  await page.getByRole('button', { name: '我的战绩', exact: true }).click();
  await expect(page.getByRole('heading', { name: '我的战绩' })).toBeVisible();
  await expectPageBackground(page);
  await page.getByRole('button', { name: '大厅', exact: true }).click();
  await page.reload();
  await expect(page.getByRole('button', { name: /快速开局/ })).toBeInViewport({ ratio: 1 });
  await expectPageBackground(page);
});

test('the new outer background preserves the original felt and interactive table layers', async ({ page }) => {
  await page.goto('/?demo=table');
  await expectPageBackground(page);
  await expect(page.locator('.table-screen')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
  await expect(page.locator('.table-screen')).toHaveCSS('background-image', 'none');
  expect(await page.locator('.table-screen').evaluate(element => getComputedStyle(element, '::before').content)).toBe('none');
  const feltBackground = await page.locator('.table-arena').evaluate(element => getComputedStyle(element).backgroundImage);
  expect(feltBackground).toContain('radial-gradient');
  expect(feltBackground).not.toContain('emerald-table');
  const bounds = await page.locator('.table-arena').boundingBox();
  for (let cycle = 0; cycle < 2; cycle++) {
    await page.getByRole('button', { name: '聊天', exact: true }).click();
    await expect(page.locator('.utility-drawer')).toBeVisible();
    await page.getByRole('button', { name: '关闭', exact: true }).click();
    await expect(page.locator('.utility-drawer')).toHaveCount(0);
    expect(await page.locator('.table-arena').boundingBox()).toEqual(bounds);
  }
  await expect(page.getByRole('button', { name: '出牌', exact: true })).toBeInViewport({ ratio: 1 });
  const card = page.getByLabel('手牌').getByRole('button').first();
  await card.click();
  await expect(card).toHaveAttribute('aria-pressed', 'true');
  await page.getByRole('button', { name: '重选', exact: true }).click();
  await expect(card).toHaveAttribute('aria-pressed', 'false');
});

test('failed background images retain a readable fallback without affecting layout', async ({ page }) => {
  await page.route('**/emerald-table-*', route => route.abort());
  for (const mode of ['lobby', 'bidding', 'table', 'result']) {
    await page.goto(`/?demo=${mode}`);
    await expectPageBackground(page);
    if (mode === 'lobby') await expect(page.getByRole('button', { name: /快速开局/ })).toBeInViewport({ ratio: 1 });
    if (mode === 'bidding') await expect(page.getByRole('button', { name: '叫地主', exact: true })).toBeInViewport({ ratio: 1 });
    if (mode === 'table') await expect(page.getByRole('button', { name: '出牌', exact: true })).toBeInViewport({ ratio: 1 });
    if (mode === 'result') await expect(page.getByRole('button', { name: '返回大厅', exact: true })).toBeInViewport({ ratio: 1 });
  }
});

test('background selection follows the viewport through portrait and landscape resizing', async ({ page }) => {
  for (const viewport of [
    { width: 320, height: 568 },
    { width: 390, height: 844 },
    { width: 844, height: 390 },
    { width: 1440, height: 900 },
  ]) {
    await page.setViewportSize(viewport);
    await page.goto('/?demo=lobby');
    await expectPageBackground(page);
    await expect(page.getByRole('navigation', { name: '大厅导航' })).toBeInViewport({ ratio: 1 });
  }
});
