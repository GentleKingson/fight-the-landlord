import { expect, test, type BrowserContext, type Page } from '@playwright/test';

test('three browser clients complete a real authoritative game', async ({ browser }) => {
  const contexts = await Promise.all(Array.from({ length: 3 }, () => browser.newContext()));
  const pages = await Promise.all(contexts.map((context) => context.newPage()));

  try {
    await Promise.all(pages.map(async (page) => {
      await page.goto('/');
      await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
    }));

    await pages[0].getByRole('button', { name: /创建房间/ }).click();
    const roomCode = (await pages[0].locator('.room-code-panel strong').textContent())?.trim();
    expect(roomCode).toMatch(/^\d{6}$/);

    for (const page of pages.slice(1)) {
      await page.getByRole('textbox', { name: '加入房间' }).fill(roomCode!);
      await page.getByRole('button', { name: '加入', exact: true }).click();
    }
    await Promise.all(pages.map((page) => expect(page.locator('.room-code-panel strong')).toHaveText(roomCode!)));

    for (const page of pages) {
      await page.getByRole('button', { name: '准备开始', exact: true }).click();
    }
    await Promise.all(pages.map((page) => expect(page.getByLabel('叫地主操作')).toBeVisible()));

    await clickEnabled(pages, /^(叫地主|抢地主)$/);
    await clickEnabled(pages, /^不抢$/);
    await clickEnabled(pages, /^不抢$/);
    await Promise.all(pages.map((page) => expect(page.getByLabel('出牌操作')).toBeVisible()));

    const firstActor = await pageWithEnabled(pages, '提示');
    await firstActor.getByRole('button', { name: '提示', exact: true }).click();
    await expect(firstActor.getByRole('button', { name: '出牌', exact: true })).toBeEnabled();
    await firstActor.getByRole('button', { name: '出牌', exact: true }).click();

    const passer = await pageWithEnabled(pages, '不出');
    await passer.getByRole('button', { name: '不出', exact: true }).click();

    const chatText = `real-e2e-${Date.now()}`;
    await pages[0].getByRole('button', { name: '聊天', exact: true }).click();
    await pages[0].getByLabel('牌局聊天消息').fill(chatText);
    await pages[0].locator('.utility-drawer').getByRole('button', { name: '发送', exact: true }).click();
    await pages[1].getByRole('button', { name: '聊天', exact: true }).click();
    await expect(pages[1].locator('.utility-drawer')).toContainText(chatText);
    await pages[1].getByRole('button', { name: '关闭' }).click();

    await playSeveralTurns(pages, 3);
    pages[2] = await reconnectPage(contexts[2], pages[2]);
    await expect(pages[2].getByLabel('斗地主牌桌')).toBeVisible();

    await driveToGameOver(pages);
    const resultPage = await pageWithVisible(pages, '返回大厅');
    await expect(resultPage.locator('.result-panel')).toContainText(/获胜/);
    await resultPage.getByRole('button', { name: '返回大厅', exact: true }).click();
    await expect(resultPage.getByRole('button', { name: /创建房间/ })).toBeVisible();
  } finally {
    await Promise.all(contexts.map((context) => context.close()));
  }
});

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

async function pageWithVisible(pages: Page[], name: string): Promise<Page> {
  return pollPages(pages, async (page) => page.getByRole('button', { name, exact: true }).isVisible().catch(() => false), 120_000);
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
  throw new Error('The real game did not settle within 180 turns');
}

async function playCurrentTurn(pages: Page[]): Promise<void> {
  const actor = await pageWithEnabled(pages, '提示');
  await actor.getByRole('button', { name: '提示', exact: true }).click();
  const play = actor.getByRole('button', { name: '出牌', exact: true });
  if (await play.isEnabled().catch(() => false)) {
    await play.click();
    return;
  }
  const pass = actor.getByRole('button', { name: '不出', exact: true });
  await expect(pass).toBeEnabled();
  await pass.click();
}

async function anyResultVisible(pages: Page[]): Promise<boolean> {
  for (const page of pages) {
    if (await page.getByRole('button', { name: '返回大厅', exact: true }).isVisible().catch(() => false)) return true;
  }
  return false;
}

async function reconnectPage(context: BrowserContext, previous: Page): Promise<Page> {
  await previous.close();
  const page = await context.newPage();
  await page.goto('/');
  return page;
}
