import { expect, test } from '@playwright/test';
import { FINAL_PLAY_DURATION_MS, RESULT_ACTION_GUARD_MS } from '../../src/shared/resultPresentation';

for (const muted of [false, true]) {
  test(`early settlement uses the action bar without covering cards (muted=${muted})`, async ({ page, isMobile }, testInfo) => {
    await page.setViewportSize(testInfo.project.name === 'desktop'
      ? { width: 1366, height: 768 }
      : testInfo.project.name === 'mobile-portrait'
        ? { width: 320, height: 568 }
        : { width: 667, height: 375 });
    await page.clock.install({ time: new Date('2026-11-09T00:00:00Z') });
    await page.clock.pauseAt(new Date('2026-11-09T00:00:01Z'));
    await page.route('**/api/config', route => route.fulfill({ json: {} }));
    await page.goto('/?demo=table');
    await page.getByLabel('手牌', { exact: true }).getByRole('button').first().click();
    const play = page.getByRole('button', { name: '出牌', exact: true });
    const playBox = (await play.boundingBox())!;
    await page.mouse.move(playBox.x + playBox.width / 2, playBox.y + playBox.height / 2);
    await page.mouse.down();
    await page.evaluate(async muted => {
      const load = (path: string) => import(/* @vite-ignore */ path);
      const { useAppStore } = await load('/src/stores/appStore.ts');
      const { gameAudio } = await load('/src/audio/gameAudio.ts');
      gameAudio.setEnabled(!muted);
      const state = useAppStore.getState();
      const winner = state.players.find((player: { id: string }) => player.id === 'p3');
      state.handleMessage({ type: 'card_played', payload: {
        player_id: winner.id, player_name: winner.name,
        cards: Array.from({ length: 20 }, (_, i) => ({ rank: 3 + Math.floor(i / 4), suit: i % 4, color: i % 2 })),
        cards_left: 0, hand_type: '飞机',
      } });
      state.handleMessage({ type: 'game_over', payload: {
        winner_id: winner.id, winner_name: winner.name, is_landlord: false,
        multiplier: 3, scores: state.scores, player_hands: state.playerHands,
      } });
    }, muted);
    await page.mouse.up(); // Release a play gesture after the UI has changed.
    const reveal = page.getByRole('button', { name: '查看结算' });
    await expect(reveal).toBeDisabled();
    const button = (await reveal.boundingBox())!;
    await page.mouse.click(button.x + button.width / 2, button.y + button.height / 2);
    await expect(page.locator('.table-screen')).toBeVisible();
    await page.clock.runFor(RESULT_ACTION_GUARD_MS);
    await expect(reveal).toBeEnabled();
    await expect(reveal).toBeInViewport({ ratio: 1 });
    expect(button.height).toBeGreaterThanOrEqual(44);
    const actionBar = (await page.locator('.action-bar').boundingBox())!;
    const arena = (await page.locator('.table-arena').boundingBox())!;
    const hand = (await page.locator('.hand-zone').boundingBox())!;
    expect(arena.y + arena.height).toBeLessThanOrEqual(actionBar.y);
    expect(actionBar.y + actionBar.height).toBeLessThanOrEqual(hand.y);
    for (const card of await page.locator('.played-seat--left .card').all()) {
      await expect(card).toBeInViewport({ ratio: 1 });
      const box = (await card.boundingBox())!;
      expect(box.y + box.height).toBeLessThanOrEqual(actionBar.y);
    }
    await expect(page.getByRole('button', { name: muted ? '开启音效' : '关闭音效' })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('final-play.png') });
    if (isMobile) await reveal.tap();
    else {
      await reveal.focus();
      await page.keyboard.press('Enter');
    }
    await expect(page.locator('.table-screen')).toHaveCount(0);
    await expect(page.getByRole('region', { name: '获胜最后一手' }).locator('.card')).toHaveCount(20);
  });
}

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
      await expect(page.locator('.turn-banner time')).toHaveCount(0);
      await expect(page.getByRole('button', { name: /^(出牌|不出|提示|重选)$/ })).toHaveCount(0);
      await expect(page.getByRole('button', { name: '查看结算' })).toBeDisabled();
      await expect(page.getByRole('timer')).toHaveText('5 秒后自动结算');
      const table = (await page.locator('.felt-table').boundingBox())!;
      const card = (await finalCard.boundingBox())!;
      expect(card.x).toBeGreaterThanOrEqual(table.x);
      expect(card.y).toBeGreaterThanOrEqual(table.y);
      expect(card.x + card.width).toBeLessThanOrEqual(table.x + table.width + 1);
      expect(card.y + card.height).toBeLessThanOrEqual(table.y + table.height + 1);
      await page.clock.runFor(FINAL_PLAY_DURATION_MS - 1);
      await expect(page.getByRole('timer')).toHaveText('1 秒后自动结算');
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

