import { useLayoutEffect, useState } from 'react';
import type { DealStage } from '../../stores/appStore';

/** Start/end timestamps and duration for a single dealt card flight. */
export interface DealFlightTiming {
  start: number;
  end: number;
  flight: number;
}

/** Reactive deal animation progress for the player's hand and deck overlay. */
export interface DealPresentation {
  visibleCount: number;
  finished: boolean;
  reducedMotion: boolean;
}

/**
 * Computes flight timing for card `index` in the 51-card dealing sequence.
 * Keeps the original 51-flight cadence so the last local card lands at `endAt`.
 */
export function dealFlightTiming(deal: DealStage, index: number, count: number): DealFlightTiming {
  const duration = Math.max(0, deal.endAt - deal.startAt);
  const flight = duration / 9;
  const start = deal.startAt + (index * (duration - flight)) / Math.max(1, count * 3 - 1);
  return { start, end: start + flight, flight };
}

/** Returns how many of the local player's `count` cards have landed by `now`. */
export function dealtCount(deal: DealStage, count: number, now: number): number {
  let landed = 0;
  while (landed < count && now >= dealFlightTiming(deal, landed * 3 + 2, count).end) {
    landed++;
  }
  return landed;
}

/** Tracks dealing animation progress and `prefers-reduced-motion` preference. */
export function useDealPresentation(deal: DealStage | null, count: number): DealPresentation {
  const [clock, setClock] = useState(() => ({ deal, now: Date.now() }));
  const [reducedMotion, setReducedMotion] = useState(
    () => window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  );
  // A new deal (including a reconnect snapshot) must never render the previous count.
  const now = clock.deal === deal ? clock.now : Date.now();
  useLayoutEffect(() => {
    const media = window.matchMedia?.('(prefers-reduced-motion: reduce)');
    if (!media) return;
    const update = () => {
      setReducedMotion(media.matches);
    };
    update();
    media.addEventListener('change', update);
    return () => {
      media.removeEventListener('change', update);
    };
  }, []);
  useLayoutEffect(() => {
    if (!deal) return;
    let timer: number;
    const update = () => {
      const currentNow = Date.now();
      setClock({ deal, now: currentNow });
      if (currentNow >= deal.endAt) return;
      const landed = dealtCount(deal, count, currentNow);
      const next = landed < count ? dealFlightTiming(deal, landed * 3 + 2, count).end : deal.endAt;
      timer = window.setTimeout(update, Math.max(1, Math.ceil(next - currentNow)));
    };
    update();
    // Background tabs may throttle timers; seek directly on return.
    const resume = () => {
      window.clearTimeout(timer);
      update();
    };
    document.addEventListener('visibilitychange', resume);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener('visibilitychange', resume);
    };
  }, [deal, count]);
  return {
    visibleCount: !deal || reducedMotion ? count : dealtCount(deal, count, now),
    finished: !deal || now >= deal.endAt,
    reducedMotion
  };
}
