import { expect, test, type BrowserContext, type Page } from '@playwright/test';

test('three final-image clients complete two games with repeat reconnects and settlement restore', async ({ browser }, testInfo) => {
  const contexts = await Promise.all(Array.from({ length: 3 }, (_, index) => browser.newContext({
    recordVideo: { dir: `${testInfo.outputPath('video')}-${index}`, size: { width: 640, height: 400 } }
  })));
  const pages = await Promise.all(contexts.map((context) => context.newPage()));

  try {
    await Promise.all(pages.map(openLobby));
    await createAndJoinRoom(pages);
    await startGame(pages);

    const firstActor = await pageWithEnabled(pages, '提示');
    await firstActor.getByRole('button', { name: '提示', exact: true }).click();
    await expect(firstActor.getByRole('button', { name: '出牌', exact: true })).toBeEnabled();
    await firstActor.getByRole('button', { name: '出牌', exact: true }).click();

    const passer = await pageWithEnabled(pages, '不出');
    await passer.getByRole('button', { name: '不出', exact: true }).click();

    const chatText = `production-e2e-${Date.now()}`;
    await pages[0].getByRole('button', { name: '聊天', exact: true }).click();
    await pages[0].getByLabel('牌局聊天消息').fill(chatText);
    await pages[0].locator('.utility-drawer').getByRole('button', { name: '发送', exact: true }).click();
    await pages[1].getByRole('button', { name: '聊天', exact: true }).click();
    await expect(pages[1].locator('.utility-drawer')).toContainText(chatText);
    await pages[1].getByRole('button', { name: '关闭' }).click();

    await playSeveralTurns(pages, 3);
    pages[2] = await reconnectPage(contexts[2], pages[2]);
    await expect(pages[2].getByLabel('斗地主牌桌')).toBeVisible();
    await playSeveralTurns(pages, 2);
    pages[2] = await reconnectPage(contexts[2], pages[2]);
    await expect(pages[2].getByLabel('斗地主牌桌')).toBeVisible();

    await driveToGameOver(pages);
    await assertSharedSettlement(pages);
    const firstSettlement = await readSettlement(pages[0]);

    await pages[0].reload();
    await expect(pages[0].locator('.result-panel')).toContainText(/获胜/);
    expect(await readSettlement(pages[0])).toEqual(firstSettlement);

    for (let index = 0; index < pages.length; index += 1) {
      pages[index] = await reconnectPage(contexts[index], pages[index]);
      await expect(pages[index].locator('.result-panel')).toContainText(/获胜/);
      await expect(pages[index].getByRole('alert')).toHaveCount(0);
      expect(await readSettlement(pages[index])).toEqual(firstSettlement);
    }

    for (const page of pages) {
      await page.getByRole('button', { name: '再来一局', exact: true }).click();
    }
    await Promise.all(pages.map((page) => expect(page.getByLabel('叫地主操作')).toBeVisible()));
    await chooseLandlord(pages);
    await Promise.all(pages.map((page) => expect(page.getByLabel('出牌操作')).toBeVisible()));

    await driveToGameOver(pages);
    await assertSharedSettlement(pages);
    for (const page of pages) {
      await page.getByRole('button', { name: '返回大厅', exact: true }).click();
    }
    await Promise.all(pages.map(async (page) => {
      await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
      await expect(page.locator('.room-code-panel')).toHaveCount(0);
      await expect(page.getByRole('status')).toHaveCount(0);
    }));
  } finally {
    for (const context of contexts) await context.close();
  }
});

async function openLobby(page: Page): Promise<void> {
  await page.goto('/');
  await expect(page).toHaveTitle('斗地主');
  await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
  await expect(page.getByRole('status')).toHaveCount(0);
}

async function createAndJoinRoom(pages: Page[]): Promise<void> {
  await pages[0].getByRole('button', { name: /创建房间/ }).click();
  await expect(pages[0].locator('.room-code-panel strong')).toHaveText(/^\d{6}$/);
  const roomCode = (await pages[0].locator('.room-code-panel strong').textContent())!.trim();
  for (const page of pages.slice(1)) {
    await page.getByRole('textbox', { name: '加入房间' }).fill(roomCode);
    await page.getByRole('button', { name: '加入', exact: true }).click();
  }
  await Promise.all(pages.map((page) => expect(page.locator('.room-code-panel strong')).toHaveText(roomCode)));
}

