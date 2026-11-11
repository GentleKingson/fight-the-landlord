import { expect, test } from '@playwright/test';

test('cycles hints, supports manual selection and restarts on the next turn', async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  await page.evaluate(async () => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    useAppStore.setState({
      hand: [7, 6, 5].flatMap(rank => [0, 1].map(suit => ({ rank, suit, color: suit % 2 }))),
      lastPlayed: [0, 1].map(suit => ({ rank: 4, suit, color: suit % 2 })),
      mustPlay: false, turnToken: 'hint-turn-1', selectedCards: new Set(), tableMessage: ''
    });
  });
  const hint = page.getByRole('button', { name: '提示', exact: true });
  const chosen = page.locator('.hand__slot.is-selected .card');
  for (const [rank, index] of [[5, 1], [6, 2], [7, 3], [5, 1]]) {
    await hint.click();
    await expect(page.getByText(`提示 ${index}/3：对子 · 再点切换`)).toBeVisible();
    await expect(chosen).toHaveCount(2);
    expect(await chosen.evaluateAll(cards => cards.map(card => card.getAttribute('aria-label')).sort()))
      .toEqual([`${rank}♠`, `${rank}♥`].sort());
  }
  await page.getByRole('button', { name: '重选', exact: true }).click();
  await expect(chosen).toHaveCount(0);
  // Click the exposed rank corner; overlapping hands cover the center of each card.
  await page.locator('.hand').getByRole('button', { name: '6♠', exact: true }).click({ position: { x: 5, y: 10 } });
  await page.locator('.hand').getByRole('button', { name: '6♥', exact: true }).click({ position: { x: 5, y: 10 } });
  await hint.click();
  await expect(page.getByText('提示 3/3：对子 · 再点切换')).toBeVisible();
  await page.evaluate(async () => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    useAppStore.getState().handleMessage({ type: 'play_turn', payload: {
      player_id: 'p1', turn_token: 'hint-turn-2', must_play: false, can_beat: true, timeout: 30
    } });
  });
  await hint.click();
  await expect(page.getByText('提示 1/3：对子 · 再点切换')).toBeVisible();
  await expect(page.getByRole('button', { name: '出牌', exact: true })).toBeEnabled();
});
