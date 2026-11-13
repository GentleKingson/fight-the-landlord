import { expect, test } from '@playwright/test';

test('small hands use one row and submission feedback never resizes the table', async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: { collect_training_data: true } }));
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=table');
  await page.evaluate(`import('/src/stores/appStore.ts').then(({useAppStore}) => useAppStore.setState({hand: [15,14,12,12,3].map((rank,i)=>({rank,suit:i%4,color:i%2})), selectedCards:new Set()}))`);
  const cards = page.getByLabel('手牌').getByRole('option');
  await expect(cards).toHaveCount(5);
  const tops = await cards.evaluateAll(elements => elements.map(el => el.getBoundingClientRect().top));
  expect(new Set(tops).size).toBe(1);
  for (const card of await cards.all()) await expect(card).toBeInViewport({ ratio: 1 });
  const zones = page.locator('.table-brand:visible, .table-score-strip:visible, .table-tools:visible');
  const boxes = await zones.evaluateAll(elements => elements.map(el => el.getBoundingClientRect().toJSON()));
  for (let i = 1; i < boxes.length; i++) expect(boxes[i - 1].right).toBeLessThanOrEqual(boxes[i].left);
  const before = await page.locator('.hand').boundingBox();
  await page.evaluate(`import('/src/stores/appStore.ts').then(({useAppStore}) => useAppStore.setState({submission:'pending',submissionMessage:'操作已提交，等待服务器确认…'}))`);
  await expect(page.getByRole('status').filter({hasText:'操作已提交'})).toBeVisible();
  expect(await page.locator('.hand').boundingBox()).toEqual(before);
});

test('background music plays after a gesture and follows mute and settlement', async ({ page }) => {
  await page.addInitScript(() => {
    const Original = window.Audio;
    (window as any).musicElements = [];
    window.Audio = function(src?: string) {
      const audio = new Original(src);
      (window as any).musicElements.push(audio);
      return audio;
    } as unknown as typeof Audio;
  });
  await page.goto('/?demo=table');
  await page.getByRole('button', {name:'聊天', exact:true}).click();
  await page.waitForFunction(() => (window as any).musicElements.some((el: HTMLAudioElement) => !el.paused && el.currentTime > 0));
  await page.getByRole('button', {name:'关闭', exact:true}).click();
  await page.getByRole('button', {name:'关闭音效'}).click();
  await expect.poll(() => page.evaluate(() => (window as any).musicElements.every((el: HTMLAudioElement) => el.paused))).toBe(true);
  await page.getByRole('button', {name:'开启音效'}).click();
  await page.waitForFunction(() => (window as any).musicElements.some((el: HTMLAudioElement) => !el.paused));
  await page.evaluate(`import('/src/stores/appStore.ts').then(({useAppStore}) => useAppStore.setState({phase:'game_over'}))`);
  await expect.poll(() => page.evaluate(() => (window as any).musicElements.every((el: HTMLAudioElement) => el.paused))).toBe(true);
});

test('dense hands expose every complete rank and suit corner', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=table');
  await page.evaluate(`import('/src/stores/appStore.ts').then(({useAppStore}) => useAppStore.setState({hand: Array.from({length:20},(_,i)=>({rank:12-Math.floor(i/4),suit:i%4,color:i%2})), selectedCards:new Set()}))`);
  await expect(page.getByLabel('手牌').getByRole('option')).toHaveCount(20);
  const hidden = await page.locator('.hand .card__index--top').evaluateAll(indices => indices.filter(index => {
    const rect = index.getBoundingClientRect();
    const card = index.closest('.card');
    return !card?.contains(document.elementFromPoint(rect.right - 1, rect.top + 2));
  }).map(index => index.textContent));
  expect(hidden).toEqual([]);
});

test('history drawer keeps white card faces and sorts composite plays', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/?demo=table');
  await page.evaluate(async () => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ path);
    const card = (suit: number, rank: number) => ({ suit, rank, color: suit === 1 || suit === 3 ? 1 : 0 });
    useAppStore.setState({
      recentActions: [{
        type: 'play',
        player_id: 'p3',
        player_name: '松风',
        hand_type: '三带一',
        cards: [card(1, 4), card(0, 9), card(2, 9), card(3, 9)],
      }],
    });
  });

  await page.getByRole('button', { name: '历史', exact: true }).click();
  const cards = page.locator('.history-cards .card');
  await expect(cards).toHaveCount(4);
  const ranks = await cards.locator('.card__index--top b').allTextContents();
  expect(ranks).toEqual(['9', '9', '9', '4']);
  const bgImages = await cards.evaluateAll(elements => elements.map(el => getComputedStyle(el).backgroundImage));
  for (const bg of bgImages) expect(bg).not.toBe('none');
  const containerBg = await page.locator('.history-cards').evaluate(el => getComputedStyle(el).backgroundImage);
  expect(containerBg).toBe('none');
});

