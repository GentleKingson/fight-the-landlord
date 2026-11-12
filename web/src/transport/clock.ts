// Server timestamps and midpoint RTT give an estimate, not an extension of the
// authoritative deadline. Keep display and warning audio on this same timeline.
let offset: number | null = null;
let rtt = 0;
export function observeClock(sent: number, server: number): void {
  const elapsed = Date.now() - sent;
  if (!server || elapsed < 0 || elapsed > 15000) return;
  rtt = elapsed;
  offset = server + elapsed / 2 - Date.now();
}
export function resetClock(): void { offset = null; rtt = 0; }
export function turnTiming(p: { timeout?: number; server_time?: number; turn_deadline?: number }) {
  const now = Date.now();
  const remaining = p.turn_deadline && p.server_time
    ? p.turn_deadline - (offset === null ? p.server_time + rtt / 2 : now + offset)
    : (p.timeout ?? 0) * 1000 - rtt / 2;
  return { timerStart: now, timeout: Math.max(0, remaining) / 1000, serverDeadline: p.turn_deadline ?? 0, serverTimestamp: p.server_time ?? 0 };
}
