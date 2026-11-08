import { expect, test, type Page } from '@playwright/test';

function expectedAsset(page: Page) {
  const { width, height } = page.viewportSize()!;
  if (height >= width) return { name: 'portrait-900', dimensions: [900, 1600], budget: 20 * 1024 };
  if (width > 960) return { name: '1600', dimensions: [1600, 900], budget: 24 * 1024 };
  return { name: '960', dimensions: [960, 540], budget: 10 * 1024 };
}

async function expectPageBackground(page: Page) {
  const { name } = expectedAsset(page);
  const shell = page.locator('.app-shell');
  if (name === 'portrait-900') {
    await expect(shell).toHaveCSS('background-image', 'none');
    const frame = await shell.evaluate(element => {
      const css = getComputedStyle(element, '::before');
      return { image: css.borderImageSource, slice: css.borderImageSlice,
        pointerEvents: css.pointerEvents, zIndex: css.zIndex, position: css.position };
    });
    expect(frame.image).toContain('emerald-table-portrait-900');
    expect(frame.slice).toBe('120 fill');
    expect(frame.pointerEvents).toBe('none');
    expect(frame.zIndex).toBe('-1');
    expect(frame.position).toBe('absolute');
  } else {
    await expect(shell).toHaveCSS('background-image', new RegExp(`emerald-table-${name}`));
    expect(await shell.evaluate(element => getComputedStyle(element, '::before').content)).toBe('none');
  }
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
  const asset = expectedAsset(page);
  expect(response.ok()).toBe(true);
  expect(response.url()).toContain(`emerald-table-${asset.name}`);
  expect(response.headers()['content-type']).toContain('image/webp');
  expect((await response.body()).byteLength).toBeLessThan(asset.budget);
  expect(imageRequests).toHaveLength(1);
  expect(await page.evaluate(async () => {
    const element = document.querySelector('.app-shell')!;
    const css = getComputedStyle(element);
    const source = css.backgroundImage === 'none' ? getComputedStyle(element, '::before').borderImageSource : css.backgroundImage;
    const image = new Image();
    image.src = source.slice(5, -2);
    await image.decode();
    return [image.naturalWidth, image.naturalHeight];
  })).toEqual(asset.dimensions);

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
  await page.goto('/?demo=lobby');
  for (const viewport of [
    { width: 320, height: 568 },
    { width: 390, height: 844 },
    { width: 844, height: 390 },
    { width: 1440, height: 900 },
    { width: 900, height: 1200 },
    { width: 1200, height: 900 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport);
    await expectPageBackground(page);
    await expect(page.getByRole('navigation', { name: '大厅导航' })).toBeInViewport({ ratio: 1 });
  }
});

test('portrait decoration paints all four gold corners on tall phones behind the content', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const image = page.waitForResponse(response => response.url().includes('emerald-table-portrait-900'));
  await page.goto('/?demo=lobby');
  expect((await image).ok()).toBe(true);
  await expectPageBackground(page);
  // Hide only the content for this paint check, retaining the real root and its
  // pseudo-element. A cover crop or a frame painted behind the root would fail.
  await page.addStyleTag({ content: '.app-shell > * { visibility: hidden !important; }' });
  const screenshot = await page.screenshot({ animations: 'disabled', scale: 'css' });
  const goldCounts = await page.evaluate(async base64 => {
    const image = new Image();
    image.src = `data:image/png;base64,${base64}`;
    await image.decode();
    const canvas = document.createElement('canvas');
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    const context = canvas.getContext('2d')!;
    context.drawImage(image, 0, 0);
    return [[0, 0], [canvas.width - 50, 0], [0, canvas.height - 50], [canvas.width - 50, canvas.height - 50]]
      .map(([x, y]) => {
        const pixels = context.getImageData(x, y, 50, 50).data;
        let gold = 0;
        for (let i = 0; i < pixels.length; i += 4) {
          const [red, green, blue] = [pixels[i], pixels[i + 1], pixels[i + 2]];
          if (red > 110 && green > 65 && blue < 150 && red > green * 1.1 && green > blue * 1.2) gold++;
        }
        return gold;
      });
  }, screenshot.toString('base64'));
  for (const count of goldCounts) expect(count).toBeGreaterThan(20);
});
