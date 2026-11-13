import { expect, test } from '@playwright/test';

test('all three seats display the triple before its higher pair', async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  await page.evaluate(async () => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    const cards = [
      { suit: 0, rank: 14, color: 0 }, { suit: 1, rank: 14, color: 1 },
      { suit: 2, rank: 3, color: 0 }, { suit: 1, rank: 3, color: 1 }, { suit: 0, rank: 3, color: 0 }
    ];
    useAppStore.setState({ mustPlay: false });
    for (const player of useAppStore.getState().players) {
      useAppStore.getState().handleMessage({ type: 'card_played', payload: {
        player_id: player.id, player_name: player.name, cards, cards_left: 5, hand_type: '三带二'
      } });
    }
  });
  for (const side of ['left', 'me', 'right']) {
    const cards = page.locator(`.played-seat--${side} .card--played`);
    await expect(cards).toHaveCount(5);
    expect(await cards.evaluateAll(elements => elements.map(card => card.getAttribute('aria-label'))))
      .toEqual(['3♠', '3♥', '3♣', 'A♠', 'A♥']);
    const positions = await cards.evaluateAll(elements => elements.map(card => card.getBoundingClientRect().left));
    expect(positions.every((x, index) => index === 0 || x > positions[index - 1])).toBe(true);
  }
  await expect(page.getByText('待压过', { exact: true })).toHaveCount(1);
  await expect(page.locator('.played-seat--left')).toHaveClass(/played-seat--target/);
});

test('target label and long hands fit the table and clear for a new trick', async ({ page }) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  await expect(page.getByText('跟牌目标：松风')).toBeVisible();
  await page.evaluate(async () => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    const cards = [3, 4, 5, 6].flatMap(rank => [0, 1, 2].map(suit => ({ rank, suit, color: suit % 2 })));
    cards.push(...[8, 9, 10, 11].flatMap(rank => [0, 1].map(suit => ({ rank, suit, color: suit % 2 }))));
    for (const player of useAppStore.getState().players) {
      useAppStore.getState().handleMessage({ type: 'card_played', payload: {
        player_id: player.id, player_name: player.name, cards, cards_left: 1, hand_type: '飞机带对'
      } });
    }
  });
  const label = page.getByText('待压过', { exact: true });
  await expect(label).toBeVisible();
  const bounds = await label.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
  for (const side of ['left', 'me', 'right']) {
    const row = page.locator(`.played-seat--${side} .played-cards__row`);
    await expect(row.locator('.card--played')).toHaveCount(20);
    const box = await row.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
  }
  await page.evaluate(async () => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    useAppStore.getState().handleMessage({ type: 'play_turn', payload: {
      player_id: 'p3', must_play: true, can_beat: true, timeout: 30
    } });
  });
  await expect(label).toHaveCount(0);
  await expect(page.locator('.played-seat--target')).toHaveCount(0);
  await expect(page.getByText('新一轮，请出牌')).toBeVisible();
});

