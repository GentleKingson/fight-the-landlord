import type { EventMeta, PongPayload } from '../../protocol/generated';

export interface ServerClockState {
  serverClockOffsetMs: number;
  clockBestRttMs: number;
}

export const initialServerClock: ServerClockState = {
  serverClockOffsetMs: 0,
  clockBestRttMs: Number.POSITIVE_INFINITY
};

export interface ClockObservation extends ServerClockState {
  latency: number;
}

// Pong carries the client send time and the server receive time. Using the
// local send/receive midpoint removes the symmetric portion of network delay;
// keeping the minimum-RTT sample avoids letting queueing spikes move the clock.
export function observePong(
  clock: ServerClockState,
  payload: Pick<PongPayload, 'client_timestamp' | 'server_timestamp'>,
  receivedAt: number
): ClockObservation {
  const roundTrip = Math.max(0, receivedAt - payload.client_timestamp);
  if (payload.server_timestamp <= 0 || roundTrip > clock.clockBestRttMs) {
    return {
      serverClockOffsetMs: clock.serverClockOffsetMs,
      clockBestRttMs: clock.clockBestRttMs,
      latency: roundTrip
    };
  }

  const midpoint = payload.client_timestamp + roundTrip / 2;
  return {
    latency: roundTrip,
    clockBestRttMs: roundTrip,
    serverClockOffsetMs: payload.server_timestamp - midpoint
  };
}

export function observeServerTimestamp(
  clock: ServerClockState,
  serverTimeMs: number | undefined,
  receivedAt: number
): ServerClockState {
  if (!serverTimeMs || Number.isFinite(clock.clockBestRttMs)) {
    return {
      serverClockOffsetMs: clock.serverClockOffsetMs,
      clockBestRttMs: clock.clockBestRttMs
    };
  }
  return {
    serverClockOffsetMs: serverTimeMs - receivedAt,
    clockBestRttMs: clock.clockBestRttMs
  };
}

export function deadlineFromEvent(
  event: EventMeta | undefined,
  legacyTimeoutSeconds: number,
  receivedAt: number,
  serverClockOffsetMs: number
): number {
  if (event) return event.turn_deadline_ms ?? 0;
  if (legacyTimeoutSeconds <= 0) return 0;
  return receivedAt + serverClockOffsetMs + legacyTimeoutSeconds * 1_000;
}

export function remainingSeconds(
  serverDeadlineMs: number,
  serverClockOffsetMs: number,
  clientNowMs: number
): number {
  if (serverDeadlineMs <= 0) return 0;
  const serverNow = clientNowMs + serverClockOffsetMs;
  return Math.max(0, Math.ceil((serverDeadlineMs - serverNow) / 1_000));
}
