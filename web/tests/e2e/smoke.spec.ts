import { expect, test } from '@playwright/test';

test('table demo renders the core game controls', async ({ page }) => {
  await page.goto('/?demo=table');
  await expect(page.getByText('斗地主').first()).toBeVisible();
  await expect(page.getByRole('button', { name: '出牌' })).toBeVisible();
  await expect(page.getByLabel('手牌')).toBeVisible();
  await expect(page.getByLabel('底牌')).toBeVisible();
});

test('bidding demo hides bottom cards and only shows bidding actions', async ({ page }) => {
  await page.goto('/?demo=bidding');
  await expect(page.getByLabel('底牌')).toContainText('底牌未揭');
  await expect(page.getByRole('button', { name: '叫地主' })).toBeVisible();
  await expect(page.getByRole('button', { name: '不叫' })).toBeVisible();
  await expect(page.getByRole('button', { name: '出牌' })).toHaveCount(0);
});

test('table controls stay compact inside the lower felt across phases and viewports', async ({ page }) => {
  const viewports = [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
    { width: 320, height: 568 },
    { width: 667, height: 375 }
  ];

  await page.goto('/?demo=table');

  for (const viewport of viewports) {
    await page.setViewportSize(viewport);
    const playingLayout = await page.evaluate(() => {
      const doc = document.documentElement;
      const arena = document.querySelector<HTMLElement>('.table-arena')?.getBoundingClientRect();
      const felt = document.querySelector<HTMLElement>('.felt-table')?.getBoundingClientRect();
      const controls = document.querySelector<HTMLElement>('.table-controls')?.getBoundingClientRect();
      const timer = document.querySelector<HTMLElement>('.turn-banner time')?.getBoundingClientRect();
      const buttons = Array.from(document.querySelectorAll<HTMLElement>('.action-bar button')).map((btn) => btn.getBoundingClientRect());
      const heading = document.querySelector<HTMLElement>('.hand-heading')?.getBoundingClientRect();
      if (!arena || !felt || !controls || !timer || buttons.length !== 4 || !heading) return null;
      return {
        fitsViewport: doc.scrollHeight <= window.innerHeight && doc.scrollWidth <= window.innerWidth,
        controlsInsideArena: controls.top >= arena.top && controls.bottom <= arena.bottom && controls.left >= arena.left && controls.right <= arena.right,
        feltAboveControls: felt.bottom <= controls.top + 1,
        timerCompact: timer.width >= 18 && timer.width <= 26 && timer.height >= 18 && timer.height <= 26,
        buttonsCompactAndTappable: buttons.every((b) => b.height >= 24 && b.height <= 32 && b.width >= 56),
        arenaTop: Math.round(arena.top),
        arenaBottom: Math.round(arena.bottom),
        headingTop: Math.round(heading.top)
      };
    });

    expect(playingLayout, `playing @ ${viewport.width}x${viewport.height}`).not.toBeNull();
    expect(playingLayout?.fitsViewport).toBe(true);
    expect(playingLayout?.controlsInsideArena).toBe(true);
    expect(playingLayout?.feltAboveControls).toBe(true);
    expect(playingLayout?.timerCompact).toBe(true);
    expect(playingLayout?.buttonsCompactAndTappable).toBe(true);

    // Switching between dealing -> bidding (mine) -> bidding (opponent) keeps outer table bounds steady.
    for (const nextState of [
      { phase: 'dealing', currentTurn: '' },
      { phase: 'bidding', currentTurn: 'p1' },
      { phase: 'bidding', currentTurn: 'p2' },
      { phase: 'playing', currentTurn: 'p1' }
    ]) {
      await page.evaluate(`import('/src/stores/appStore.ts').then(({ useAppStore }) => useAppStore.setState(${JSON.stringify(nextState)}))`);
      const bounds = await page.evaluate(() => {
        const arena = document.querySelector<HTMLElement>('.table-arena')!.getBoundingClientRect();
        const heading = document.querySelector<HTMLElement>('.hand-heading')!.getBoundingClientRect();
        return { arenaTop: Math.round(arena.top), arenaBottom: Math.round(arena.bottom), headingTop: Math.round(heading.top) };
      });
      expect(bounds, `${nextState.phase}/${nextState.currentTurn} @ ${viewport.width}x${viewport.height}`).toEqual({
        arenaTop: playingLayout!.arenaTop,
        arenaBottom: playingLayout!.arenaBottom,
        headingTop: playingLayout!.headingTop
      });
    }
  }
});

test('lobby demo renders the basic room actions', async ({ page }) => {
  await page.goto('/?demo=lobby');
  await expect(page.getByRole('button', { name: /快速开局/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /创建房间/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /人机练习/ })).toBeVisible();
});

