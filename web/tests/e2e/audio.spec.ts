import { expect, test } from '@playwright/test';

test('all reused audio assets load and decode in the browser', async ({ page }) => {
  await page.goto('/?demo=table');
  const result = await page.evaluate<{ name: string; duration: number }[]>(`(async () => {
    const { cardSoundUrls } = await import('/src/audio/cardSounds.ts');
    const context = new AudioContext();
    try {
      return await Promise.all(Object.entries(cardSoundUrls).map(async ([name, url]) => {
        const response = await fetch(url);
        if (!response.ok) throw new Error(name + ': HTTP ' + response.status);
        const buffer = await context.decodeAudioData(await response.arrayBuffer());
        return { name, duration: buffer.duration };
      }));
    } finally { await context.close(); }
  })()`);
  expect(result).toHaveLength(54);
  for (const clip of result) {
    expect(clip.duration, clip.name).toBeGreaterThan(0);
    expect(clip.duration, clip.name).toBeLessThan(3);
  }
});
