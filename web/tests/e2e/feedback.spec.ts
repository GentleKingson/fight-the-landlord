import { expect, test } from '@playwright/test';
import axe from 'axe-core';

test('live feedback remains stable and legible with reduced motion', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  const arena = page.locator('.table-arena');
  const before = await arena.boundingBox();
  await page.evaluate(async () => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ path);
    const state = useAppStore.getState();
    useAppStore.setState({ submissionMessage: '操作已提交，等待服务器确认…', currentTurn: state.playerId,
      timeout: 4, timerStart: Date.now(), players: state.players.map((p: object) => ({ ...p, cards_count: 1 })) });
  });
  await expect(page.locator('.cards-warning').first()).toBeVisible();
  await expect(page.locator('.turn-banner')).toHaveClass(/is-urgent/);
  expect(await arena.boundingBox()).toEqual(before);
  expect(await page.locator('.turn-banner time').evaluate(el => getComputedStyle(el).animationName)).toBe('none');
  await expect(page.getByRole('button', { name: '规则', exact: true })).toBeInViewport({ ratio: 1 });
  await page.getByRole('button', { name: '规则', exact: true }).click();
  await expect(page.getByText(/选牌：点击/)).toBeVisible();
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => {
    const result = await (window as any).axe.run(document, { runOnly: ['color-contrast'] });
    return result.violations.map((v: any) => ({ id: v.id, nodes: v.nodes.map((n: any) => ({ target: n.target, summary: n.failureSummary })) }));
  });
  expect(violations).toEqual([]);
});

for (const scene of ['lobby', 'result']) {
  test(`${scene} text meets contrast requirements`, async ({ page }) => {
    await page.route('**/api/config', route => route.fulfill({ json: {} }));
    await page.goto(`/?demo=${scene}`);
    await page.addScriptTag({ content: axe.source });
    const violations = await page.evaluate(async () => {
      const result = await (window as any).axe.run(document, { runOnly: ['color-contrast'] });
      return result.violations.map((v: any) => ({ id: v.id, nodes: v.nodes.map((n: any) => ({ target: n.target, summary: n.failureSummary })) }));
    });
    expect(violations).toEqual([]);
  });
}
