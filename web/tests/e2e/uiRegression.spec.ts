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