test('wheel over expanded remaining cards reaches both settlement scroll boundaries', async ({ page }, testInfo) => {
  const viewport = testInfo.project.name === 'desktop'
    ? { width: 1280, height: 600 }
    : testInfo.project.name === 'mobile-portrait'
      ? { width: 320, height: 568 }
      : { width: 667, height: 375 };
  await page.setViewportSize(viewport);
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=result');
  await page.evaluate(async () => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    const deck = Array.from({ length: 54 }, (_, i) => ({
      rank: i < 52 ? 3 + Math.floor(i / 4) : i - 36,
      suit: i < 52 ? i % 4 : 4,
      color: i % 2,
    }));
    useAppStore.setState({
      lastPlayedBy: 'p1', lastPlayed: deck.slice(0, 17),
      playerHands: [
        { player_id: 'p2', player_name: '山月', cards: deck.slice(17, 37) },
        { player_id: 'p3', player_name: '松风', cards: deck.slice(37) },
      ],
    });
  });
  await page.getByText('查看剩余手牌', { exact: true }).click();
  const scroller = page.locator('.result-hands');
  const remaining = page.locator('.remaining-hands__list');
  await expect.poll(() => scroller.evaluate(el => el.scrollHeight - el.clientHeight)).toBeGreaterThan(0);
  // Set up a visible starting card; all boundary movement below uses real wheel input.
  await remaining.locator('.card').first().scrollIntoViewIfNeeded();

  // Target the visible cards without hover()/scrollIntoView(), which can move
  // containers programmatically and hide a broken native scroll chain.
  const wheelOverRemaining = async (deltaY: number) => {
    const outer = (await scroller.boundingBox())!;
    const inner = (await remaining.boundingBox())!;
    const top = Math.max(outer.y, inner.y);
    const bottom = Math.min(outer.y + outer.height, inner.y + inner.height);
    expect(bottom - top).toBeGreaterThan(0);
    const point = { x: outer.x + outer.width / 2, y: (top + bottom) / 2 };
    expect(await page.evaluate(({ x, y }) =>
      !!document.elementFromPoint(x, y)?.closest('.remaining-hands__list'), point)).toBe(true);
    await page.mouse.move(point.x, point.y);
    await page.mouse.wheel(0, deltaY);
  };
  const expectActionsReachable = async () => {
    for (const name of ['再来一局', '返回大厅']) {
      const button = page.getByRole('button', { name, exact: true });
      await expect(button).toBeInViewport({ ratio: 1 });
      await button.click({ trial: true });
    }
  };

  await expectActionsReachable();
  await wheelOverRemaining(10_000);
  await expect.poll(() => scroller.evaluate(el =>
    el.scrollHeight - el.clientHeight - el.scrollTop)).toBeLessThanOrEqual(1);
  await expect(remaining.locator('.card').last()).toBeInViewport({ ratio: 1 });
  await expectActionsReachable();
  await wheelOverRemaining(-10_000);
  await expect.poll(() => scroller.evaluate(el => el.scrollTop)).toBe(0);
  await expect(page.getByRole('heading', { name: '获胜最后一手' })).toBeInViewport({ ratio: 1 });
  await expectActionsReachable();
  expect(await page.evaluate(() => ({
    width: document.documentElement.scrollWidth,
    height: document.documentElement.scrollHeight,
  }))).toEqual(viewport);
  await page.getByRole('button', { name: '返回大厅', exact: true }).click();
  await expect(page.getByRole('navigation', { name: '大厅导航' })).toBeVisible();
});