test('waiting room fits room code, seats, and both action buttons without scrolling on desktop and mobile', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'share', {
      configurable: true,
      value: async () => {}
    });
  });

  const viewports = [
    { width: 1440, height: 900 },
    { width: 1366, height: 640 },
    { width: 1280, height: 584 },
    { width: 390, height: 664 },
    { width: 375, height: 560 },
    { width: 320, height: 568 },
    { width: 667, height: 375 }
  ];

  await page.goto('/?demo=waiting');

  for (const viewport of viewports) {
    await page.setViewportSize(viewport);
    const readyButton = page.getByRole('button', { name: '准备开始' });
    const leaveButton = page.getByRole('button', { name: '离开房间' });
    await expect(page.locator('#waiting-room-code')).toBeVisible();
    await expect(page.locator('.room-waiting .seat-row')).toHaveCount(3);
    await expect(readyButton).toBeVisible();
    await expect(leaveButton).toBeVisible();

    const fit = await page.evaluate(() => {
      const doc = document.documentElement;
      const waiting = document.querySelector<HTMLElement>('.room-waiting');
      const buttons = Array.from(document.querySelectorAll<HTMLElement>('.room-waiting .room-actions button'));
      if (!waiting || buttons.length !== 2) return null;
      const waitingRect = waiting.getBoundingClientRect();
      return {
        docFits: doc.scrollHeight <= window.innerHeight && doc.scrollWidth <= window.innerWidth,
        panelFits: waiting.scrollHeight <= waiting.clientHeight && waiting.scrollWidth <= waiting.clientWidth,
        buttonsVisibleWithoutScroll: buttons.every((btn) => {
          const rect = btn.getBoundingClientRect();
          return (
            rect.top >= waitingRect.top - 1 &&
            rect.bottom <= waitingRect.bottom + 1 &&
            rect.top >= 0 &&
            rect.bottom <= window.innerHeight &&
            rect.left >= 0 &&
            rect.right <= window.innerWidth
          );
        })
      };
    });

    expect(fit, `viewport ${viewport.width}x${viewport.height}`).toEqual({
      docFits: true,
      panelFits: true,
      buttonsVisibleWithoutScroll: true
    });
  }
});

test('leaderboard and stats panels keep reload button compact with clean spacing across desktop and mobile viewports', async ({ page }) => {
  const viewports = [
    { width: 1440, height: 900, fitsWithoutScroll: true },
    { width: 390, height: 844, fitsWithoutScroll: true },
    { width: 667, height: 375, fitsWithoutScroll: true },
    { width: 320, height: 568, fitsWithoutScroll: false }
  ];

  for (const demo of ['leaderboard', 'stats', 'stats-empty'] as const) {
    await page.goto(`/?demo=${demo}`);
    for (const viewport of viewports) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      const reload = page.getByRole('button', { name: '重新加载' });
      await expect(reload).toBeVisible();

      const layout = await page.evaluate(() => {
        const doc = document.documentElement;
        const panel = document.querySelector<HTMLElement>('.sub-panel');
        const head = document.querySelector<HTMLElement>('.sub-panel__head');
        const reloadBtn = document.querySelector<HTMLElement>('.sub-panel__reload');
        const body = head?.nextElementSibling as HTMLElement | null;
        if (!panel || !head || !reloadBtn || !body) return null;

        const pRect = panel.getBoundingClientRect();
        const hRect = head.getBoundingClientRect();
        const rRect = reloadBtn.getBoundingClientRect();
        const bRect = body.getBoundingClientRect();

        return {
          noHorizontalOverflow: doc.scrollWidth <= window.innerWidth && panel.scrollWidth <= panel.clientWidth,
          reloadCompact: rRect.width <= pRect.width * 0.48,
          reloadSeparatedFromBody: bRect.top - rRect.bottom >= 10 && bRect.top - hRect.bottom >= 6,
          bodyFitsWithoutScroll: body.scrollHeight <= body.clientHeight + 1
        };
      });

      expect(layout, `${demo} @ ${viewport.width}x${viewport.height}`).not.toBeNull();
      expect(layout?.noHorizontalOverflow, `${demo} overflow @ ${viewport.width}x${viewport.height}`).toBe(true);
      expect(layout?.reloadCompact, `${demo} reload width @ ${viewport.width}x${viewport.height}`).toBe(true);
      expect(layout?.reloadSeparatedFromBody, `${demo} reload gap @ ${viewport.width}x${viewport.height}`).toBe(true);
      if (demo === 'stats' && viewport.fitsWithoutScroll) {
        expect(layout?.bodyFitsWithoutScroll, `stats fit @ ${viewport.width}x${viewport.height}`).toBe(true);
      }
    }
  }
});
