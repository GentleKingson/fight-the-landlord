import { expect, test } from '@playwright/test';

// Opt-in integration against a real Go + Redis + AI deployment.
test('human practice survives reload and finishes a recorded game', async ({ page, context }) => {
  test.skip(!process.env.DDZ_LIVE_URL, 'Set DDZ_LIVE_URL to the running game server');
  test.setTimeout(240_000);
  await page.goto(process.env.DDZ_LIVE_URL!);
  await expect(page.getByText('本服务已启用训练数据采集', { exact: false })).toBeVisible();
  await expect(page.getByText('正在连接服务器...')).toHaveCount(0);
  const identity = (await context.cookies()).find(cookie => cookie.name === 'ddz_guest')!;
  expect(identity.httpOnly).toBe(true);
  expect(await page.evaluate(() => document.cookie)).not.toContain('ddz_guest');
  await page.getByRole('button', { name: /人机练习/ }).click();
  await expect(page.getByLabel('斗地主牌桌')).toBeVisible();
  await expect(page.locator('.player-avatar img')).toHaveCount(2);
  const avatars = await page.locator('.player-avatar img').evaluateAll(images => images.map(image => (image as HTMLImageElement).src));
  expect(new Set(avatars).size).toBe(2);
  await page.reload();
  await expect(page.getByLabel('斗地主牌桌')).toBeVisible();
  await expect(page.locator('.player-avatar img')).toHaveCount(2);
  expect(await page.locator('.player-avatar img').evaluateAll(images => images.map(image => (image as HTMLImageElement).src))).toEqual(avatars);
  expect((await context.cookies()).find(cookie => cookie.name === 'ddz_guest')?.value).toBe(identity.value);
  let plays = 0;
  for (let step = 0; step < 1000; step++) {
    if (await page.getByRole('button', { name: '返回大厅' }).isVisible()) break;
    const bid = page.getByRole('button', { name: /^(叫地主|抢地主)$/ });
    const hint = page.getByRole('button', { name: '提示', exact: true });
    if (await bid.isVisible() && await bid.isEnabled()) {
      await bid.click();
    } else if (await hint.isVisible() && await hint.isEnabled()) {
      await hint.click();
      const play = page.getByRole('button', { name: '出牌', exact: true });
      if (await play.isEnabled()) { await play.click(); plays++; }
      else {
        const pass = page.getByRole('button', { name: '不出', exact: true });
        if (await pass.isEnabled()) await pass.click();
      }
    }
    await page.waitForTimeout(200);
  }
  await expect(page.getByRole('button', { name: '返回大厅' })).toBeVisible();
  expect(plays).toBeGreaterThan(0);
  expect(await page.evaluate(() => sessionStorage.getItem('ddz_match_mode'))).toBe('practice');
  await page.getByRole('button', { name: '再来一局' }).click();
  await expect(page.getByLabel('斗地主牌桌')).toBeVisible();
  await expect(page.getByLabel('斗地主牌桌')).toContainText('🤖');
});

