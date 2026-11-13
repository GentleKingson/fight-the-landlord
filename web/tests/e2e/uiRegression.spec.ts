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
  await page.getByRole('button', {name:'关闭声音'}).click();
  await expect.poll(() => page.evaluate(() => (window as any).musicElements.every((el: HTMLAudioElement) => el.paused))).toBe(true);
  await page.getByRole('button', {name:'开启声音'}).click();
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

test('dynamic turn rotation keeps active opponent plays visible in portrait', async ({ page }) => {
  for (const viewport of [{ width: 390, height: 664 }, { width: 320, height: 568 }]) {
    await page.setViewportSize(viewport);
    await page.goto('/?demo=table');

    const rotateState = async (mode: 'only-left' | 'both' | 'right-only-left-pass' | 'left-only-right-pass') => {
      await page.evaluate(async (m) => {
        const path = '/src/stores/appStore.ts';
        const { useAppStore } = await import(/* @vite-ignore */ path) as typeof import('../../src/stores/appStore');
        const mkCards = (len: number) => Array.from({ length: len }, (_, i) => ({
          suit: i % 4,
          rank: 3 + Math.floor(i / 2),
          color: i % 2,
        }));
        const leftId = 'p3';
        const rightId = 'p2';
        if (m === 'only-left') {
          useAppStore.setState({
            mustPlay: false,
            lastPlayed: mkCards(4),
            lastPlayedBy: leftId,
            lastPlayedName: '松风',
            lastHandType: '三带一',
            seatActions: {
              [leftId]: { type: 'play', player_id: leftId, player_name: '松风', cards: mkCards(4), hand_type: '三带一' },
            },
          });
        } else if (m === 'both') {
          useAppStore.setState({
            mustPlay: false,
            lastPlayed: mkCards(4),
            lastPlayedBy: rightId,
            lastPlayedName: '山月',
            lastHandType: '三带一',
            seatActions: {
              [leftId]: { type: 'play', player_id: leftId, player_name: '松风', cards: mkCards(4), hand_type: '三带一' },
              [rightId]: { type: 'play', player_id: rightId, player_name: '山月', cards: mkCards(4), hand_type: '三带一' },
            },
          });
        } else if (m === 'right-only-left-pass') {
          useAppStore.setState({
            mustPlay: false,
            lastPlayed: mkCards(5),
            lastPlayedBy: rightId,
            lastPlayedName: '山月',
            lastHandType: '三带二',
            seatActions: {
              [leftId]: { type: 'pass', player_id: leftId, player_name: '松风', label: '不出' },
              [rightId]: { type: 'play', player_id: rightId, player_name: '山月', cards: mkCards(5), hand_type: '三带二' },
            },
          });
        } else {
          useAppStore.setState({
            mustPlay: false,
            lastPlayed: mkCards(6),
            lastPlayedBy: leftId,
            lastPlayedName: '松风',
            lastHandType: '顺子',
            seatActions: {
              [leftId]: { type: 'play', player_id: leftId, player_name: '松风', cards: mkCards(6), hand_type: '顺子' },
              [rightId]: { type: 'pass', player_id: rightId, player_name: '山月', label: '不出' },
            },
          });
        }
      }, mode);
    };

    await rotateState('only-left');
    expect((await page.locator('.played-seat--left .played-cards').boundingBox())!.width).toBeGreaterThan(100);
    await expect(page.locator('.played-seat--right')).toBeHidden();

    await rotateState('both');
    expect((await page.locator('.played-seat--left .played-cards').boundingBox())!.width).toBeGreaterThan(80);
    expect((await page.locator('.played-seat--right .played-cards').boundingBox())!.width).toBeGreaterThan(80);

    await rotateState('right-only-left-pass');
    await expect(page.locator('.played-seat--left')).toBeHidden();
    expect((await page.locator('.played-seat--right .played-cards').boundingBox())!.width).toBeGreaterThan(100);
    await expect(page.locator('.seat-panel--left .player-action')).toBeVisible();

    await rotateState('left-only-right-pass');
    expect((await page.locator('.played-seat--left .played-cards').boundingBox())!.width).toBeGreaterThan(100);
    await expect(page.locator('.played-seat--right')).toBeHidden();
  }
});

