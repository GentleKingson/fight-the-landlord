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
  expect(result).toHaveLength(66);
  for (const clip of result) {
    expect(clip.duration, clip.name).toBeGreaterThan(0);
    expect(clip.duration, clip.name).toBeLessThan(6);
  }
});

test('the final hand completes before the full settlement clip in a real AudioContext', async ({ page }) => {
  await page.goto('/?demo=table');
  // A real gesture unlocks the same singleton used by live socket messages.
  await page.getByRole('button', { name: '聊天', exact: true }).click();
  await page.evaluate(`(async () => {
    const { gameAudio } = await import('/src/audio/gameAudio.ts');
    window.audioProbe = [];
    const original = AudioBufferSourceNode.prototype.start;
    AudioBufferSourceNode.prototype.start = function (...args) {
      const record = { start: performance.now(), duration: this.buffer.duration, end: null };
      window.audioProbe.push(record);
      const onended = this.onended;
      this.onended = (event) => { record.end = performance.now(); onended?.call(this, event); };
      return original.apply(this, args);
    };
    gameAudio.play('炸弹', Array.from({ length: 4 }, (_, suit) => ({ rank: 3, suit, color: 0 })), 0);
    gameAudio.result(true);
  })()`);
  await page.waitForFunction('window.audioProbe.length === 3 && window.audioProbe.every(clip => clip.end !== null)');
  const clips = await page.evaluate<{ start: number; end: number; duration: number }[]>('window.audioProbe');
  expect(clips[1].start).toBeGreaterThanOrEqual(clips[0].end);
  expect(clips[2].start).toBeGreaterThanOrEqual(clips[1].end);
  expect(clips[2].duration).toBeGreaterThan(4);
  expect(clips[2].end - clips[2].start).toBeGreaterThan(3900);
});
