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
