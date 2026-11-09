import { expect, test } from '@playwright/test';

test('card artwork fits every card size and responsive context without joker collisions', async ({ page }, testInfo) => {
  await page.route('**/api/config', route => route.fulfill({ json: {} }));
  await page.goto('/?demo=table');
  // Mount the production Card with the same ancestor selectors as each consumer.
  await page.evaluate(async () => {
    const reactPath = '/node_modules/.vite/deps/react.js';
    const rootPath = '/node_modules/.vite/deps/react-dom_client.js';
    const cardPath = '/src/shared/cards/Card.tsx';
    const { default: React } = await import(/* @vite-ignore */ reactPath);
    const h = React.createElement;
    const { default: ReactDOM } = await import(/* @vite-ignore */ rootPath);
    const { createRoot } = ReactDOM;
    const { Card } = await import(/* @vite-ignore */ cardPath);
    const host = document.createElement('div');
    host.id = 'card-scaling-probe';
    document.body.append(host);
    document.getElementById('root')!.style.display = 'none';
    const contexts = [
      ['hand', 'hand'], ['played', ''], ['mini', ''], ['action', ''],
      ['mini', 'bottom-cards'], ['played', 'felt-table'], ['played', 'end-table__cards'],
    ];
    createRoot(host).render(h('div', {}, contexts.map(([size, context]) => h('section', {
      key: `${size}-${context}`, className: context,
      style: { display: 'flex', gap: '12px', height: 'auto', padding: '16px', position: 'relative' },
    }, h('div', { className: context === 'felt-table' ? 'played-cards' : '', style: { display: 'flex', gap: '12px' } }, [16, 17, 10].map(rank => h(Card, { key: rank, size, card: { rank, suit: rank >= 16 ? 4 : 1, color: rank === 16 ? 0 : 1 } })))))));
  });
  const cards = page.locator('#card-scaling-probe .card');
  await expect(cards).toHaveCount(21);
  for (const viewport of [
    { width: 1440, height: 900 }, { width: 880, height: 700 },
    { width: 390, height: 844 }, { width: 320, height: 568 },
    { width: 667, height: 375 }, { width: 844, height: 390 },
  ]) {
    await page.setViewportSize(viewport);
    const issues = await cards.evaluateAll(elements => elements.flatMap(card => {
      const box = card.getBoundingClientRect();
      const errors: string[] = [];
      const label = `${card.closest('section')!.className}/${card.className}`;
      for (const part of card.children) {
        if (!part.getClientRects().length) continue;
        const rect = part.getBoundingClientRect();
        if (rect.left < box.left || rect.right > box.right || rect.top < box.top || rect.bottom > box.bottom) {
          errors.push(`${label}: ${part.className} outside card`);
        }
      }
      if (card.classList.contains('card--mini')) {
        if (!card.closest('.bottom-cards') && (box.width !== 34 || box.height !== 48)) errors.push(`${label}: mini size overridden`);
        const parts = [...card.children].filter(part => part.getClientRects().length);
        for (let i = 0; i < parts.length; i++) for (let j = i + 1; j < parts.length; j++) {
          const a = parts[i].getBoundingClientRect(), b = parts[j].getBoundingClientRect();
          if (a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top) errors.push(`${label}: mini artwork collision`);
        }
      }
      const word = card.querySelector('.card__joker-word');
      const star = card.querySelector('.card__joker-star');
      if (word && star) {
        const a = word.getBoundingClientRect(), b = star.getBoundingClientRect();
        if (a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top) errors.push(`${label}: joker collision`);
      }
      return errors;
    }));
    expect(issues, `${viewport.width}x${viewport.height}`).toEqual([]);
    if (viewport.width === 390) {
      await page.locator('#card-scaling-probe').screenshot({ path: `/tmp/ddz-card-scaling-${testInfo.project.name}.png` });
    }
  }
});
