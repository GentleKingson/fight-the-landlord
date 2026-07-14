import { describe, expect, it } from 'vitest';
import { resolveDemoMode } from '../src/stores/demoMode';

describe('demo mode gate', () => {
  it('ignores demo query parameters in a normal production build', () => {
    expect(resolveDemoMode('?demo=table', false)).toBeNull();
  });

  it('accepts known demos in development or an explicitly enabled demo build', () => {
    expect(resolveDemoMode('?demo=table', true)).toBe('table');
    expect(resolveDemoMode('?demo=bidding', true)).toBe('bidding');
    expect(resolveDemoMode('?demo=lobby', true)).toBe('lobby');
    expect(resolveDemoMode('?demo=result', true)).toBe('result');
  });

  it('rejects unsupported demo names even when demo mode is enabled', () => {
    expect(resolveDemoMode('?demo=unknown', true)).toBeNull();
    expect(resolveDemoMode('', true)).toBeNull();
  });
});
