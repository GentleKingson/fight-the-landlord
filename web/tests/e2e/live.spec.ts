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