test('dense 20-card hand avoids collisions under collection notice and trick boundary', async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: { collect_training_data: true } }));
  for (const viewport of [
    { width: 320, height: 480 },
    { width: 320, height: 568 },
    { width: 360, height: 640 },
    { width: 568, height: 320 },
  ]) {
    await page.setViewportSize(viewport);
    await page.goto('/?demo=table');
    await expect(page.locator('.collection-notice')).toBeVisible();

    for (const mustPlay of [false, true]) {
      await page.evaluate(async (mp) => {
        const path = '/src/stores/appStore.ts';
        const { useAppStore } = await import(/* @vite-ignore */ path) as typeof import('../../src/stores/appStore');
        const card = (suit: number, rank: number) => ({ suit, rank, color: suit === 1 || suit === 3 ? 1 : 0 });
        const denseHand = Array.from({ length: 20 }, (_, i) => card(i % 4, 15 - Math.floor(i / 4)));
        const playedCards = Array.from({ length: 12 }, (_, i) => card(i % 4, 3 + Math.floor(i / 3)));
        const state = useAppStore.getState();
        useAppStore.setState({
          mustPlay: mp,
          players: state.players.map(p => (p.id === 'p3' ? { ...p, online: false } : p)),
          hand: denseHand,
          selectedCards: new Set(),
          lastPlayed: playedCards,
          lastPlayedBy: 'p3',
          lastPlayedName: '松风',
          lastHandType: '飞机带翅膀',
          seatActions: {
            p3: { type: 'play', player_id: 'p3', player_name: '松风', cards: playedCards, hand_type: '飞机带翅膀' },
            p2: { type: 'pass', player_id: 'p2', player_name: '山月', label: '不出' },
            p1: { type: 'play', player_id: 'p1', player_name: '青竹', cards: [card(0, 14), card(1, 14)], hand_type: '对子' },
          },
        });
      }, mustPlay);

      const layoutIssues = await page.evaluate(() => {
        const issues: string[] = [];
        const badges = [...document.querySelectorAll('.player-action, .player-offline')].filter(el => el.getBoundingClientRect().width > 0);
        const played = [...document.querySelectorAll('.felt-table .played-cards')].filter(el => el.getBoundingClientRect().width > 0);
        for (const badge of badges) {
          const b = badge.getBoundingClientRect();
          for (const p of played) {
            const pr = p.getBoundingClientRect();
            if (!(b.right <= pr.left + 0.5 || pr.right <= b.left + 0.5 || b.bottom <= pr.top + 0.5 || pr.bottom <= b.top + 0.5)) {
              issues.push(`badge ${badge.className} overlaps ${p.className}`);
            }
          }
        }
        const felt = document.querySelector('.felt-table')!.getBoundingClientRect();
        for (const p of played) {
          const pr = p.getBoundingClientRect();
          if (pr.top < felt.top - 1 || pr.bottom > felt.bottom + 1 || pr.left < felt.left - 1 || pr.right > felt.right + 1) {
            issues.push(`played outside felt: p=[${pr.top.toFixed(1)},${pr.bottom.toFixed(1)}] felt=[${felt.top.toFixed(1)},${felt.bottom.toFixed(1)}]`);
          }
        }
        const zones = [...document.querySelectorAll('.opponent-row, .turn-banner'), ...played].map(el => ({
          name: el.className,
          r: el.getBoundingClientRect(),
        }));
        for (let i = 0; i < zones.length; i++) {
          for (let j = i + 1; j < zones.length; j++) {
            const a = zones[i].r;
            const b = zones[j].r;
            if (!(a.bottom <= b.top + 0.5 || b.bottom <= a.top + 0.5 || a.right <= b.left + 0.5 || b.right <= a.left + 0.5)) {
              issues.push(`zone overlap: ${zones[i].name} vs ${zones[j].name}`);
            }
          }
        }
        if (document.documentElement.scrollWidth !== window.innerWidth || document.documentElement.scrollHeight !== window.innerHeight) {
          issues.push(`scroll mismatch: ${document.documentElement.scrollWidth}x${document.documentElement.scrollHeight} vs ${window.innerWidth}x${window.innerHeight}`);
        }
        return issues;
      });
      expect(layoutIssues, `${viewport.width}x${viewport.height} mustPlay=${mustPlay}`).toEqual([]);
    }
  }
});

