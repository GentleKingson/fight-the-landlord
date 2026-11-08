import { expect, test } from '@playwright/test';

test('optimized lobby logo loads and stays beside the title across navigation and reload', async ({ page }) => {
  const logoResponsePromise = page.waitForResponse((response) =>
    response.request().resourceType() === 'image' && response.url().includes('landlord-logo-176'));
  await page.goto('/?demo=lobby');
  const logoResponse = await logoResponsePromise;
  expect(logoResponse.ok()).toBe(true);
  expect(logoResponse.headers()['content-type']).toContain('image/webp');
  expect((await logoResponse.body()).byteLength).toBeLessThan(32 * 1024);
  const logo = page.locator('.lobby-header .brand-mark');

  async function expectLogo() {
    await expect(logo).toBeInViewport({ ratio: 1 });
    await expect.poll(() => logo.evaluate((element) => {
      const image = element as HTMLImageElement;
      return image.complete && image.naturalWidth === 176 && image.naturalHeight === 176;
    })).toBe(true);
    const bounds = (await logo.boundingBox())!;
    const heading = (await page.getByRole('heading', { name: '斗地主', level: 1 }).boundingBox())!;
    const status = (await page.locator('.lobby-status').boundingBox())!;
    expect(bounds.width).toBe(bounds.height);
    expect(bounds.width).toBeGreaterThanOrEqual(32);
    expect(bounds.width).toBeLessThanOrEqual(44);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(heading.x);
    expect(heading.x + heading.width).toBeLessThanOrEqual(status.x);
    expect(await logo.evaluate((element) => getComputedStyle(element).objectFit)).toBe('contain');
  }

  await expectLogo();
  await page.getByRole('button', { name: '我的战绩', exact: true }).click();
  await expect(page.getByRole('heading', { name: '我的战绩' })).toBeVisible();
  await expectLogo();
  await page.getByRole('button', { name: '大厅', exact: true }).click();
  await expect(page.getByRole('button', { name: /快速开局/ })).toBeVisible();
  await expectLogo();
  await page.reload();
  await expectLogo();

  await page.setViewportSize({ width: 320, height: 568 });
  await expectLogo();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(320);
});