async function startGame(pages: Page[]): Promise<void> {
  for (const page of pages) {
    await page.getByRole('button', { name: '准备开始', exact: true }).click();
  }
  await Promise.all(pages.map((page) => expect(page.getByLabel('叫地主操作')).toBeVisible()));
  await chooseLandlord(pages);
  await Promise.all(pages.map((page) => expect(page.getByLabel('出牌操作')).toBeVisible()));
}

async function chooseLandlord(pages: Page[]): Promise<void> {
  await clickEnabled(pages, /^(叫地主|抢地主)$/);
  await clickEnabled(pages, /^(不叫|不抢)$/);
  await clickEnabled(pages, /^(不叫|不抢)$/);
}

async function clickEnabled(pages: Page[], name: RegExp): Promise<Page> {
  return pollPages(pages, async (page) => {
    const button = page.getByRole('button', { name }).first();
    if (!await button.isVisible().catch(() => false) || !await button.isEnabled().catch(() => false)) return false;
    await button.click();
    return true;
  });
}

async function pageWithEnabled(pages: Page[], name: string): Promise<Page> {
  return pollPages(pages, async (page) => {
    const button = page.getByRole('button', { name, exact: true });
    return await button.isVisible().catch(() => false) && await button.isEnabled().catch(() => false);
  });
}

async function pollPages(
  pages: Page[],
  predicate: (page: Page) => Promise<boolean>,
  timeout = 15_000
): Promise<Page> {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    for (const page of pages) {
      if (!page.isClosed() && await predicate(page)) return page;
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error('No browser page reached the expected authoritative state');
}

async function playSeveralTurns(pages: Page[], count: number): Promise<void> {
  for (let turn = 0; turn < count; turn += 1) {
    if (await anyResultVisible(pages)) return;
    await playCurrentTurn(pages);
  }
}

async function driveToGameOver(pages: Page[]): Promise<void> {
  for (let turn = 0; turn < 180; turn += 1) {
    if (await anyResultVisible(pages)) return;
    await playCurrentTurn(pages);
  }
  throw new Error('The production game did not settle within 180 turns');
}

async function playCurrentTurn(pages: Page[]): Promise<void> {
  const actor = await pageWithEnabled(pages, '提示');
  const hint = actor.getByRole('button', { name: '提示', exact: true });
  try {
    await hint.click({ timeout: 3_000 });
  } catch (error: unknown) {
    if (await anyResultVisible(pages)) return;
    if (!await hint.isEnabled().catch(() => false)) return;
    throw error;
  }
  const play = actor.getByRole('button', { name: '出牌', exact: true });
  if (await play.isEnabled().catch(() => false)) {
    try {
      await play.click({ timeout: 3_000 });
    } catch (error: unknown) {
      if (await anyResultVisible(pages)) return;
      if (!await play.isEnabled().catch(() => false)) return;
      throw error;
    }
    return;
  }
  const pass = actor.getByRole('button', { name: '不出', exact: true });
  await expect(pass).toBeEnabled();
  try {
    await pass.click({ timeout: 3_000 });
  } catch (error: unknown) {
    if (await anyResultVisible(pages)) return;
    if (!await pass.isEnabled().catch(() => false)) return;
    throw error;
  }
}

async function anyResultVisible(pages: Page[]): Promise<boolean> {
  for (const page of pages) {
    if (await page.getByRole('button', { name: '返回大厅', exact: true }).isVisible().catch(() => false)) return true;
  }
  return false;
}

async function assertSharedSettlement(pages: Page[]): Promise<void> {
  await Promise.all(pages.map(async (page) => {
    await expect(page.locator('.result-panel')).toContainText(/获胜/);
    await expect(page.getByRole('alert')).toHaveCount(0);
  }));
  const expected = await readSettlement(pages[0]);
  await Promise.all(pages.slice(1).map(async (page) => expect(await readSettlement(page)).toEqual(expected)));
}

async function readSettlement(page: Page): Promise<string> {
  const result = page.locator('.result-panel');
  const parts = await Promise.all([
    result.locator('.result-badge').textContent(),
    result.locator('h1').textContent(),
    result.locator('p').first().textContent(),
    result.locator('.score-list').textContent(),
    result.locator('.remaining-hands').textContent()
  ]);
  return parts.map((part) => part?.replace(/\s+/g, ' ').trim() ?? '').join('|');
}

async function reconnectPage(context: BrowserContext, previous: Page): Promise<Page> {
  await previous.close();
  const page = await context.newPage();
  await page.goto('/');
  return page;
}
