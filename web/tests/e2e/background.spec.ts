import { expect, test, type Page } from '@playwright/test';
import type { BackgroundScene } from '../../src/shared/ui/backgroundScene';

const FALLBACK_RGB: Record<BackgroundScene, string> = {
  lobby: 'rgb(22, 41, 33)',
  table: 'rgb(8, 40, 32)',
  settlement: 'rgb(23, 38, 31)',
};

function isSceneBackground(url: string) {
  return /\/scene-(lobby|table|settlement)-[^/]+\.webp$/.test(new URL(url).pathname);
}

function expectedVariant(page: Page) {
  const { width, height } = page.viewportSize()!;
  if (height >= width) return { suffix: 'portrait-900', dimensions: [900, 1600], budget: 60 * 1024 };
  if (width > 960) return { suffix: '1600', dimensions: [1600, 900], budget: 72 * 1024 };
  return { suffix: '960', dimensions: [960, 540], budget: 40 * 1024 };
}

async function expectSceneBackground(page: Page, scene: BackgroundScene) {
  const { suffix } = expectedVariant(page);
  const shell = page.locator('.app-shell');
  await expect(shell).toHaveAttribute('data-scene', scene);
  await expect(shell).toHaveCSS('background-image', new RegExp(`scene-${scene}-${suffix}`));
  await expect(shell).toHaveCSS('background-color', FALLBACK_RGB[scene]);
  await expect(shell).toHaveCSS('background-size', 'cover');
  await expect(shell).toHaveCSS('background-position', '50% 50%');

  const veil = await shell.evaluate((element) => {
    const css = getComputedStyle(element, '::before');
    return { pointerEvents: css.pointerEvents, zIndex: css.zIndex, position: css.position };
  });
  expect(veil.pointerEvents).toBe('none');
  expect(veil.zIndex).toBe('-1');
  expect(veil.position).toBe('absolute');

  expect(await page.evaluate(() => ({
    width: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth),
    height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight),
  }))).toEqual(page.viewportSize());
}

