import { expect, test } from 'vitest';
import { resolveSettlementOutcome } from '../src/shared/ui/backgroundScene';

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
