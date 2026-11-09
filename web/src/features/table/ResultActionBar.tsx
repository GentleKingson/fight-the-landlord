import { useEffect, useState } from 'react';
import { FINAL_PLAY_DURATION_MS, RESULT_ACTION_GUARD_MS } from '../../shared/resultPresentation';
import { useAppStore } from '../../stores/appStore';

export function ResultActionBar() {
  const deadline = useAppStore(state => state.resultRevealAt);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (deadline === null) return;
    setNow(Date.now());
    const interval = window.setInterval(() => setNow(Date.now()), 250);
    return () => window.clearInterval(interval);
  }, [deadline]);

  const unlockAt = deadline === null ? Infinity : deadline - FINAL_PLAY_DURATION_MS + RESULT_ACTION_GUARD_MS;
  const seconds = deadline === null ? 0 : Math.max(0, Math.ceil((deadline - now) / 1000));

  return (
    <section className="action-bar action-bar--result" aria-label="本局已结束">
      <span role="timer" aria-live="off">{seconds} 秒后自动结算</span>
      <button
        className="primary-action"
        disabled={now < unlockAt}
        onClick={event => {
          // A separate component creates a fresh button (and drops play-button
          // focus). Guard rapid follow-up clicks/taps from the final play too.
          if (event.detail > 1 || Date.now() < unlockAt) return;
          const state = useAppStore.getState();
          if (state.phase === 'game_over' && state.resultRevealAt === deadline) {
            useAppStore.setState({ resultRevealAt: null });
          }
        }}
      >查看结算</button>
    </section>
  );
}
