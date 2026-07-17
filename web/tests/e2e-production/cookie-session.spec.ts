import { expect, test, type APIRequestContext, type BrowserContext, type Locator, type Page } from '@playwright/test';
import { decodeMessage } from '../../src/protocol/codec';
import { MsgType } from '../../src/protocol/types';

const baseURL = process.env.E2E_BASE_URL ?? 'https://127.0.0.1:1783';
const sessionCookieName = 'ddz_web_session';

test('HttpOnly cookie survives refresh and two page-close recoveries, then logout revokes it', async ({ browser, context, page, request }) => {
  expect(new URL(baseURL).protocol).toBe('https:');

  const initialIdentity = observePlayerIdentity(page);
  await page.goto('/');
  await expectConnectedLobby(page);
  const originalPlayerID = await expectObservedPlayerID(initialIdentity);

  const originalCookie = await expectSessionCookie(context);
  expect(originalCookie.httpOnly).toBe(true);
  expect(originalCookie.secure).toBe(true);
  expect(originalCookie.sameSite).toBe('Strict');
  expect(originalCookie.path).toBe('/');
  expect(originalCookie.expires).toBeGreaterThan(Date.now() / 1000 + 6 * 24 * 60 * 60);
  expect(await page.evaluate((name) => document.cookie.includes(`${name}=`), sessionCookieName)).toBe(false);

  await pressButton(page.getByRole('button', { name: /创建房间/ }));
  const roomCode = (await page.locator('.room-code-panel strong').textContent())?.trim();
  expect(roomCode).toMatch(/^\d{6}$/);

  // Keep a second participant online because empty waiting rooms are removed
  // immediately by the production room lifecycle.
  const keeperContext = await browser.newContext({ baseURL, ignoreHTTPSErrors: true });
  try {
    const keeperPage = await keeperContext.newPage();
    await keeperPage.goto('/');
    await expectConnectedLobby(keeperPage);
    await keeperPage.getByRole('textbox', { name: '加入房间' }).fill(roomCode!);
    await pressButton(keeperPage.getByRole('button', { name: '加入', exact: true }));
    await expectRecoveredRoom(keeperPage, roomCode!);

    await page.reload({ waitUntil: 'domcontentloaded' });
    await expectRecoveredRoom(page, roomCode!);

    page = await recoverAfterPageClose(context, page, request, roomCode!);
    page = await recoverAfterPageClose(context, page, request, roomCode!);

    await pressButton(page.getByRole('button', { name: '离开房间', exact: true }));
    await expectConnectedLobby(page);

    const postLogoutIdentity = observePlayerIdentity(page);
    const cookieBeforeLogout = await expectSessionCookie(context);
    const revokeResponse = page.waitForResponse((response) => (
      response.request().method() === 'POST'
      && new URL(response.url()).pathname === '/session/revoke'
    ));
    await pressButton(page.getByRole('button', { name: '退出并撤销本机会话' }));
    expect((await revokeResponse).status()).toBe(204);
    await expectConnectedLobby(page);
    expect(await expectObservedPlayerID(postLogoutIdentity)).not.toBe(originalPlayerID);
    await expect.poll(async () => (await expectSessionCookie(context)).value).not.toBe(cookieBeforeLogout.value);
    await expect(page.locator('.room-code-panel strong')).toHaveCount(0);

    const replayContext = await browser.newContext({ baseURL, ignoreHTTPSErrors: true });
    try {
      await replayContext.addCookies([{
        name: sessionCookieName,
        value: cookieBeforeLogout.value,
        url: baseURL,
        httpOnly: true,
        secure: true,
        sameSite: 'Strict'
      }]);
      const replayPage = await replayContext.newPage();
      const replayIdentity = observePlayerIdentity(replayPage);
      await replayPage.goto('/');
      await expectConnectedLobby(replayPage);
      expect(await expectObservedPlayerID(replayIdentity)).not.toBe(originalPlayerID);
      await expect(replayPage.locator('.room-code-panel strong')).toHaveCount(0);
      await expect.poll(async () => (await expectSessionCookie(replayContext)).value).not.toBe(cookieBeforeLogout.value);
    } finally {
      await replayContext.close();
    }
  } finally {
    await keeperContext.close();
  }
});

async function expectConnectedLobby(page: Page): Promise<void> {
  await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
  await expect(page.getByRole('status')).toHaveCount(0);
}

async function expectRecoveredRoom(page: Page, roomCode: string): Promise<void> {
  await expect(page.locator('.room-code-panel strong')).toHaveText(roomCode);
  await expect(page.getByRole('status')).toHaveCount(0);
}

async function expectSessionCookie(context: BrowserContext) {
  let cookie = (await context.cookies(baseURL)).find(({ name }) => name === sessionCookieName);
  await expect.poll(async () => {
    cookie = (await context.cookies(baseURL)).find(({ name }) => name === sessionCookieName);
    return cookie?.value.length ?? 0;
  }).toBe(64);
  if (!cookie) throw new Error('HttpOnly browser session cookie was not established');
  return cookie;
}

async function recoverAfterPageClose(
  context: BrowserContext,
  page: Page,
  request: APIRequestContext,
  roomCode: string
): Promise<Page> {
  const connectedBeforeClose = await websocketConnectionCount(request);
  expect(connectedBeforeClose).toBeGreaterThan(0);
  await page.close();
  await expect.poll(() => websocketConnectionCount(request)).toBeLessThan(connectedBeforeClose);

  const recoveredPage = await context.newPage();
  await recoveredPage.goto('/');
  await expectRecoveredRoom(recoveredPage, roomCode);
  return recoveredPage;
}

async function websocketConnectionCount(request: APIRequestContext): Promise<number> {
  const response = await request.get('/metrics');
  expect(response.status()).toBe(200);
  const match = /^fight_landlord_websocket_connections_current\s+([0-9.]+)$/m.exec(await response.text());
  if (!match) throw new Error('websocket connection gauge is missing from /metrics');
  return Number(match[1]);
}

async function pressButton(button: Locator): Promise<void> {
  await button.focus();
  await button.press('Enter');
}

interface PlayerIdentityObserver {
  currentPlayerID: () => string;
}

function observePlayerIdentity(page: Page): PlayerIdentityObserver {
  let currentPlayerID = '';
  page.on('websocket', (socket) => {
    socket.on('framereceived', ({ payload }) => {
      if (typeof payload === 'string') return;
      const message = decodeMessage(payload);
      if (message.type !== MsgType.Connected && message.type !== MsgType.Reconnected) return;
      const playerID = (message.payload as { player_id?: string }).player_id;
      if (playerID) currentPlayerID = playerID;
    });
  });
  return { currentPlayerID: () => currentPlayerID };
}

async function expectObservedPlayerID(observer: PlayerIdentityObserver): Promise<string> {
  await expect.poll(observer.currentPlayerID).not.toBe('');
  return observer.currentPlayerID();
}
