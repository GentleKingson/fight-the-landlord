import { afterEach, expect, test, vi } from 'vitest';
import { observeClock, resetClock, turnTiming } from '../src/transport/clock';
afterEach(() => { resetClock(); vi.useRealTimers(); });
test.each([500, 1000, 2000])('RTT %d and clock skew preserve the absolute deadline', rtt => {
  vi.useFakeTimers();
  vi.setSystemTime(100000 + rtt);
  observeClock(100000, 500000 + rtt / 2);
  const timing = turnTiming({ server_time: 500000 + rtt / 2, turn_deadline: 510000, timeout: 10 });
  expect(timing.timerStart + timing.timeout * 1000).toBe(110000);
  vi.advanceTimersByTime(11000);
  expect(turnTiming({ server_time: 500000, turn_deadline: 510000 }).timeout).toBe(0);
});