test('portrait topbar stays non-overlapping across 361px-430px widths', async ({ page }) => {
  for (const viewport of [
    { width: 361, height: 640 },
    { width: 375, height: 667 },
    { width: 390, height: 844 },
    { width: 412, height: 915 },
    { width: 430, height: 932 },
  ]) {
    await page.setViewportSize(viewport);
    await page.goto('/?demo=table');
    await page.evaluate(async () => {
      const path = '/src/stores/appStore.ts';
      const { useAppStore } = await import(/* @vite-ignore */ path);
      useAppStore.setState({ multiplier: 192 });
    });
    const zones = page.locator('.table-brand:visible, .table-score-strip:visible, .table-tools:visible');
    const boxes = await zones.evaluateAll(elements => elements.map(el => el.getBoundingClientRect().toJSON()));
    expect(boxes).toHaveLength(3);
    for (const box of boxes) {
      expect(box.left).toBeGreaterThanOrEqual(0);
      expect(box.right).toBeLessThanOrEqual(viewport.width);
    }
    for (let i = 1; i < boxes.length; i++) {
      expect(boxes[i - 1].right).toBeLessThanOrEqual(boxes[i].left);
    }
    for (const button of await page.locator('.table-tools button').all()) {
      const box = (await button.boundingBox())!;
      expect(box.width).toBeGreaterThanOrEqual(43.99);
      expect(box.height).toBeGreaterThanOrEqual(43.99);
    }
  }
});

test('compact portrait avoids badge-vs-played collisions and lifts selected short-hand cards', async ({ page }) => {
  for (const viewport of [{ width: 320, height: 480 }, { width: 320, height: 568 }, { width: 360, height: 640 }]) {
    await page.setViewportSize(viewport);
    await page.goto('/?demo=table');
    await page.evaluate(async () => {
      const path = '/src/stores/appStore.ts';
      const { useAppStore } = await import(/* @vite-ignore */ path) as typeof import('../../src/stores/appStore');
      const card = (suit: number, rank: number) => ({ suit, rank, color: suit === 1 || suit === 3 ? 1 : 0 });
      const cards = Array.from({ length: 12 }, (_, i) => card(i % 4, 3 + Math.floor(i / 3)));
      const state = useAppStore.getState();
      useAppStore.setState({
        players: state.players.map(p => (p.id === 'p3' ? { ...p, online: false } : p)),
        hand: [15, 14, 12, 12, 3].map((rank, i) => card(i % 4, rank)),
        selectedCards: new Set(['0_15']),
        lastPlayed: cards,
        lastPlayedBy: 'p3',
        lastPlayedName: '松风',
        lastHandType: '飞机带翅膀',
        seatActions: {
          p3: { type: 'play', player_id: 'p3', player_name: '松风', cards, hand_type: '飞机带翅膀' },
          p2: { type: 'pass', player_id: 'p2', player_name: '山月', label: '不出' },
          p1: { type: 'play', player_id: 'p1', player_name: '青竹', cards: [card(0, 14), card(1, 14)], hand_type: '对子' },
        },
      });
    });

    const collisions = await page.evaluate(() => {
      const badges = [...document.querySelectorAll('.player-action, .player-offline')];
      const played = [...document.querySelectorAll('.felt-table .played-cards')];
      const out: string[] = [];
      for (const badge of badges) {
        const b = badge.getBoundingClientRect();
        for (const p of played) {
          const pr = p.getBoundingClientRect();
          if (!(b.right <= pr.left + 0.5 || pr.right <= b.left + 0.5 || b.bottom <= pr.top + 0.5 || pr.bottom <= b.top + 0.5)) {
            out.push(`${badge.className} hits ${p.className}`);
          }
        }
      }
      return out;
    });
    expect(collisions).toEqual([]);

    const selected = page.getByLabel('手牌').getByRole('option').first();
    const unselected = page.getByLabel('手牌').getByRole('option').nth(1);
    const selectedBox = (await selected.boundingBox())!;
    const unselectedBox = (await unselected.boundingBox())!;
    expect(unselectedBox.y - selectedBox.y).toBeGreaterThanOrEqual(12);
    await expect(selected).toBeInViewport({ ratio: 1 });
  }
});
