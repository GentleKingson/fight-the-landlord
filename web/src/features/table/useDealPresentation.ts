import { useLayoutEffect, useState } from 'react';
import type { DealStage } from '../../stores/appStore';

// Keep the original 51-flight cadence. The last local card lands at endAt.
export function dealFlightTiming(deal: DealStage, index: number, count: number) {
  const duration = Math.max(0, deal.endAt - deal.startAt);
  const flight = duration / 9;
  const start = deal.startAt + index * (duration - flight) / Math.max(1, count * 3 - 1);
  return { start, end: start + flight, flight };
}

export function dealtCount(deal: DealStage, count: number, now: number) {
  let landed = 0;
  while (landed < count && now >= dealFlightTiming(deal, landed * 3 + 2, count).end) landed++;
  return landed;
}

export function useDealPresentation(deal: DealStage | null, count: number) {
  const [clock, setClock] = useState(() => ({ deal, now: Date.now() }));
  const [reducedMotion, setReducedMotion] = useState(() => window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false);
  // A new deal (including a reconnect snapshot) must never render the previous count.
  const now = clock.deal === deal ? clock.now : Date.now();
  useLayoutEffect(() => {
    const media = window.matchMedia?.('(prefers-reduced-motion: reduce)');
    if (!media) return;
    const update = () => setReducedMotion(media.matches);
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);
  useLayoutEffect(() => {
    if (!deal) return;
    let timer: number;
    const update = () => {
      const now = Date.now();
      setClock({ deal, now });
      if (now >= deal.endAt) return;
      const landed = dealtCount(deal, count, now);
      const next = landed < count ? dealFlightTiming(deal, landed * 3 + 2, count).end : deal.endAt;
      timer = window.setTimeout(update, Math.max(1, Math.ceil(next - now)));
    };
    update();
    // Background tabs may throttle timers; seek directly on return.
    const resume = () => { window.clearTimeout(timer); update(); };
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