test('target ring follows visible cards for short and long plays', async ({ page }, testInfo) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  for (const count of [1, 2, 4, 5, 12, 20]) {
    await page.evaluate(async (count) => {
      const modulePath = '/src/stores/appStore.ts';
      const { useAppStore } = await import(/* @vite-ignore */ modulePath);
      const state = useAppStore.getState();
      const actions = Object.fromEntries(state.players.map((player: { id: string }) => [player.id, {
        type: 'play', player_id: player.id, hand_type: '出牌',
        cards: Array.from({ length: count }, (_, i) => ({ rank: 3 + i % 13, suit: i % 4, color: i % 2 }))
      }]));
      useAppStore.setState({ mustPlay: false, lastPlayedBy: state.players[0].id, seatActions: actions });
    }, count);
    await expect(page.locator('.played-seat--target')).toHaveCount(1);
    if (count <= 5) {
      await expect(page.locator('.played-cards__scroll-hint')).toHaveCount(0);
    }
    for (const side of ['left', 'me', 'right']) {
      const row = page.locator(`.played-seat--${side} .played-cards__row`);
      await expect(row.locator('.card')).toHaveCount(count);
      const geometry = await row.evaluate(element => {
        const rect = element.getBoundingClientRect();
        const cards = [...element.children].map(card => card.getBoundingClientRect());
        return {
          left: rect.left,
          right: rect.right,
          first: cards[0].left,
          last: Math.min(rect.right, cards.at(-1)!.right),
          rawLast: cards.at(-1)!.right,
          scrollWidth: element.scrollWidth,
          clientWidth: element.clientWidth
        };
      });
      expect(Math.abs(geometry.left - geometry.first)).toBeLessThan(1);
      expect(Math.abs(geometry.right - geometry.last)).toBeLessThan(1);
      if (count <= 5) {
        expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.clientWidth + 1);
        expect(Math.abs(geometry.right - geometry.rawLast)).toBeLessThan(1);
      }
    }
    if (count === 4) await page.screenshot({ path: `/tmp/ddz-layout-${testInfo.project.name}.png` });
    for (const row of await page.locator('.felt-table .played-cards__row').all()) {
      const readable = await row.evaluate(element => {
        const cards = [...element.children];
        return cards.every((card, index) => {
          const rank = card.querySelector('.card__index--top b')!;
          const next = cards[index + 1];
          return parseFloat(getComputedStyle(rank).fontSize) >= 14 && (!next || rank.getBoundingClientRect().right <= next.getBoundingClientRect().left);
        });
      });
      expect(readable).toBe(true);
      await row.evaluate(element => { element.scrollLeft = element.scrollWidth; });
      const last = await row.locator('.card').last().boundingBox();
      const bounds = (await row.boundingBox())!;
      expect(last!.x + last!.width).toBeLessThanOrEqual(bounds.x + bounds.width + 1);
    }
  }
  await page.goto('/?demo=lobby');
  await expect(page.locator('.quick-card__art .card')).toHaveCount(2);
  await page.screenshot({ path: `/tmp/ddz-lobby-${testInfo.project.name}.png` });
});

