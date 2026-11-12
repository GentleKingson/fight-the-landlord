import { expect, test, type Page, type WebSocketRoute } from '@playwright/test';
import protobuf from 'protobufjs';
import { fileURLToPath } from 'node:url';

const protocol = protobuf.loadSync(fileURLToPath(new URL('../../../internal/protocol/proto/message.proto', import.meta.url)));
const envelope = protocol.lookupType('protocol.Message');
const types = protocol.lookupEnum('protocol.MessageType').values;

// Real server integration. Only transport frames are interrupted; game state and
// acknowledgements always come from Go + Redis. Use the Vite URL for read-only state inspection.
test('three players preserve selection and reconcile interrupted submissions', async ({ browser }, testInfo) => {
  test.skip(!process.env.DDZ_LIVE_URL, 'Set DDZ_LIVE_URL to Vite proxying a real Go + Redis server');
  test.setTimeout(120_000);
  const contexts = await Promise.all([browser.newContext(), browser.newContext(), browser.newContext()]);
  const faults = contexts.map(() => ({
    offline: false, mode: '' as '' | 'drop-send' | 'drop-ack',
    client: null as WebSocketRoute | null, server: null as WebSocketRoute | null,
    playsSent: 0, playsForwarded: 0, droppedAcks: 0
  }));
  const cut = async (index: number) => {
    const fault = faults[index];
    fault.offline = true;
    await fault.client?.close({ code: 4000, reason: 'Integration test interruption' });
    await fault.server?.close();
  };
  const read = (page: Page) => page.evaluate(async () => {
    // @ts-expect-error Vite serves this module in the browser.
    const { useAppStore } = await import('/src/stores/appStore.ts');
    const s = useAppStore.getState();
    return { playerId: s.playerId, phase: s.phase, currentTurn: s.currentTurn,
      connected: s.connected, submission: s.submission, hand: s.hand,
      selected: [...s.selectedCards], lastPlayedBy: s.lastPlayedBy,
      roomCode: s.roomCode };
  });
  try {
    for (let index = 0; index < contexts.length; index++) {
      const fault = faults[index];
      await contexts[index].routeWebSocket('**/ws', async client => {
        fault.client = client;
        if (fault.offline) { await client.close({ code: 4000 }); return; }
        const server = client.connectToServer();
        fault.server = server;
        client.onMessage(async data => {
          const message = envelope.decode(data as Buffer) as unknown as { type: number };
          if (message.type === types.MSG_PLAY_CARDS) {
            fault.playsSent++;
            if (fault.mode === 'drop-send') {
              fault.mode = '';
              await cut(index);
              return;
            }
            fault.playsForwarded++;
          }
          server.send(data);
        });
        server.onMessage(async data => {
          const message = envelope.decode(data as Buffer) as unknown as { type: number };
          if (fault.mode === 'drop-ack' && message.type === types.MSG_CARD_PLAYED) {
            fault.mode = '';
            fault.droppedAcks++;
            await cut(index);
            return;
          }
          if (!fault.offline) client.send(data);
        });
      });
    }
    const pages = await Promise.all(contexts.map(c => c.newPage()));
    await Promise.all(pages.map(p => p.goto(process.env.DDZ_LIVE_URL!)));
    for (const p of pages) await expect.poll(async () => (await read(p)).phase).toBe('lobby');
    const identities = await Promise.all(pages.map(async p => (await read(p)).playerId));
    expect(new Set(identities).size).toBe(3);

    await test.step('disconnected lobby retains drafts and never enters false matching', async () => {
      await cut(0);
      await expect.poll(async () => (await read(pages[0])).connected).toBe(false);
      await pages[0].getByRole('button', { name: /快速开局/ }).click();
      await expect(pages[0].getByText('匹配请求发送失败，请等待连接恢复后重试')).toBeVisible();
      expect((await read(pages[0])).phase).toBe('lobby');
      await pages[0].getByRole('button', { name: '聊天', exact: true }).click();
      await pages[0].getByLabel('大厅聊天消息').fill('断线草稿');
      await pages[0].getByRole('button', { name: '发送', exact: true }).click();
      await expect(pages[0].getByLabel('大厅聊天消息')).toHaveValue('断线草稿');
      await expect(pages[0].getByText('聊天发送失败，草稿已保留，请等待连接恢复后重试')).toBeVisible();
      faults[0].offline = false;
      await expect.poll(async () => (await read(pages[0])).connected, { timeout: 15000 }).toBe(true);
      await pages[0].getByRole('button', { name: '发送', exact: true }).click();
      await expect(pages[0].getByLabel('大厅聊天消息')).toHaveValue('');
      await pages[1].getByRole('button', { name: '聊天', exact: true }).click();
      await expect(pages[1].locator('.chat-feed')).toContainText('断线草稿');
      for (const p of pages.slice(0, 2)) await p.getByRole('button', { name: '大厅', exact: true }).click();
    });

    await pages[0].getByRole('button', { name: /创建房间/ }).click();
    await expect(pages[0].locator('#waiting-room-code')).toHaveText(/^\d{6}$/);
    const code = (await pages[0].locator('#waiting-room-code').textContent())!;
    for (const p of pages.slice(1)) {
      await p.getByLabel('加入房间', { exact: true }).fill(code);
      await p.getByRole('button', { name: '加入', exact: true }).click();
      await expect(p.locator('#waiting-room-code')).toHaveText(code);
    }
    for (const p of pages) await expect(p.getByText('3/3 人已入座')).toBeVisible();
    for (const p of pages) await p.getByRole('button', { name: '准备开始' }).click();
    for (let step = 0; step < 8; step++) {
      await expect.poll(async () => (await read(pages[0])).phase).not.toBe('dealing');
      if ((await read(pages[0])).phase === 'playing') break;
      const state = await read(pages[0]);
      const current = pages[identities.indexOf(state.currentTurn)];
      const bid = current.getByRole('button', { name: /^(叫地主|抢地主)$/ });
      await expect(bid).toBeEnabled();
      await bid.click();
      await expect.poll(async () => {
        const next = await read(pages[0]);
        return next.phase === 'playing' || next.currentTurn !== state.currentTurn;
      }).toBe(true);
    }
    for (const p of pages) await expect.poll(async () => (await read(p)).phase).toBe('playing');
    const leader = identities.indexOf((await read(pages[0])).currentTurn);
    const other = (leader + 1) % 3;
    const firstCard = pages[other].getByLabel('手牌', { exact: true }).getByRole('option').first();
    await firstCard.press('Space');
    await expect(firstCard).toHaveAttribute('aria-selected', 'true');
    const selection = (await read(pages[other])).selected;
    await pages[leader].getByRole('button', { name: '提示', exact: true }).click();

    await test.step('request lost before reaching server is rejected on recovery without replay', async () => {
      const before = await read(pages[leader]);
      faults[leader].mode = 'drop-send';
      await pages[leader].getByRole('button', { name: '出牌', exact: true }).click();
      await expect(pages[leader].locator('.submission-notice')).toContainText('操作结果待核对');
      await expect(pages[leader].getByRole('button', { name: '出牌', exact: true })).toBeDisabled();
      faults[leader].offline = false;
      await expect(pages[leader].locator('.submission-notice')).toContainText('服务器未接受上次操作', { timeout: 15000 });
      expect((await read(pages[leader])).hand).toEqual(before.hand);
      expect(faults[leader].playsSent).toBe(1);
      expect(faults[leader].playsForwarded).toBe(0);
    });

    await test.step('committed play with lost acknowledgement is confirmed once after reconnect', async () => {
      await pages[leader].getByRole('button', { name: '提示', exact: true }).click();
      const before = await read(pages[leader]);
      faults[leader].mode = 'drop-ack';
      await pages[leader].getByRole('button', { name: '出牌', exact: true }).click();
      await expect(pages[leader].locator('.submission-notice')).toContainText('操作结果待核对');
      await expect.poll(async () => (await read(pages[other])).lastPlayedBy).toBe(identities[leader]);
      expect((await read(pages[other])).selected).toEqual(selection);
      await pages[leader].getByRole('button', { name: '聊天', exact: true }).click();
      await pages[leader].getByLabel('房间聊天消息').fill('房间断线草稿');
      await pages[leader].getByRole('button', { name: '发送', exact: true }).click();
      await expect(pages[leader].getByLabel('房间聊天消息')).toHaveValue('房间断线草稿');
      await expect(pages[leader].getByText('聊天发送失败，草稿已保留，请等待连接恢复后重试')).toBeVisible();
      faults[leader].offline = false;
      await expect(pages[leader].locator('.submission-notice')).toContainText('服务器已确认操作成功', { timeout: 15000 });
      expect((await read(pages[leader])).hand.length).toBe(before.hand.length - before.selected.length);
      expect(faults[leader].playsSent).toBe(2);
      expect(faults[leader].playsForwarded).toBe(1);
      expect(faults[leader].droppedAcks).toBe(1);
      await expect(pages[leader].locator('.submission-notice')).toHaveCount(0, { timeout: 5000 });
      await pages[leader].getByRole('button', { name: '发送', exact: true }).click();
      await expect(pages[leader].getByLabel('房间聊天消息')).toHaveValue('');
      await pages[leader].getByRole('button', { name: '关闭', exact: true }).click();
    });

    await test.step('offline peer is visible and rejoins the same three-person game', async () => {
      const state = await read(pages[other]);
      await cut(other);
      await expect(pages[leader].getByText('离线', { exact: true })).toHaveCount(1);
      faults[other].offline = false;
      await expect.poll(async () => (await read(pages[other])).connected, { timeout: 15000 }).toBe(true);
      await expect(pages[leader].getByText('离线', { exact: true })).toHaveCount(0);
      expect((await read(pages[other])).playerId).toBe(state.playerId);
      expect((await read(pages[other])).hand).toEqual(state.hand);
      expect((await read(pages[other])).roomCode).toBe(code);
      for (let index = 0; index < pages.length; index++) {
        await testInfo.attach('player-' + index, { body: await pages[index].screenshot(), contentType: 'image/png' });
      }
    });
  } finally {
    await Promise.all(contexts.map(c => c.close()));
  }
});