test('played cards fit-content width and narrow metadata preserve player names', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto('/?demo=table');
  await page.evaluate(async () => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ path) as typeof import('../../src/stores/appStore');
    const pair = [{ suit: 0, rank: 14, color: 0 }, { suit: 1, rank: 14, color: 1 }];
    useAppStore.setState({
      mustPlay: false,
      lastPlayed: pair,
      lastPlayedBy: 'p3',
      seatActions: {
        p3: { type: 'play', player_id: 'p3', player_name: '松风', cards: pair, hand_type: '对子' },
      },
    });
  });
  const desktopWidths = await page.locator('.played-seat--left .played-cards').evaluate((el) => {
    const meta = el.querySelector('.played-cards__meta')!.getBoundingClientRect();
    const row = el.querySelector('.played-cards__row')!.getBoundingClientRect();
    return {
      cardBlockW: el.getBoundingClientRect().width,
      contentMaxW: Math.max(meta.width, row.width),
    };
  });
  expect(desktopWidths.cardBlockW).toBeLessThanOrEqual(desktopWidths.contentMaxW + 14);

  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/?demo=table');
  await page.evaluate(async () => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ path) as typeof import('../../src/stores/appStore');
    const longCards = Array.from({ length: 20 }, (_, i) => ({ suit: i % 4, rank: 3 + Math.floor(i / 4), color: i % 2 }));
    useAppStore.setState({
      mustPlay: false,
      lastPlayed: longCards,
      lastPlayedBy: 'p3',
      seatActions: {
        p3: { type: 'play', player_id: 'p3', player_name: '独孤求败超长昵称', cards: longCards, hand_type: '飞机带翅膀' },
        p2: { type: 'play', player_id: 'p2', player_name: '山月', cards: longCards, hand_type: '飞机带翅膀' },
      },
    });
  });
  const narrowMeta = await page.locator('.played-seat--left').evaluate((seat) => {
    const seatRect = seat.getBoundingClientRect();
    const meta = seat.querySelector('.played-cards__meta')!.getBoundingClientRect();
    const nameSpan = seat.querySelector('.played-cards__meta span')!.getBoundingClientRect();
    return {
      spanW: nameSpan.width,
      metaH: meta.height,
      metaRight: meta.right,
      seatRight: seatRect.right,
    };
  });
  expect(narrowMeta.spanW).toBeGreaterThanOrEqual(20);
  expect(narrowMeta.metaH).toBeLessThanOrEqual(26);
  expect(narrowMeta.metaRight).toBeLessThanOrEqual(narrowMeta.seatRight + 1);
});

test('forced minimum font sizes keep compact topbar within 361px viewport', async ({ page }) => {
  await page.setViewportSize({ width: 361, height: 640 });
  await page.goto('/?demo=table');
  await page.evaluate(async () => {
    const path = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ path) as typeof import('../../src/stores/appStore');
    useAppStore.setState({ multiplier: 9999 });
  });
  await page.addStyleTag({ content: '.table-score-strip strong { font-size: 20px !important; }' });
  const zones = page.locator('.table-brand:visible, .table-score-strip:visible, .table-tools:visible');
  const boxes = await zones.evaluateAll(elements => elements.map(el => el.getBoundingClientRect().toJSON()));
  expect(boxes).toHaveLength(3);
  for (const box of boxes) {
    expect(box.left).toBeGreaterThanOrEqual(0);
    expect(box.right).toBeLessThanOrEqual(361);
  }
  for (let i = 1; i < boxes.length; i++) {
    expect(boxes[i - 1].right).toBeLessThanOrEqual(boxes[i].left);
  }
  for (const button of await page.locator('.table-tools button').all()) {
    const box = (await button.boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(43.99);
  }
});
