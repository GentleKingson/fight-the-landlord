import { expect, test } from '@playwright/test';

test('closing the last tab and reopening restores a retained practice game using only the cookie', async ({ page, context }) => {
  const liveUrl = process.env.DDZ_LIVE_URL;
  test.skip(!liveUrl, 'Set DDZ_LIVE_URL to the running game server');
  if (!liveUrl) {
    return;
  }
  await page.goto(liveUrl);
  await expect(page.getByText('正在连接服务器...')).toHaveCount(0);
  const cookie = (await context.cookies()).find((entry) => entry.name === 'ddz_guest');
  if (!cookie) {
    throw new Error('Expected ddz_guest cookie');
  }
  expect(cookie.httpOnly).toBe(true);
  expect(cookie.expires).toBeGreaterThan(Date.now() / 1000);
  await page.getByRole('button', { name: /人机练习/ }).click();
  await expect(page.getByLabel('斗地主牌桌')).toBeVisible();
  const room = page.getByText(/^房间号 \d{6}$/);
  await expect(room).toBeVisible();
  const roomLabel = await room.innerText();
  await page.close();
  const reopened = await context.newPage();
  await reopened.goto(liveUrl);
  await expect(reopened.getByLabel('斗地主牌桌')).toBeVisible();
  await expect(reopened.getByText(roomLabel, { exact: true })).toBeVisible();
  expect(await reopened.evaluate(() => sessionStorage.getItem('ddz_next_reconnect'))).toBeNull();
  expect(await reopened.evaluate(() => document.cookie)).not.toContain('ddz_guest');
  expect((await context.cookies()).find((entry) => entry.name === 'ddz_guest')?.value).toBe(cookie.value);
  await reopened.close();
});

test('three browser identities can join a room, start a game, and refresh in place', async ({ browser }) => {
  const liveUrl = process.env.DDZ_LIVE_URL;
  test.skip(!liveUrl, 'Set DDZ_LIVE_URL to the running game server');
  if (!liveUrl) return;

  const contexts = await Promise.all(Array.from({ length: 3 }, () => browser.newContext()));
  try {
    const pages = await Promise.all(contexts.map((context) => context.newPage()));
    await Promise.all(pages.map((page) => page.goto(liveUrl)));
    await pages[0].getByRole('button', { name: /创建房间/ }).click();
    const code = pages[0].locator('#waiting-room-code');
    await expect(code).toHaveText(/^\d{6}$/);
    const roomCode = await code.innerText();
    for (const page of pages.slice(1)) {
      await page.getByLabel('加入房间', { exact: true }).fill(roomCode);
      await page.getByRole('button', { name: '加入', exact: true }).click();
      await expect(page.locator('#waiting-room-code')).toHaveText(roomCode);
      await page.getByRole('button', { name: '准备开始', exact: true }).click();
      await expect(page.getByRole('button', { name: '取消准备', exact: true })).toBeVisible();
    }
    await pages[0].getByRole('button', { name: '开始游戏', exact: true }).click();
    await pages[0].getByRole('button', { name: '确认开始', exact: true }).click();
    for (const page of pages) await expect(page.getByLabel('斗地主牌桌')).toBeVisible();

    const cookies = await Promise.all(contexts.map((context) => context.cookies()));
    const identities = cookies.map((entries) => entries.find((entry) => entry.name === 'ddz_guest'));
    expect(identities.every((cookie) => cookie?.httpOnly)).toBe(true);
    expect(new Set(identities.map((cookie) => cookie?.value)).size).toBe(3);
    await pages[1].reload();
    await expect(pages[1].getByLabel('斗地主牌桌')).toBeVisible();
    await expect(pages[1].getByText(`房间号 ${roomCode}`, { exact: true })).toBeVisible();
  } finally {
    await Promise.all(contexts.map((context) => context.close()));
  }
});
