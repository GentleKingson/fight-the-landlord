import { expect, test, type Page } from '@playwright/test';
import { RESULT_ACTION_GUARD_MS } from '../../src/shared/resultPresentation';

async function finish(page: Page, winnerId = 'p3') {
  await page.evaluate(async winnerId => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    const players = useAppStore.getState().players;
    useAppStore.getState().handleMessage({ type: 'game_start', payload: { players } });
    useAppStore.setState({ phase: 'playing', roundId: 'browser-round' });
    const winner = players.find((player: { id: string }) => player.id === winnerId);
    const cards = (count: number) => Array.from({ length: count }, (_, i) => ({ suit: i % 4, rank: 3 + Math.floor(i / 4), color: i % 2 }));
    useAppStore.getState().handleMessage({ type: 'card_played', payload: {
      player_id: winner.id, player_name: winner.name, cards: cards(winner.is_landlord ? 20 : 17), cards_left: 0, hand_type: '飞机'
    } });
    useAppStore.getState().handleMessage({ type: 'game_over', payload: {
      winner_id: winner.id, winner_name: winner.name, is_landlord: winner.is_landlord, multiplier: 8,
      player_hands: players.map((player: { id: string; name: string; is_landlord: boolean }) => ({
        player_id: player.id, player_name: player.name, cards: cards(player.id === winner.id ? 0 : player.is_landlord ? 20 : 17)
      })),
      scores: players.map((player: { id: string; name: string; is_landlord: boolean }) => ({
        player_id: player.id, player_name: player.name, is_landlord: player.is_landlord, score: player.is_landlord === winner.is_landlord ? 24 : -12
      }))
    } });
  }, winnerId);
}

test.beforeEach(async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: { collect_training_data: true } }));
  await page.clock.install({ time: new Date('2026-11-09T08:00:00Z') });
  await page.goto('/?demo=table');
  await expect(page.getByLabel('手牌')).toBeVisible();
});

for (const winner of ['p1', 'p2', 'p3']) {
  test(`reveals and reviews all seats with long hands when ${winner} wins`, async ({ page }, testInfo) => {
    await finish(page, winner);
    const board = page.getByRole('region', { name: '终局牌桌' });
    const settle = page.getByRole('button', { name: '查看结算' });
    await expect(board).toBeVisible();
    await expect(settle).toBeInViewport();
    await expect(page.getByRole('button', { name: '出牌', exact: true })).toHaveCount(0);
    await expect(board.locator('.card')).toHaveCount(54);
    expect(await board.locator('.card').evaluateAll(cards => cards.every(card => card.getAttribute('role') === 'img' && (card as HTMLElement).tabIndex === -1))).toBe(true);

    for (const label of ['获胜最后一手', '上家剩余手牌', '下家剩余手牌', '我剩余手牌']) {
      const section = page.getByRole('region', { name: label });
      const cards = section.locator('.card');
      if (await cards.count()) {
        await cards.last().scrollIntoViewIfNeeded();
        await expect(cards.last()).toBeInViewport();
        const { bounds, boxes } = await section.evaluate(element => ({
          bounds: element.getBoundingClientRect().toJSON(),
          boxes: [...element.querySelectorAll('.card')].map(card => card.getBoundingClientRect().toJSON()),
        }));
        for (const box of boxes) {
          expect(box.width).toBeGreaterThanOrEqual(44);
          expect(box.x).toBeGreaterThanOrEqual(bounds.x);
          expect(box.x + box.width).toBeLessThanOrEqual(bounds.x + bounds.width + 1);
        }
      }
      await expect(settle).toBeInViewport();
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth && document.documentElement.scrollHeight <= innerHeight)).toBe(true);
    await board.evaluate(element => { element.scrollTop = 0; });
    if (winner === 'p3') await page.screenshot({ path: `/tmp/ddz-end-${testInfo.project.name}.png` });

    const originalCards = await board.locator('.card').evaluateAll(cards => cards.map(card => card.getAttribute('aria-label')));
    await settle.click();
    await expect(page.getByText('最终倍数 x8')).toBeVisible();
    await expect(page.locator('.card')).toHaveCount(0);
    for (let i = 0; i < 2; i++) {
      await page.getByRole('button', { name: '查看本局牌面' }).click();
      await expect(page.getByRole('heading', { name: '本局牌面' })).toBeFocused();
      await expect(page.getByText('仅供查看 · 不限时')).toBeVisible();
      await page.clock.fastForward(180_000);
      expect(await board.locator('.card').evaluateAll(cards => cards.map(card => card.getAttribute('aria-label')))).toEqual(originalCards);
      await expect(page.getByText(/秒后显示结算/)).toHaveCount(0);
      await page.getByRole('button', { name: '返回结算' }).click();
    }
    await page.getByRole('button', { name: '返回大厅' }).click();
    await expect(board).toHaveCount(0);
    expect(await page.evaluate(async () => {
      const path = '/src/stores/appStore.ts';
      return (await import(/* @vite-ignore */ path)).useAppStore.getState().endSnapshot;
    })).toBeNull();
  });
}