test('three people can join, restore a waiting room and start bidding', async ({ browser }) => {
  test.skip(!process.env.DDZ_LIVE_URL, 'Set DDZ_LIVE_URL to the running game server');
  const contexts = await Promise.all([browser.newContext(), browser.newContext(), browser.newContext()]);
  try {
    const pages = await Promise.all(contexts.map(context => context.newPage()));
    for (const page of pages) {
      await page.goto(process.env.DDZ_LIVE_URL!);
      await expect(page.getByText('正在连接服务器...')).toHaveCount(0);
    }
    await pages[0].getByRole('button', { name: /创建房间/ }).click();
    const code = pages[0].locator('.room-code-panel strong');
    await expect(code).toHaveText(/^\d{6}$/);
    const roomCode = (await code.textContent())!;
    for (const page of pages.slice(1)) {
      await page.getByRole('textbox', { name: '加入房间', exact: true }).fill(roomCode);
      await page.getByRole('button', { name: '加入', exact: true }).click();
      await expect(page.locator('.room-code-panel strong')).toHaveText(roomCode);
    }
    // Replace the middle seat and verify it is neither duplicated nor lost.
    await pages[1].getByRole('button', { name: '离开房间', exact: true }).click();
    await expect(pages[0].getByText('2/3 人已入座')).toBeVisible();
    await expect(pages[0].getByText('等待加入', { exact: true })).toHaveCount(1);
    await pages[1].getByRole('textbox', { name: '加入房间', exact: true }).fill(roomCode);
    await pages[1].getByRole('button', { name: '加入', exact: true }).click();
    await expect(pages[0].getByText('3/3 人已入座')).toBeVisible();
    await pages[0].reload();
    await expect(pages[0].getByText('3/3 人已入座')).toBeVisible();
    for (const page of pages) await page.getByRole('button', { name: '准备开始' }).click();
    for (const page of pages) await expect(page.getByLabel('斗地主牌桌')).toBeVisible();
    const identities = await Promise.all(contexts.map(async context => (await context.cookies()).find(cookie => cookie.name === 'ddz_guest')?.value));
    expect(new Set(identities).size).toBe(3);
    await pages[2].close();
    await expect(pages[0].getByText('离线', { exact: true })).toHaveCount(1);
    await pages[0].reload();
    await expect(pages[0].getByLabel('斗地主牌桌')).toBeVisible();
    await expect(pages[0].getByText('离线', { exact: true })).toHaveCount(1);
  } finally { await Promise.all(contexts.map(context => context.close())); }
});


test('duplicated credentials replace the old tab without a reconnect loop', async ({ page, context }) => {
  test.skip(!process.env.DDZ_LIVE_URL, 'Set DDZ_LIVE_URL to the running game server');
  await page.goto(process.env.DDZ_LIVE_URL!);
  await expect(page.getByText('正在连接服务器...')).toHaveCount(0);
  const identity = (await context.cookies()).find(cookie => cookie.name === 'ddz_guest')!;
  expect(identity.httpOnly).toBe(true);
  expect(await page.evaluate(() => document.cookie)).not.toContain('ddz_guest');
  expect(identity).toBeTruthy();
  const duplicate = await context.newPage();
  await duplicate.goto(process.env.DDZ_LIVE_URL!);
  await expect(page.getByText('此账号已在另一个页面继续游戏，本页面已停止重连')).toBeVisible();
  // Observe more than two retry intervals; the displaced page must stay stopped.
  await page.waitForTimeout(6000);
  await expect(page.getByText('此账号已在另一个页面继续游戏，本页面已停止重连')).toBeVisible();
  await duplicate.getByRole('button', { name: /创建房间/ }).click();
  await expect(duplicate.locator('.room-code-panel strong')).toHaveText(/^\d{6}$/);
  expect((await context.cookies()).find(cookie => cookie.name === 'ddz_guest')?.value).toBe(identity.value);
  await duplicate.close();
});

test('closing the last tab and reopening restores a retained practice game using only the cookie', async ({ page, context }) => {
  test.skip(!process.env.DDZ_LIVE_URL, 'Set DDZ_LIVE_URL to the running game server');
  await page.goto(process.env.DDZ_LIVE_URL!);
  await expect(page.getByText('正在连接服务器...')).toHaveCount(0);
  const cookie = (await context.cookies()).find(cookie => cookie.name === 'ddz_guest')!;
  expect(cookie.httpOnly).toBe(true);
  expect(cookie.expires).toBeGreaterThan(Date.now() / 1000);
  await page.getByRole('button', { name: /人机练习/ }).click();
  await expect(page.getByLabel('斗地主牌桌')).toBeVisible();
  await page.close();
  const reopened = await context.newPage();
  await reopened.goto(process.env.DDZ_LIVE_URL!);
  await expect(reopened.getByLabel('斗地主牌桌')).toBeVisible();
  expect(await reopened.evaluate(() => sessionStorage.getItem('ddz_next_reconnect'))).toBeNull();
  expect(await reopened.evaluate(() => document.cookie)).not.toContain('ddz_guest');
  expect((await context.cookies()).find(cookie => cookie.name === 'ddz_guest')?.value).toBe(cookie.value);
  await reopened.close();
});
