// @vitest-environment node
import { readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';
import { resolveBackgroundScene, resolveSettlementOutcome } from '../src/shared/ui/backgroundScene';

const SCENES = ['lobby', 'table', 'settlement'] as const;
const VARIANTS = [
  { suffix: '960', width: 960, height: 540, budget: 40 * 1024 },
  { suffix: '1600', width: 1600, height: 900, budget: 72 * 1024 },
  { suffix: 'portrait-900', width: 900, height: 1600, budget: 60 * 1024 },
] as const;

describe('scene background WebP assets', () => {
  test.each(
    SCENES.flatMap((scene) => VARIANTS.map((variant) => ({ scene, ...variant }))),
  )('scene-$scene-$suffix.webp stays within its decoded size and payload budget', ({ scene, suffix, width, height, budget }) => {
    const bytes = readFileSync(new URL(`../src/assets/scene-${scene}-${suffix}.webp`, import.meta.url));
    expect(bytes.length).toBeGreaterThan(4 * 1024);
    expect(bytes.length).toBeLessThan(budget);
    expect(bytes.toString('ascii', 0, 4)).toBe('RIFF');
    expect(bytes.toString('ascii', 8, 12)).toBe('WEBP');
    // Static scene assets use a single metadata-free lossy VP8 key frame.
    expect(bytes.toString('ascii', 12, 16)).toBe('VP8 ');
    expect(bytes.subarray(23, 26)).toEqual(Buffer.from([0x9d, 0x01, 0x2a]));
    expect(bytes.readUInt16LE(26) & 0x3fff).toBe(width);
    expect(bytes.readUInt16LE(28) & 0x3fff).toBe(height);
  });
});

describe('background scene resolution', () => {
  test.each([
    ['connecting', 'lobby'],
    ['lobby', 'lobby'],
    ['matching', 'lobby'],
    ['waiting', 'lobby'],
    ['dealing', 'table'],
    ['bidding', 'table'],
    ['playing', 'table'],
    ['game_over', 'settlement'],
  ] as const)('maps phase %s to scene %s', (phase, expected) => {
    expect(resolveBackgroundScene(phase)).toBe(expected);
  });

  test('resolves settlement outcome for landlord and peasant teammates', () => {
    const scores = [
      { player_id: 'p1', player_name: '青竹', is_landlord: false, score: 6 },
      { player_id: 'p2', player_name: '山月', is_landlord: true, score: -12 },
      { player_id: 'p3', player_name: '松风', is_landlord: false, score: 6 },
    ];
    expect(resolveSettlementOutcome('p1', 'p3', false, scores)).toBe('victory');
    expect(resolveSettlementOutcome('p3', 'p3', false, scores)).toBe('victory');
    expect(resolveSettlementOutcome('p2', 'p3', false, scores)).toBe('defeat');
    expect(resolveSettlementOutcome('', 'p3', false, scores)).toBe('neutral');
  });
});
