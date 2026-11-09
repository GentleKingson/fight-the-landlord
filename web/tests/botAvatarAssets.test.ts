// @vitest-environment node
import { readFileSync } from 'node:fs';
import { expect, test } from 'vitest';

test.each(['scholar', 'jade', 'uncle', 'blossom', 'sunshine', 'grandma'])('bot portrait %s stays within its size and payload budget', name => {
  const bytes = readFileSync(new URL(`../src/assets/bot-avatars/${name}.webp`, import.meta.url));
  expect(bytes.length).toBeLessThan(12 * 1024);
  expect(bytes.toString('ascii', 0, 4)).toBe('RIFF');
  expect(bytes.toString('ascii', 8, 12)).toBe('WEBP');
  expect(bytes.toString('ascii', 12, 16)).toBe('VP8 ');
  expect(bytes.readUInt16LE(26) & 0x3fff).toBe(192);
  expect(bytes.readUInt16LE(28) & 0x3fff).toBe(192);
});