test.describe('scene-specific Neo-Chinese page backgrounds', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/config', (route) => route.fulfill({ json: {} }));
  });

  for (const { demo, scene } of [
    { demo: 'lobby', scene: 'lobby' },
    { demo: 'table', scene: 'table' },
    { demo: 'result', scene: 'settlement' },
  ] as const) {
    test(`${demo} loads only its matching ${scene} WebP variant and decodes cleanly`, async ({ page }) => {
      const imageRequests: string[] = [];
      page.on('request', (request) => {
        if (isSceneBackground(request.url())) imageRequests.push(request.url());
      });
      const variant = expectedVariant(page);
      const responsePromise = page.waitForResponse((response) =>
        isSceneBackground(response.url()) && response.url().includes(`scene-${scene}-`));
      await page.goto(`/?demo=${demo}`);
      const response = await responsePromise;
      expect(response.ok()).toBe(true);
      expect(response.url()).toContain(`scene-${scene}-${variant.suffix}`);
      expect(response.headers()['content-type']).toContain('image/webp');
      expect((await response.body()).byteLength).toBeLessThan(variant.budget);
      expect(imageRequests).toHaveLength(1);

      expect(await page.evaluate(async () => {
        const element = document.querySelector('.app-shell')!;
        const source = getComputedStyle(element).backgroundImage;
        const urlMatch = /url\("([^"]+)"\)/.exec(source);
        const image = new Image();
        image.src = urlMatch ? urlMatch[1] : source.slice(5, -2);
        await image.decode();
        return [image.naturalWidth, image.naturalHeight];
      })).toEqual(variant.dimensions);

      await expectSceneBackground(page, scene);
    });
  }

  test('lobby sub-panels and reload retain the tea-house lobby scene without extra fetches', async ({ page }) => {
    const imageRequests: string[] = [];
    page.on('request', (request) => {
      if (isSceneBackground(request.url())) imageRequests.push(request.url());
    });
    await page.goto('/?demo=lobby');
    await expectSceneBackground(page, 'lobby');
    for (const tab of ['战绩榜', '我的战绩', '聊天', '大厅']) {
      await page.getByRole('button', { name: tab, exact: true }).click();
      await expectSceneBackground(page, 'lobby');
    }
    expect(imageRequests).toHaveLength(1);
    await page.reload();
    await expect(page.getByRole('button', { name: /快速开局/ })).toBeInViewport({ ratio: 1 });
    await expectSceneBackground(page, 'lobby');
  });

  test('end-of-round reveal, settlement, review and return-to-lobby transition scenes cleanly', async ({ page }) => {
    await page.goto('/?demo=reveal');
    await expectSceneBackground(page, 'settlement');
    await expect(page.getByRole('region', { name: '终局牌桌' })).toBeVisible();

    await page.goto('/?demo=result');
    await expectSceneBackground(page, 'settlement');
    await expect(page.locator('.result-screen')).toHaveAttribute('data-outcome', 'victory');
    await page.getByRole('button', { name: '查看本局牌面' }).click();
    await expect(page.getByRole('heading', { name: '本局牌面' })).toBeVisible();
    await expectSceneBackground(page, 'settlement');

    await page.getByRole('button', { name: '返回结算' }).click();
    await expectSceneBackground(page, 'settlement');
    await page.getByRole('button', { name: '返回大厅' }).click();
    await expect(page.getByRole('button', { name: /快速开局/ })).toBeVisible();
    await expectSceneBackground(page, 'lobby');
  });

  test('the bamboo courtyard table scene preserves the green felt and interactive table layers', async ({ page }) => {
    await page.goto('/?demo=table');
    await expectSceneBackground(page, 'table');
    await expect(page.locator('.table-screen')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
    await expect(page.locator('.table-screen')).toHaveCSS('background-image', 'none');
    expect(await page.locator('.table-screen').evaluate((element) => getComputedStyle(element, '::before').content)).toBe('none');
    const feltBackground = await page.locator('.table-arena').evaluate((element) => getComputedStyle(element).backgroundImage);
    expect(feltBackground).toContain('radial-gradient');
    expect(feltBackground).not.toContain('scene-');
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

  test('failed background images retain readable scene fallbacks without affecting layout', async ({ page }) => {
    await page.route((url) => isSceneBackground(url.href), (route) => route.abort());
    for (const { mode, scene } of [
      { mode: 'lobby', scene: 'lobby' },
      { mode: 'bidding', scene: 'table' },
      { mode: 'table', scene: 'table' },
      { mode: 'reveal', scene: 'settlement' },
      { mode: 'result', scene: 'settlement' },
    ] as const) {
      await page.goto(`/?demo=${mode}`);
      await expectSceneBackground(page, scene);
      if (mode === 'lobby') await expect(page.getByRole('button', { name: /快速开局/ })).toBeInViewport({ ratio: 1 });
      if (mode === 'bidding') await expect(page.getByRole('button', { name: '叫地主', exact: true })).toBeInViewport({ ratio: 1 });
      if (mode === 'table') await expect(page.getByRole('button', { name: '出牌', exact: true })).toBeInViewport({ ratio: 1 });
      if (mode === 'reveal') await expect(page.getByRole('button', { name: '查看结算', exact: true })).toBeInViewport();
      if (mode === 'result') await expect(page.getByRole('button', { name: '返回大厅', exact: true })).toBeInViewport({ ratio: 1 });
    }
  });

  test('background selection follows viewport orientation and width across all scenes', async ({ page }) => {
    for (const { demo, scene } of [
      { demo: 'lobby', scene: 'lobby' },
      { demo: 'table', scene: 'table' },
      { demo: 'result', scene: 'settlement' },
    ] as const) {
      await page.goto(`/?demo=${demo}`);
      for (const viewport of [
        { width: 320, height: 568 },
        { width: 390, height: 844 },
        { width: 844, height: 390 },
        { width: 1440, height: 900 },
        { width: 900, height: 1200 },
        { width: 1200, height: 900 },
      ]) {
        await page.setViewportSize(viewport);
        await expectSceneBackground(page, scene);
      }
    }
  });
});
