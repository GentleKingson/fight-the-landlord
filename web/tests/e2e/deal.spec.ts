import { expect, test, type Page } from '@playwright/test';

async function setup(page: Page, mode = 'normal') {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  if (mode !== 'locked') await page.getByRole('button', { name: '聊天', exact: true }).click();
  await page.evaluate(async (mode) => {
    const load = (path: string) => import(/* @vite-ignore */ path);
    const { useAppStore } = await load('/src/stores/appStore.ts');
    const { connectGameAudio } = await load('/src/audio/connectGameAudio.ts');
    const { gameAudio } = await load('/src/audio/gameAudio.ts');
    if (mode === 'muted') gameAudio.setEnabled(false);
    const w = window as typeof window & { dealReceive: (type: string, payload: unknown) => void };
    connectGameAudio({ subscribe(listener: (message: unknown) => void) {
      w.dealReceive = (type, payload) => {
        const message = { type, payload };
        useAppStore.getState().handleMessage(message);
        listener(message);
      };
      return () => {};
    } });
    useAppStore.setState({ drawer: 'none' });
    w.dealReceive('game_start', { players: useAppStore.getState().players });
  }, mode);
}

async function deal(page: Page, id = 'first', elapsed = 0) {
  await page.evaluate(async ({ id, elapsed }) => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    const state = useAppStore.getState();
    (window as any).dealReceive('deal_cards', {
      cards: Array.from({ length: 17 }, (_, i) => ({ rank: i % 13 + 3, suit: i % 4, color: 0 })),
      bottom_cards: [], deal_id: id, deal_start_at: 100000, deal_end_at: 103870, server_time: 100000 + elapsed
    });
    (window as any).dealPlayer = state.playerId;
  }, { id, elapsed });
}

async function bid(page: Page) {
  await page.evaluate(() => (window as any).dealReceive('bid_turn', {
    player_id: (window as any).dealPlayer, timeout: 15, bid_token: 'turn-first', multiplier: 1
  }));
}

test('deal cards travel to three seats, then wait for server authorization', async ({ page }) => {
  await setup(page);
  await deal(page);
  await expect(page.locator('.deal-card')).toHaveCount(52);
  await expect(page.getByRole('button', { name: '叫地主', exact: true })).toHaveCount(0);
  await expect(page.locator('.turn-banner time')).toHaveCount(0);
  const layout = await page.locator('.deal-animation').evaluate(el => {
    const rect = el.getBoundingClientRect();
    const cards = [...el.querySelectorAll('.deal-card:not(.deal-card--deck)')].map(card => getComputedStyle(card).getPropertyValue('--deal-x'));
    return { width: rect.width, height: rect.height, left: rect.left, right: rect.right, viewport: innerWidth, targets: [...new Set(cards)] };
  });
  expect(layout.width).toBeGreaterThan(100);
  expect(layout.height).toBeGreaterThanOrEqual(72);
  expect(layout.left).toBeGreaterThanOrEqual(0);
  expect(layout.right).toBeLessThanOrEqual(layout.viewport);
  expect(layout.targets).toHaveLength(3);
  await page.screenshot({ path: `/tmp/deal-${test.info().project.name}.png` });
  await expect(page.getByText('等待叫地主通知…')).toBeVisible({ timeout: 5000 });
  await expect(page.getByRole('button', { name: '叫地主', exact: true })).toHaveCount(0);
  await bid(page);
  await expect(page.locator('.deal-animation')).toHaveCount(0);
  await expect(page.getByRole('button', { name: '叫地主', exact: true })).toBeVisible();
  await expect(page.locator('.turn-banner time')).toHaveText('15');
  await deal(page, 'redeal');
  await expect(page.locator('.deal-card')).toHaveCount(52);
  await expect(page.getByRole('button', { name: '叫地主', exact: true })).toHaveCount(0);
});

for (const mode of ['muted', 'locked', 'failed', 'reduced']) {
  test(`${mode} presentation keeps controls locked until the official turn`, async ({ page }) => {
    if (mode === 'reduced') await page.emulateMedia({ reducedMotion: 'reduce' });
    await setup(page, mode);
    if (mode === 'failed') await page.route('**/*.mp3*', route => route.request().resourceType() === 'fetch' ? route.abort() : route.continue());
    await deal(page, 'reconnected', 2500);
    if (mode === 'reduced') {
      await expect(page.locator('.deal-card--left').first()).toBeHidden();
      await expect(page.locator('.deal-card--deck')).toBeVisible();
    }
    await expect(page.getByText('等待叫地主通知…')).toBeVisible({ timeout: 2500 });
    await expect(page.locator('.turn-banner time')).toHaveCount(0);
    await bid(page);
    await expect(page.getByRole('button', { name: '叫地主', exact: true })).toBeVisible();
  });
}