test('mobile portrait shows full opponent plays without clipping', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'mobile-portrait', 'Portrait viewport sweep runs once');
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  const leftCards = [
    { suit: 0, rank: 10, color: 0 },
    { suit: 1, rank: 10, color: 1 },
    { suit: 3, rank: 10, color: 1 },
    { suit: 3, rank: 12, color: 1 }
  ];
  const rightCards = [
    { suit: 0, rank: 3, color: 0 },
    { suit: 1, rank: 3, color: 1 },
    { suit: 3, rank: 3, color: 1 },
    { suit: 2, rank: 8, color: 0 }
  ];
  const fiveCards = [
    { suit: 0, rank: 10, color: 0 },
    { suit: 1, rank: 10, color: 1 },
    { suit: 3, rank: 10, color: 1 },
    { suit: 0, rank: 14, color: 0 },
    { suit: 1, rank: 14, color: 1 }
  ];

  for (const viewport of [{ width: 390, height: 664 }, { width: 320, height: 568 }]) {
    await page.setViewportSize(viewport);
    await page.goto('/?demo=table');
    for (const [label, left, right] of [
      ['三带一', leftCards, rightCards],
      ['三带二', fiveCards, fiveCards]
    ] as const) {
      await page.evaluate(async ({ label, left, right }) => {
        const modulePath = '/src/stores/appStore.ts';
        const { useAppStore } = await import(/* @vite-ignore */ modulePath);
        const state = useAppStore.getState();
        const players = state.players as Array<{ id: string; seat: number; name: string }>;
        const me = players.find(p => p.id === state.playerId)!;
        const leftPlayer = players.find(p => p.seat === (me.seat + 2) % 3)!;
        const rightPlayer = players.find(p => p.seat === (me.seat + 1) % 3)!;
        useAppStore.setState({
          mustPlay: false,
          lastPlayed: left,
          lastPlayedBy: leftPlayer.id,
          lastPlayedName: leftPlayer.name,
          lastHandType: label,
          seatActions: {
            [leftPlayer.id]: { type: 'play', player_id: leftPlayer.id, player_name: leftPlayer.name, cards: left, hand_type: label },
            [rightPlayer.id]: { type: 'play', player_id: rightPlayer.id, player_name: rightPlayer.name, cards: right, hand_type: label }
          }
        });
      }, { label, left, right });

      await expect(page.locator('.played-cards__scroll-hint')).toHaveCount(0);
      for (const side of ['left', 'right']) {
        const row = page.locator(`.played-seat--${side} .played-cards__row`);
        const cards = row.locator('.card--played');
        await expect(cards).toHaveCount(left.length);
        const rowBox = (await row.boundingBox())!;
        for (const card of await cards.all()) {
          await expect(card).toBeInViewport({ ratio: 1 });
          const cardBox = (await card.boundingBox())!;
          expect(cardBox.x).toBeGreaterThanOrEqual(rowBox.x - 1);
          expect(cardBox.x + cardBox.width).toBeLessThanOrEqual(rowBox.x + rowBox.width + 1);
        }
      }
    }
  }

  await page.setViewportSize({ width: 390, height: 664 });
  await page.goto('/?demo=table');
  const airplaneWithSingles = [
    { suit: 0, rank: 7, color: 0 }, { suit: 1, rank: 7, color: 1 }, { suit: 2, rank: 7, color: 0 },
    { suit: 0, rank: 8, color: 0 }, { suit: 1, rank: 8, color: 1 }, { suit: 2, rank: 8, color: 0 },
    { suit: 0, rank: 14, color: 0 }, { suit: 1, rank: 15, color: 1 }
  ];
  await page.evaluate(async (cards) => {
    const modulePath = '/src/stores/appStore.ts';
    const { useAppStore } = await import(/* @vite-ignore */ modulePath);
    const state = useAppStore.getState();
    const players = state.players as Array<{ id: string; seat: number; name: string }>;
    const me = players.find(p => p.id === state.playerId)!;
    const leftPlayer = players.find(p => p.seat === (me.seat + 2) % 3)!;
    const rightPlayer = players.find(p => p.seat === (me.seat + 1) % 3)!;
    useAppStore.setState({
      mustPlay: false,
      lastPlayed: cards,
      lastPlayedBy: leftPlayer.id,
      lastPlayedName: leftPlayer.name,
      lastHandType: '飞机带单',
      seatActions: {
        [leftPlayer.id]: { type: 'play', player_id: leftPlayer.id, player_name: leftPlayer.name, cards, hand_type: '飞机带单' },
        [rightPlayer.id]: { type: 'pass', player_id: rightPlayer.id, player_name: rightPlayer.name, label: '不出' }
      }
    });
  }, airplaneWithSingles);

  await expect(page.locator('.played-cards__scroll-hint')).toHaveCount(0);
  const leftRow = page.locator('.played-seat--left .played-cards__row');
  await expect(leftRow.locator('.card--played')).toHaveCount(8);
  const overflow = await leftRow.evaluate(el => el.scrollWidth > el.clientWidth + 1);
  expect(overflow).toBe(false);
  const leftRowBox = (await leftRow.boundingBox())!;
  for (const card of await leftRow.locator('.card--played').all()) {
    await expect(card).toBeInViewport({ ratio: 1 });
    const cardBox = (await card.boundingBox())!;
    expect(cardBox.x).toBeGreaterThanOrEqual(leftRowBox.x - 1);
    expect(cardBox.x + cardBox.width).toBeLessThanOrEqual(leftRowBox.x + leftRowBox.width + 1);
  }
});
