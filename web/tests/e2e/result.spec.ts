import { expect, test } from '@playwright/test';

for (const [id, side] of [['p1', 'me'], ['p2', 'right'], ['p3', 'left']]) {
  for (const muted of [false, true]) {
    test(`final hand stays at ${side}, then appears in settlement (muted=${muted})`, async ({ page }) => {
      await page.clock.install({ time: new Date('2026-11-09T00:00:00Z') });
      await page.clock.pauseAt(new Date('2026-11-09T00:00:01Z'));
      await page.goto('/?demo=table');
      await expect(page.getByRole('region', { name: '斗地主牌桌' })).toBeVisible();
      await page.evaluate(`(async () => {
        const { useAppStore } = await import('/src/stores/appStore.ts');
        const { gameAudio } = await import('/src/audio/gameAudio.ts');
        gameAudio.setEnabled(${!muted});
        const state = useAppStore.getState();
        const winner = state.players.find(player => player.id === '${id}');
        state.handleMessage({ type: 'card_played', payload: { player_id: winner.id, player_name: winner.name, cards: [{ rank: 10, suit: 0, color: 0 }], cards_left: 0, hand_type: '单张' } });
        state.handleMessage({ type: 'game_over', payload: { winner_id: winner.id, winner_name: winner.name, is_landlord: winner.is_landlord, multiplier: 3, scores: state.scores, player_hands: state.playerHands } });
      })()`);
      const finalCard = page.locator(`.played-seat--${side}`).getByRole('button', { name: '10♠' });
      await expect(finalCard).toBeVisible();
      await expect(page.getByRole('listbox', { name: '手牌' })).toHaveAttribute('aria-disabled', 'true');
      await expect(page.locator('.action-bar button, .turn-banner time')).toHaveCount(0);
      const table = (await page.locator('.felt-table').boundingBox())!;
      const card = (await finalCard.boundingBox())!;
      expect(card.x).toBeGreaterThanOrEqual(table.x);
      expect(card.y).toBeGreaterThanOrEqual(table.y);
      expect(card.x + card.width).toBeLessThanOrEqual(table.x + table.width + 1);
      expect(card.y + card.height).toBeLessThanOrEqual(table.y + table.height + 1);
      await page.clock.runFor(1999);
      await expect(finalCard).toBeVisible();
      await expect(page.locator('.result-screen')).toHaveCount(0);
      await page.clock.runFor(1);
      await expect(page.locator('.table-screen')).toHaveCount(0);
      await expect(page.getByRole('region', { name: '获胜最后一手' }).getByRole('button', { name: '10♠' })).toBeVisible();
      await expect(page.getByRole('button', { name: '再来一局' })).toBeInViewport();
      await expect(page.getByRole('button', { name: '返回大厅' })).toBeInViewport();
    });
  }
}

test('reconnecting to an ended game shows the cached final hand without a delay', async ({ page }) => {
  await page.clock.install({ time: new Date('2026-11-09T00:00:00Z') });
  await page.clock.pauseAt(new Date('2026-11-09T00:00:01Z'));
  await page.goto('/?demo=table');
  await expect(page.getByRole('region', { name: '斗地主牌桌' })).toBeVisible();
  await page.evaluate(`(async () => {
    const { useAppStore } = await import('/src/stores/appStore.ts');
    const state = useAppStore.getState();
    const cards = [{ rank: 10, suit: 0, color: 0 }];
    state.handleMessage({ type: 'reconnected', payload: { player_id: 'p1', player_name: '青竹', room_code: state.roomCode, game_state: { phase: 'ended', players: state.players, hand: [], last_played: cards, last_player_id: 'p3', last_hand_type: '单张' } } });
    state.handleMessage({ type: 'game_over', payload: { winner_id: 'p3', winner_name: '松风', is_landlord: false, multiplier: 3, scores: state.scores, player_hands: state.playerHands } });
  })()`);
  await expect(page.locator('.table-screen')).toHaveCount(0);
  const replay = page.getByRole('region', { name: '获胜最后一手' });
  await expect(replay.getByRole('button', { name: '10♠' })).toBeVisible();
  await expect(replay.getByText('松风')).toBeVisible();
});

test('long winning hands keep settlement actions reachable on a small portrait screen', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=result');
  await page.evaluate(`import('/src/stores/appStore.ts').then(({ useAppStore }) => useAppStore.setState({
    lastPlayedBy: 'p1', lastHandType: '飞机',
    lastPlayed: Array.from({ length: 20 }, (_, i) => ({ rank: 3 + Math.floor(i / 3), suit: i % 3, color: 0 }))
  }))`);
  await expect(page.getByRole('region', { name: '获胜最后一手' }).locator('.card')).toHaveCount(20);
  await expect(page.getByRole('button', { name: '再来一局' })).toBeInViewport();
  await expect(page.getByRole('button', { name: '返回大厅' })).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(320);
  const lastCard = page.getByRole('region', { name: '获胜最后一手' }).locator('.card').last();
  await lastCard.scrollIntoViewIfNeeded();
  await expect(lastCard).toBeInViewport();
  await page.getByText('查看剩余手牌', { exact: true }).click();
  await expect(page.getByRole('button', { name: '返回大厅' })).toBeInViewport();
});
