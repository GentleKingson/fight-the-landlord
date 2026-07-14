import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page, type TestInfo } from '@playwright/test';

test('table viewport screenshot is nonblank, contained, and accessible', async ({ page }, testInfo) => {
  await page.goto('/?demo=table');
  await expect(page.getByLabel('斗地主牌桌')).toBeVisible();
  await assertContained(page, ['.table-topbar', '.table-arena', '.action-bar', '.hand-zone']);

  await page.getByRole('button', { name: '聊天', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await assertAccessible(page);
  await captureVerifiedScreenshot(page, testInfo, 'table-with-chat');
});

test('lobby and result screenshots remain accessible at the configured viewport', async ({ page }, testInfo) => {
  await page.goto('/?demo=lobby');
  await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
  await assertAccessible(page);
  await captureVerifiedScreenshot(page, testInfo, 'lobby');

  await page.goto('/?demo=result');
  await expect(page.getByRole('button', { name: '返回大厅' })).toBeVisible();
  await assertAccessible(page);
  await captureVerifiedScreenshot(page, testInfo, 'result');
});

async function assertAccessible(page: Page): Promise<void> {
  const scan = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa'])
    .analyze();
  expect(scan.violations, formatViolations(scan.violations)).toEqual([]);
}

async function assertContained(page: Page, selectors: string[]): Promise<void> {
  const viewport = page.viewportSize();
  expect(viewport).not.toBeNull();
  for (const selector of selectors) {
    const box = await page.locator(selector).boundingBox();
    expect(box, `${selector} must have stable geometry`).not.toBeNull();
    expect(box!.x, `${selector} must not overflow left`).toBeGreaterThanOrEqual(-1);
    expect(box!.y, `${selector} must not overflow top`).toBeGreaterThanOrEqual(-1);
    expect(box!.x + box!.width, `${selector} must not overflow right`).toBeLessThanOrEqual(viewport!.width + 1);
    expect(box!.y + box!.height, `${selector} must not overflow bottom`).toBeLessThanOrEqual(viewport!.height + 1);
  }
}

async function captureVerifiedScreenshot(page: Page, testInfo: TestInfo, label: string): Promise<void> {
  const screenshot = await page.screenshot({ animations: 'disabled', fullPage: true });
  expect(screenshot.subarray(1, 4).toString()).toBe('PNG');
  const width = screenshot.readUInt32BE(16);
  const height = screenshot.readUInt32BE(20);
  const deviceScaleFactor = await page.evaluate(() => window.devicePixelRatio);
  expect(width).toBe(Math.round(page.viewportSize()!.width * deviceScaleFactor));
  expect(height).toBeGreaterThanOrEqual(Math.round(page.viewportSize()!.height * deviceScaleFactor));
  expect(screenshot.byteLength).toBeGreaterThan(20_000);
  await testInfo.attach(`${label}-${testInfo.project.name}`, { body: screenshot, contentType: 'image/png' });
}

function formatViolations(violations: Array<{ id: string; help: string; nodes: Array<{ target: unknown }> }>): string {
  return violations
    .map((violation) => `${violation.id}: ${violation.help} (${violation.nodes.map((node) => String(node.target)).join(', ')})`)
    .join('\n');
}
