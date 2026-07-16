import { spawnSync } from 'node:child_process';
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';

const composeProject = process.env.E2E_COMPOSE_PROJECT_NAME;
const composeFile = process.env.E2E_COMPOSE_FILE;
const redisService = process.env.E2E_REDIS_SERVICE ?? 'redis';
const serverService = process.env.E2E_SERVER_SERVICE ?? 'poker-server';

test.describe.serial('production fault recovery', () => {
  test('queued matching is canceled explicitly and when the client disconnects', async ({ browser }, testInfo) => {
    const context = await browser.newContext({
      recordVideo: { dir: testInfo.outputPath('video'), size: { width: 640, height: 400 } }
    });
    let page = await context.newPage();

    try {
      await openLobby(page);
      await page.getByRole('button', { name: '快速开局', exact: true }).click();
      await expect(page.getByRole('heading', { name: '正在寻找牌友' })).toBeVisible();
      await page.getByRole('button', { name: '取消匹配', exact: true }).click();
      await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();

      await page.getByRole('button', { name: '快速开局', exact: true }).click();
      await expect(page.getByRole('heading', { name: '正在寻找牌友' })).toBeVisible();
      await page.close();

      page = await context.newPage();
      await openLobby(page);
      await expect(page.getByRole('heading', { name: '正在寻找牌友' })).toHaveCount(0);

      await page.getByRole('button', { name: /创建房间/ }).click();
      await expect(page.locator('.room-code-panel strong')).toHaveText(/^\d{6}$/);
      await page.getByRole('button', { name: '离开房间', exact: true }).click();
      await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test('game survives disconnects during deal and landlord selection', async ({ browser }, testInfo) => {
    const { contexts, pages } = await createRoom(browser, testInfo.outputPath('video'));

    try {
      await pages[1].getByRole('button', { name: '准备开始', exact: true }).click();
      await pages[2].getByRole('button', { name: '准备开始', exact: true }).click();
      await pages[2].close();
      await pages[0].getByRole('button', { name: '准备开始', exact: true }).click();

      await Promise.all(pages.slice(0, 2).map((page) => expect(page.getByLabel('叫地主操作')).toBeVisible()));
      pages[2] = await reconnectPage(contexts[2]);
      await expect(pages[2].getByLabel('叫地主操作')).toBeVisible();

      const landlord = await clickEnabled(pages, /^(叫地主|抢地主)$/);
      const landlordIndex = pages.indexOf(landlord);
      expect(landlordIndex).toBeGreaterThanOrEqual(0);
      await landlord.close();

      await clickEnabled(pages, /^(不叫|不抢)$/);
      await clickEnabled(pages, /^(不叫|不抢)$/);
      await Promise.all(pages.filter((page) => !page.isClosed())
        .map((page) => expect(page.getByLabel('出牌操作')).toBeVisible()));

      pages[landlordIndex] = await reconnectPage(contexts[landlordIndex]);
      await expect(pages[landlordIndex].getByLabel('出牌操作')).toBeVisible();
      await driveToGameOver(pages);
      await Promise.all(pages.map((page) => expect(page.locator('.result-panel')).toContainText(/获胜/)));
      await returnAllToLobby(pages);
    } finally {
      for (const context of contexts) await context.close();
    }
  });

  test('SIGTERM performs a graceful shutdown and clients reconnect after restart', async ({ browser }, testInfo) => {
    requireComposeControl();
    const context = await browser.newContext({
      recordVideo: { dir: testInfo.outputPath('video'), size: { width: 640, height: 400 } }
    });
    const page = await context.newPage();

    try {
      await openLobby(page);
      runCompose(['stop', '--timeout', '90', serverService]);

      await expect.poll(() => endpointStatus('/livez'), { timeout: 15_000 }).toBe(0);
      await expect(page.getByRole('status')).toBeVisible();
      expect(runCompose(['logs', '--no-color', serverService])).toContain('服务器已关闭');

      runCompose(['up', '-d', '--wait', serverService]);
      await expect.poll(() => endpointStatus('/readyz'), { timeout: 30_000 }).toBe(200);
      await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible({ timeout: 30_000 });
      await expect(page.getByRole('status')).toContainText('已作为新玩家连接');
    } finally {
      await context.close();
      ensureServerRunning();
    }
  });

  test('a temporary Redis outage changes readiness without dropping the game socket', async ({ browser, request }, testInfo) => {
    requireComposeControl();
    const context = await browser.newContext({
      recordVideo: { dir: testInfo.outputPath('video'), size: { width: 640, height: 400 } }
    });
    const page = await context.newPage();
    let paused = false;

    try {
      await openLobby(page);
      runCompose(['pause', redisService]);
      paused = true;

      await expect.poll(async () => (await request.get('/readyz')).status(), { timeout: 15_000 }).toBe(503);
      expect((await request.get('/livez')).status()).toBe(200);
      await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
      await expect(page.getByRole('status')).toHaveCount(0);

      runCompose(['unpause', redisService]);
      paused = false;
      await expect.poll(async () => (await request.get('/readyz')).status(), { timeout: 15_000 }).toBe(200);
    } finally {
      if (paused) runCompose(['unpause', redisService]);
      await context.close();
    }
  });
});

async function openLobby(page: Page): Promise<void> {
  await page.goto('/');
  await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
  await expect(page.getByRole('status')).toHaveCount(0);
}

async function createRoom(browser: Browser, videoDir: string): Promise<{ contexts: BrowserContext[]; pages: Page[] }> {
  const contexts = await Promise.all(Array.from({ length: 3 }, (_, index) => browser.newContext({
    recordVideo: { dir: `${videoDir}-${index}`, size: { width: 640, height: 400 } }
  })));
  const pages = await Promise.all(contexts.map((context) => context.newPage()));
  await Promise.all(pages.map(openLobby));

  await pages[0].getByRole('button', { name: /创建房间/ }).click();
  await expect(pages[0].locator('.room-code-panel strong')).toHaveText(/^\d{6}$/);
  const roomCode = (await pages[0].locator('.room-code-panel strong').textContent())!.trim();
  for (const page of pages.slice(1)) {
    await page.getByRole('textbox', { name: '加入房间' }).fill(roomCode);
    await page.getByRole('button', { name: '加入', exact: true }).click();
  }
  await Promise.all(pages.map((page) => expect(page.locator('.room-code-panel strong')).toHaveText(roomCode)));
  return { contexts, pages };
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

async function driveToGameOver(pages: Page[]): Promise<void> {
  for (let turn = 0; turn < 180; turn += 1) {
    if (await anyResultVisible(pages)) return;
    const actor = await pageWithEnabled(pages, '提示');
    const hint = actor.getByRole('button', { name: '提示', exact: true });
    try {
      await hint.click({ timeout: 3_000 });
    } catch (error: unknown) {
      if (await anyResultVisible(pages)) return;
      if (!await hint.isEnabled().catch(() => false)) continue;
      throw error;
    }
    const play = actor.getByRole('button', { name: '出牌', exact: true });
    if (await play.isEnabled().catch(() => false)) {
      try {
        await play.click({ timeout: 3_000 });
      } catch (error: unknown) {
        if (await anyResultVisible(pages)) return;
        if (!await play.isEnabled().catch(() => false)) continue;
        throw error;
      }
    } else {
      const pass = actor.getByRole('button', { name: '不出', exact: true });
      await expect(pass).toBeEnabled();
      try {
        await pass.click({ timeout: 3_000 });
      } catch (error: unknown) {
        if (await anyResultVisible(pages)) return;
        if (!await pass.isEnabled().catch(() => false)) continue;
        throw error;
      }
    }
  }
  throw new Error('The production game did not settle within 180 turns');
}

async function anyResultVisible(pages: Page[]): Promise<boolean> {
  for (const page of pages) {
    if (await page.getByRole('button', { name: '返回大厅', exact: true }).isVisible().catch(() => false)) return true;
  }
  return false;
}

async function returnAllToLobby(pages: Page[]): Promise<void> {
  for (const page of pages) {
    await page.getByRole('button', { name: '返回大厅', exact: true }).click();
  }
  await Promise.all(pages.map((page) => expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible()));
}

async function reconnectPage(context: BrowserContext): Promise<Page> {
  const page = await context.newPage();
  await page.goto('/');
  return page;
}

function requireComposeControl(): void {
  test.skip(!composeProject || !composeFile, 'Compose control environment is required for this production fault test');
}

function runCompose(args: string[]): string {
  if (!composeProject || !composeFile) throw new Error('Compose control environment is not configured');
  const command = ['compose', '--project-name', composeProject, '--file', composeFile, ...args];
  const result = spawnSync('docker', command, { encoding: 'utf8', env: process.env });
  if (result.error) throw result.error;
  expect(result.status, `docker ${command.join(' ')}\n${result.stderr}`).toBe(0);
  return `${result.stdout}\n${result.stderr}`.trim();
}

function ensureServerRunning(): void {
  if (!composeProject || !composeFile) return;
  runCompose(['up', '-d', '--wait', serverService]);
}

async function endpointStatus(path: string): Promise<number> {
  const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:1782';
  try {
    const response = await fetch(new URL(path, baseURL), { signal: AbortSignal.timeout(1_000) });
    return response.status;
  } catch {
    return 0;
  }
}
