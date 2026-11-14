// Server timestamps and midpoint RTT give an estimate, not an extension of the
// authoritative deadline. Keep display and warning audio on this same timeline.
const MAX_CLOCK_SAMPLE_RTT_MS = 15_000;

export interface TurnTimingInput {
  timeout?: number;
  server_time?: number;
  turn_deadline?: number;
}

export interface TurnTimingResult {
  timerStart: number;
  timeout: number;
  serverDeadline: number;
  serverTimestamp: number;
}

let offset: number | null = null;
let rtt = 0;

/** Updates the estimated clock offset and round-trip time from a pong sample. */
export function observeClock(sent: number, server: number): void {
  const elapsed = Date.now() - sent;
  if (!server || elapsed < 0 || elapsed > MAX_CLOCK_SAMPLE_RTT_MS) return;
  rtt = elapsed;
  offset = server + elapsed / 2 - Date.now();
}

/** Clears cached clock offset and RTT estimates on reconnect. */
export function resetClock(): void {
  offset = null;
  rtt = 0;
}

/** Computes local countdown timing aligned to the server's authoritative deadline. */
export function turnTiming(timing: TurnTimingInput): TurnTimingResult {
  const now = Date.now();
  const remaining = timing.turn_deadline && timing.server_time
    ? timing.turn_deadline - (offset === null ? timing.server_time + rtt / 2 : now + offset)
    : (timing.timeout ?? 0) * 1000 - rtt / 2;
  return {
    timerStart: now,
    timeout: Math.max(0, remaining) / 1000,
    serverDeadline: timing.turn_deadline ?? 0,
    serverTimestamp: timing.server_time ?? 0
  };
}
