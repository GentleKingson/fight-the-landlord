import { expect, test, type BrowserContext, type Locator, type Page } from '@playwright/test';

test('final image supports a real connection and card selection', async ({ browser }) => {
  const contexts: BrowserContext[] = [];
  const pages: Page[] = [];

  try {
    for (let index = 0; index < 3; index += 1) {
      const context = await browser.newContext();
      contexts.push(context);
      const page = await context.newPage();
      pages.push(page);
      await page.goto('/');
      await expect(page).toHaveTitle('斗地主');
      await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
      await expect(page.getByRole('status')).toHaveCount(0);
    }

    await pressButton(pages[0].getByRole('button', { name: /创建房间/ }));
    const roomCode = (await pages[0].locator('.room-code-panel strong').textContent())?.trim();
    expect(roomCode).toMatch(/^\d{6}$/);

    for (const page of pages.slice(1)) {
      await page.getByRole('textbox', { name: '加入房间' }).fill(roomCode!);
      await pressButton(page.getByRole('button', { name: '加入', exact: true }));
    }
    await Promise.all(pages.map((page) => expect(page.locator('.room-code-panel strong')).toHaveText(roomCode!)));

    for (const page of pages) {
      await pressButton(page.getByRole('button', { name: '准备开始', exact: true }));
    }
    await Promise.all(pages.map((page) => expect(page.getByLabel('叫地主操作')).toBeVisible()));

    await clickEnabled(pages, /^(叫地主|抢地主)$/);
    await clickEnabled(pages, /^(不叫|不抢)$/);
    await clickEnabled(pages, /^(不叫|不抢)$/);
    await Promise.all(pages.map((page) => expect(page.getByLabel('出牌操作')).toBeVisible()));

    const actor = await pageWithEnabled(pages, '提示');
    const firstCard = actor.getByLabel('手牌').getByRole('button').first();
    await expect(firstCard).toBeEnabled();
    await firstCard.focus();
    await firstCard.press('Enter');
    await expect(firstCard).toHaveAttribute('aria-pressed', 'true');
    await expect(actor.getByLabel('手牌').locator('.hand__slot.is-selected')).toHaveCount(1);
    await firstCard.press('Enter');

    await driveToGameOver(pages);
    await Promise.all(pages.map((page) => expect(page.locator('.result-panel')).toContainText(/获胜/)));
    for (const page of pages) {
      await pressButton(page.getByRole('button', { name: '返回大厅', exact: true }));
    }
    await Promise.all(pages.map((page) => expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible()));
  } finally {
    for (const context of contexts) await context.close();
  }
});

async function clickEnabled(pages: Page[], name: RegExp): Promise<void> {
  await pollPages(pages, async (page) => {
    const button = page.getByRole('button', { name }).first();
    if (!await button.isVisible().catch(() => false) || !await button.isEnabled().catch(() => false)) return false;
    await pressButton(button);
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

async function driveToGameOver(pages: Page[]): Promise<void> {
  for (let turn = 0; turn < 180; turn += 1) {
    if (await anyResultVisible(pages)) return;
    const actor = await pageWithEnabled(pages, '提示');
    const hint = actor.getByRole('button', { name: '提示', exact: true });
    try {
      await pressButton(hint);
    } catch (error: unknown) {
      if (await anyResultVisible(pages)) return;
      if (!await hint.isEnabled().catch(() => false)) continue;
      throw error;
    }
    const play = actor.getByRole('button', { name: '出牌', exact: true });
    if (await play.isEnabled().catch(() => false)) {
      try {
        await pressButton(play);
      } catch (error: unknown) {
        if (await anyResultVisible(pages)) return;
        if (!await play.isEnabled().catch(() => false)) continue;
        throw error;
      }
    } else {
      const pass = actor.getByRole('button', { name: '不出', exact: true });
      await expect(pass).toBeEnabled();
      try {
        await pressButton(pass);
      } catch (error: unknown) {
        if (await anyResultVisible(pages)) return;
        if (!await pass.isEnabled().catch(() => false)) continue;
        throw error;
      }
    }
  }
  throw new Error('The cross-browser smoke game did not settle within 180 turns');
}

async function anyResultVisible(pages: Page[]): Promise<boolean> {
  for (const page of pages) {
    if (await page.getByRole('button', { name: '返回大厅', exact: true }).isVisible().catch(() => false)) return true;
  }
  return false;
}

async function pressButton(button: Locator): Promise<void> {
  await button.focus({ timeout: 3_000 });
  await button.press('Enter', { timeout: 3_000 });
}
