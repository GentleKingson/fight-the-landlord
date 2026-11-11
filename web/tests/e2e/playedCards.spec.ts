import { expect, test } from '@playwright/test';

test('all three seats display the triple before its higher pair', async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  await page.evaluate(async () => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    const cards = [
      { suit: 0, rank: 14, color: 0 }, { suit: 1, rank: 14, color: 1 },
      { suit: 2, rank: 3, color: 0 }, { suit: 1, rank: 3, color: 1 }, { suit: 0, rank: 3, color: 0 }
    ];
    useAppStore.setState({ mustPlay: false });
    for (const player of useAppStore.getState().players) {
      useAppStore.getState().handleMessage({ type: 'card_played', payload: {
        player_id: player.id, player_name: player.name, cards, cards_left: 5, hand_type: '三带二'
      } });
    }
  });
  for (const side of ['left', 'me', 'right']) {
    const cards = page.locator(`.played-seat--${side} .card--played`);
    await expect(cards).toHaveCount(5);
    expect(await cards.evaluateAll(elements => elements.map(card => card.getAttribute('aria-label'))))
      .toEqual(['3♠', '3♥', '3♣', 'A♠', 'A♥']);
    const positions = await cards.evaluateAll(elements => elements.map(card => card.getBoundingClientRect().left));
    expect(positions.every((x, index) => index === 0 || x > positions[index - 1])).toBe(true);
  }
  await expect(page.getByText('待压过', { exact: true })).toHaveCount(1);
  await expect(page.locator('.played-seat--left')).toHaveClass(/played-seat--target/);
});

test('target label and long hands fit the table and clear for a new trick', async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  await expect(page.getByText('跟牌目标：松风')).toBeVisible();
  await page.evaluate(async () => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    const cards = [3, 4, 5, 6].flatMap(rank => [0, 1, 2].map(suit => ({ rank, suit, color: suit % 2 })));
    cards.push(...[8, 9, 10, 11].flatMap(rank => [0, 1].map(suit => ({ rank, suit, color: suit % 2 }))));
    for (const player of useAppStore.getState().players) {
      useAppStore.getState().handleMessage({ type: 'card_played', payload: {
        player_id: player.id, player_name: player.name, cards, cards_left: 1, hand_type: '飞机带对'
      } });
    }
  });
  const label = page.getByText('待压过', { exact: true });
  await expect(label).toBeVisible();
  const bounds = await label.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
  for (const side of ['left', 'me', 'right']) {
    const row = page.locator(`.played-seat--${side} .played-cards__row`);
    await expect(row.locator('.card--played')).toHaveCount(20);
    const box = await row.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
  }
  await page.evaluate(async () => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    useAppStore.getState().handleMessage({ type: 'play_turn', payload: {
      player_id: 'p3', must_play: true, can_beat: true, timeout: 30
    } });
  });
  await expect(label).toHaveCount(0);
  await expect(page.locator('.played-seat--target')).toHaveCount(0);
  await expect(page.getByText('新一轮，请出牌')).toBeVisible();
});

test('target ring follows visible cards for short and long plays', async ({ page }, testInfo) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  for (const count of [1, 2, 4, 5, 12, 20]) {
    await page.evaluate(async (count) => {
      const modulePath = '/src/stores/appStore.ts';
      const { useAppStore } = await import(/* @vite-ignore */ modulePath);
      const state = useAppStore.getState();
      const actions = Object.fromEntries(state.players.map((player: { id: string }) => [player.id, {
        type: 'play', player_id: player.id, hand_type: '出牌',
        cards: Array.from({ length: count }, (_, i) => ({ rank: 3 + i % 13, suit: i % 4, color: i % 2 }))
      }]));
      useAppStore.setState({ mustPlay: false, lastPlayedBy: state.players[0].id, seatActions: actions });
    }, count);
    await expect(page.locator('.played-seat--target')).toHaveCount(1);
    for (const side of ['left', 'me', 'right']) {
      const row = page.locator(`.played-seat--${side} .played-cards__row`);
      await expect(row.locator('.card')).toHaveCount(count);
      const geometry = await row.evaluate(element => {
        const rect = element.getBoundingClientRect();
        const cards = [...element.children].map(card => card.getBoundingClientRect());
        return { left: rect.left, right: rect.right, first: cards[0].left, last: cards.at(-1)!.right };
      });
      expect(Math.abs(geometry.left - geometry.first)).toBeLessThan(1);
      expect(Math.abs(geometry.right - geometry.last)).toBeLessThan(1);
    }
    if (count === 4) await page.screenshot({ path: `/tmp/ddz-layout-${testInfo.project.name}.png` });
  }
  await page.goto('/?demo=lobby');
  await expect(page.locator('.quick-card__art .card')).toHaveCount(2);
  await page.screenshot({ path: `/tmp/ddz-lobby-${testInfo.project.name}.png` });
});