test('auto settlement and keyboard review work without restarting the timer', async ({ page }) => {
  await finish(page);
  await page.clock.fastForward(10_000);
  const review = page.getByRole('button', { name: '查看本局牌面' });
  await expect(review).toBeVisible();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: '重新匹配' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(review).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { name: '本局牌面' })).toBeFocused();
  await page.keyboard.press('Tab'); // Audio remains available in the read-only table.
  await expect(page.getByRole('button', { name: /^(开启音效|关闭音效)$/ })).toBeFocused();
  await page.keyboard.press('Tab'); // Scrollable board is keyboard reachable.
  await expect(page.getByRole('region', { name: '终局牌桌' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: '返回结算' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(review).toBeVisible();
});

for (const muted of [false, true]) {
  test(`preserves the gesture guard and audio control (muted=${muted})`, async ({ page }) => {
    await page.clock.pauseAt(new Date('2026-11-09T08:00:10Z'));
    await page.evaluate(async muted => {
      const modulePath = '/src/audio/gameAudio.ts';
      (await import(/* @vite-ignore */ modulePath)).gameAudio.setEnabled(!muted);
    }, muted);
    await page.getByLabel('手牌', { exact: true }).getByRole('option').first().click();
    const play = (await page.getByRole('button', { name: '出牌', exact: true }).boundingBox())!;
    await page.mouse.move(play.x + play.width / 2, play.y + play.height / 2);
    await page.mouse.down();
    await finish(page);
    await page.mouse.up();
    const settle = page.getByRole('button', { name: '查看结算' });
    await expect(settle).toBeDisabled();
    await page.clock.runFor(RESULT_ACTION_GUARD_MS);
    await expect(settle).toBeEnabled();
    await expect(page.getByRole('button', { name: muted ? '开启音效' : '关闭音效' })).toBeVisible();
    await settle.click();
    await expect(page.getByRole('button', { name: '查看本局牌面' })).toBeVisible();
  });
}

test('wheel over long remaining hands reaches both review boundaries', async ({ page }, testInfo) => {
  const viewport = testInfo.project.name === 'desktop' ? { width: 1280, height: 600 }
    : testInfo.project.name === 'mobile-portrait' ? { width: 320, height: 568 } : { width: 667, height: 375 };
  await page.setViewportSize(viewport);
  await finish(page);
  await page.getByRole('button', { name: '查看结算' }).click();
  await page.getByRole('button', { name: '查看本局牌面' }).click();
  const board = page.getByRole('region', { name: '终局牌桌' });
  const remaining = page.getByRole('region', { name: '我剩余手牌' });
  await expect.poll(() => board.evaluate(el => el.scrollHeight - el.clientHeight)).toBeGreaterThan(0);
  await remaining.locator('.card').first().scrollIntoViewIfNeeded();
  const wheelOverCards = async (delta: number) => {
    const outer = (await board.boundingBox())!;
    const inner = (await remaining.boundingBox())!;
    const top = Math.max(outer.y, inner.y);
    const bottom = Math.min(outer.y + outer.height, inner.y + inner.height);
    expect(bottom).toBeGreaterThan(top);
    await page.mouse.move(outer.x + outer.width / 2, (top + bottom) / 2);
    await page.mouse.wheel(0, delta);
  };
  await wheelOverCards(10_000);
  await expect.poll(() => board.evaluate(el => el.scrollHeight - el.clientHeight - el.scrollTop)).toBeLessThanOrEqual(1);
  await expect(remaining.locator('.card').last()).toBeInViewport({ ratio: 1 });
  await expect(page.getByRole('button', { name: '返回结算' })).toBeInViewport({ ratio: 1 });
  await wheelOverCards(-10_000);
  await expect.poll(() => board.evaluate(el => el.scrollTop)).toBe(0);
  await expect(page.getByRole('button', { name: '返回结算' })).toBeInViewport({ ratio: 1 });
  expect(await page.evaluate(() => ({ width: document.documentElement.scrollWidth, height: document.documentElement.scrollHeight }))).toEqual(viewport);
});

test('narrow screens and rotation keep long cards readable and the exit visible', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop', 'Explicit viewport sweep only runs once');
  await finish(page);
  await page.getByRole('button', { name: '查看结算' }).click();
  await page.getByRole('button', { name: '查看本局牌面' }).click();
  for (const viewport of [{ width: 320, height: 568 }, { width: 667, height: 375 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    await expect(page.getByRole('button', { name: '返回结算' })).toBeInViewport();
    const cards = page.getByRole('region', { name: '终局牌桌' }).locator('.card');
    expect(await cards.evaluateAll(cards => cards.every(card => {
      const box = card.getBoundingClientRect();
      return box.width >= 44 && box.left >= 0 && box.right <= innerWidth;
    }))).toBe(true);
    await cards.last().scrollIntoViewIfNeeded();
    await expect(cards.last()).toBeInViewport();
    await expect(page.getByRole('button', { name: '返回结算' })).toBeInViewport();
    await page.screenshot({ path: `/tmp/ddz-end-${viewport.width}x${viewport.height}.png` });
  }
});
