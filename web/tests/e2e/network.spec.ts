import { expect, test } from '@playwright/test';

// Deterministic WebSocket fault injection: TCP packet loss manifests as delayed
// frames or a stalled connection, never unordered application messages.
const scenarios = [
  { rtt: 500, storageFailure: false },
  { rtt: 2000, storageFailure: false },
  { rtt: 500, storageFailure: true },
];
for (const { rtt, storageFailure } of scenarios) test(`submission recovery with ${rtt}ms RTT, storage failure=${storageFailure}`, async ({ page }) => {
  await page.goto('/?demo=table');
  await page.evaluate(`(async () => {
    const { GameSocket } = await import('/src/transport/wsClient.ts');
    const { encodeMessage, decodeMessage } = await import('/src/protocol/codec.ts');
    const { useAppStore } = await import('/src/stores/appStore.ts');
    class Transport {
      static OPEN = 1;
      readyState = 1;
      constructor() { window.transport = this; }
      send(data) {
        const msg = decodeMessage(data);
        if (msg.type === 'ping') setTimeout(() => this.receive('pong', { client_timestamp: msg.payload.timestamp, server_timestamp: Date.now() - ${rtt / 2} }), ${rtt});
        if (msg.type === 'bid') window.submissions++;
      }
      receive(type, payload) { this.onmessage?.({ data: encodeMessage(type, payload) }); }
      close() {}
    }
    window.WebSocket = Transport;
    window.submissions = 0;
    window.networkSocket = new GameSocket('ws://test/ws');
    window.networkSocket.connect();
    window.transport.onopen();
    window.transport.receive('connected', { player_id: 'me' });
    window.transport.receive('reconnected', { player_id: 'me' });
    if (${storageFailure}) Storage.prototype.setItem = () => { throw new DOMException('full', 'QuotaExceededError'); };
    useAppStore.setState({ currentTurn: 'me', bidToken: 'token', timeout: 15, timerStart: Date.now() });
    window.networkSocket.send('bid', { bid: true, bid_token: 'token' });
    window.networkSocket.send('bid', { bid: true, bid_token: 'token' });
  })()`);
  await expect(page.getByText('操作已提交，等待服务器确认…', { exact: true })).toBeVisible();
  expect(await page.evaluate('window.submissions')).toBe(1);
  await page.evaluate('window.transport.onclose({ code: 1006 })');
  await expect(page.locator('.submission-notice')).toContainText('操作结果待核对');
  await page.waitForTimeout(2600);
  await page.evaluate(`window.transport.onopen(); window.transport.receive('connected', { player_id: 'me' });`);
  await expect(page.getByText('正在核对上次操作，请勿重复提交', { exact: true })).toBeVisible();
  await page.evaluate(`window.transport.receive('reconnected', { player_id: 'me', game_state: { phase: 'playing', last_action_token: 'token', hand: [{ rank: 3 }], players: [] } });`);
  await expect(page.getByText('服务器已确认操作成功', { exact: true })).toBeVisible();
  expect(await page.evaluate('window.submissions')).toBe(1);
  await page.evaluate('window.networkSocket.close()');
});
