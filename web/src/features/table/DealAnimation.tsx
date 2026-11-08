import { useLayoutEffect, useRef } from 'react';
import type { DealStage } from '../../stores/appStore';
import { dealFlightTiming } from './useDealPresentation';

// The overlay spans the table so the arena cannot clip flights to the hand.
// Only geometry is measured; transform/opacity do all per-frame work in CSS.
export function DealAnimation({ deal, count, finished, reducedMotion }: {
  deal: DealStage | null; count: number; finished: boolean; reducedMotion: boolean;
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const root = rootRef.current;
    const table = root?.closest('.table-screen');
    const origin = table?.querySelector('.deal-origin');
    if (!root || !table || !origin || !deal || finished) return;
    const flights = [...root.querySelectorAll<HTMLElement>('[data-flight-index]')];
    const slots = [...table.querySelectorAll<HTMLElement>('.hand__slot')];
    const seats = ['left', 'right'].map(side => table.querySelector(`.seat-panel--${side}`));
    const measure = () => {
      const bounds = root.getBoundingClientRect();
      const source = origin.getBoundingClientRect();
      const x = source.left + source.width / 2;
      const y = source.top + source.height / 2;
      // Read all targets before writing styles to avoid repeated layout work.
      const targets = [...seats, ...slots].map(element => element?.getBoundingClientRect());
      root.style.setProperty('--deal-origin-x', `${x - bounds.left - 15}px`);
      root.style.setProperty('--deal-origin-y', `${y - bounds.top - 21}px`);
      flights.forEach((card, index) => {
        const target = targets[index % 3 === 2 ? Math.floor(index / 3) + 2 : index % 3];
        if (!target) return;
        card.style.setProperty('--deal-x', `${target.left + target.width / 2 - x}px`);
        card.style.setProperty('--deal-y', `${target.top + target.height / 2 - y}px`);
        card.style.setProperty('--deal-scale-x', String(index % 3 === 2 ? target.width / 30 : 1));
        card.style.setProperty('--deal-scale-y', String(index % 3 === 2 ? target.height / 42 : 1));
      });
    };
    measure();
    const elapsedAtMount = Date.now();
    flights.forEach((card, index) => {
      const timing = dealFlightTiming(deal, index, count);
      card.style.setProperty('--deal-delay', `${timing.start - elapsedAtMount}ms`);
      card.style.setProperty('--deal-flight', `${timing.flight}ms`);
    });
    root.classList.add('is-ready');
    // Hand's ResizeObserver recomputes rank spacing in React. Measure on the next
    // frame so orientation changes use those committed positions, not the old ones.
    let frame = 0;
    const scheduleMeasure = () => {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(measure);
    };
    const observer = new ResizeObserver(scheduleMeasure);
    [table, origin, ...slots, ...seats].forEach(element => { if (element) observer.observe(element); });
    window.addEventListener('resize', scheduleMeasure);
    return () => {
      observer.disconnect();
      window.cancelAnimationFrame(frame);
      window.removeEventListener('resize', scheduleMeasure);
      root.classList.remove('is-ready');
    };
  }, [deal, count, finished, reducedMotion]);
  return (
    <div ref={rootRef} className="deal-animation" role="status" aria-label="正在发牌">
      {!finished && <div aria-hidden="true">
        <span className="deal-card deal-card--deck" />
        {!reducedMotion && Array.from({ length: count * 3 }, (_, index) => (
          <span key={`${deal?.id}-${index}`} data-flight-index={index}
            className={`deal-card deal-card--${['left', 'right', 'me'][index % 3]}`} />
        ))}
      </div>}
    </div>
  );
}
