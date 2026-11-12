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
  const startedAt = Date.now();
  // Freeze Date while leaving timers and animation frames running for layout.
  await page.clock.setFixedTime(startedAt);
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
  expect(layout.targets.length).toBeGreaterThan(3);
  await expect(page.getByRole('option')).toHaveCount(0);
  await expect(page.getByText('手牌 0 张')).toBeVisible();
  // Hold a partial deal independently of runner speed. Exact per-card
  // boundaries are covered in deal.test.tsx.
  await page.clock.setFixedTime(startedAt + 1000);
  await expect(page.getByText(/^手牌 [1-9] 张$/)).toBeVisible();
  await expectLandingTargets(page);
  await page.clock.setFixedTime(startedAt + 4000);
  await expect(page.getByText('等待叫地主通知…')).toBeVisible({ timeout: 5000 });
  await expect(page.getByRole('option')).toHaveCount(17);
  await expect(page.getByText('手牌 17 张')).toBeVisible();
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
      await expect(page.locator('.deal-card--left')).toHaveCount(0);
      await expect(page.getByRole('option')).toHaveCount(17);
      await expect(page.locator('.deal-card--deck')).toBeVisible();
    }
    await expect(page.getByText('等待叫地主通知…')).toBeVisible({ timeout: 2500 });
    await expect(page.getByRole('option')).toHaveCount(17);
    await expect(page.locator('.turn-banner time')).toHaveCount(0);
    await bid(page);
    await expect(page.getByRole('button', { name: '叫地主', exact: true })).toBeVisible();
  });
}

// Validate computed endpoints against real slots, including the second portrait row.
async function expectLandingTargets(page: Page) {
  await expect(page.locator('.deal-card--me')).toHaveCount(17);
  await expect.poll(() => page.locator('.deal-animation').evaluate(root => {
    const bounds = root.getBoundingClientRect();
    const style = getComputedStyle(root);
    const originX = bounds.left + parseFloat(style.getPropertyValue('--deal-origin-x')) + 15;
    const originY = bounds.top + parseFloat(style.getPropertyValue('--deal-origin-y')) + 21;
    const slots = [...document.querySelectorAll('.hand__slot')];
    return [...root.querySelectorAll('.deal-card--me')].map((card, index) => {
      const css = getComputedStyle(card);
      const target = slots[index].getBoundingClientRect();
      return Math.max(
        Math.abs(originX + parseFloat(css.getPropertyValue('--deal-x')) - target.left - target.width / 2),
        Math.abs(originY + parseFloat(css.getPropertyValue('--deal-y')) - target.top - target.height / 2)
      );
    }).reduce((a, b) => Math.max(a, b), 0);
  })).toBeLessThan(1);
}

test('flights reach actual card bounds across an orientation change without restarting', async ({ page }) => {
  await setup(page, 'muted');
  await deal(page);
  await expect(page.getByText(/^手牌 [1-9] 张$/)).toBeVisible();
  const viewport = page.viewportSize()!;
  await page.setViewportSize({ width: viewport.height, height: viewport.width });
  await expectLandingTargets(page);
  const endpoints = await page.locator('.deal-card--me').evaluateAll(cards => cards.map((card, index) => {
    // Seek each real CSS animation to just before landing and inspect rendered bounds.
    const animation = card.getAnimations()[0];
    const timing = animation.effect!.getTiming();
    animation.pause();
    animation.currentTime = Number(timing.delay) + Number(timing.duration) - 0.01;
    const flight = card.getBoundingClientRect();
    const slot = document.querySelectorAll('.hand__slot')[index].getBoundingClientRect();
    return Math.max(Math.abs(flight.left - slot.left), Math.abs(flight.top - slot.top),
      Math.abs(flight.width - slot.width), Math.abs(flight.height - slot.height));
  }));
  expect(endpoints).toHaveLength(17);
  expect(Math.max(...endpoints)).toBeLessThan(1);
  await page.screenshot({ path: `/tmp/deal-landing-${test.info().project.name}.png` });
  await bid(page); // An official early turn must finish immediately, even with paused CSS.
  await expect(page.locator('.deal-animation')).toHaveCount(0);
  await expect(page.getByRole('option')).toHaveCount(17);
  await expect(page.getByRole('button', { name: '叫地主', exact: true })).toBeVisible();
});
