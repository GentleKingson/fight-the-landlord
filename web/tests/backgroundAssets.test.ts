// @vitest-environment node
import { readFileSync } from 'node:fs';
import { expect, test } from 'vitest';

test.each([
  { width: 960, height: 540, budget: 10 * 1024 },
  { width: 1600, height: 900, budget: 24 * 1024 },
])('emerald background $width stays within its decoded size and payload budget', ({ width, height, budget }) => {
  const bytes = readFileSync(new URL(`../src/assets/emerald-table-${width}.webp`, import.meta.url));
  expect(bytes.length).toBeLessThan(budget);
  expect(bytes.toString('ascii', 0, 4)).toBe('RIFF');
  expect(bytes.toString('ascii', 8, 12)).toBe('WEBP');
  // These static assets use a simple lossy VP8 key frame, without metadata.
  expect(bytes.toString('ascii', 12, 16)).toBe('VP8 ');
  expect(bytes.subarray(23, 26)).toEqual(Buffer.from([0x9d, 0x01, 0x2a]));
  expect(bytes.readUInt16LE(26) & 0x3fff).toBe(width);
  expect(bytes.readUInt16LE(28) & 0x3fff).toBe(height);
});
