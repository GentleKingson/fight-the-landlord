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
});
