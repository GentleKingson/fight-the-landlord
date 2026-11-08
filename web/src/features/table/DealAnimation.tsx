import { useEffect, useState, type CSSProperties } from 'react';
import { useAppStore } from '../../stores/appStore';

// CSS seeks into the original timeline on reconnect; it never advances game phase.
export function DealAnimation() {
  const deal = useAppStore(state => state.deal);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    setNow(Date.now());
    if (!deal) return;
    const timer = window.setTimeout(() => setNow(Date.now()), Math.max(0, deal.endAt - Date.now()));
    return () => window.clearTimeout(timer);
  }, [deal]);
  const finished = !deal || now >= deal.endAt;
  const duration = deal ? deal.endAt - deal.startAt : 0;
  const elapsed = deal ? Math.max(0, now - deal.startAt) : 0;
  const flight = duration / 9;
  return (
    <div className="deal-animation" role="status" aria-label="正在发牌">
      {!finished && <div className="deal-animation__cards" aria-hidden="true">
        <span className="deal-card deal-card--deck" />
        {Array.from({ length: 51 }, (_, index) => (
          <span key={`${deal!.id}-${index}`} className={`deal-card deal-card--${['left', 'right', 'me'][index % 3]}`}
            style={{ '--deal-delay': `${index * (duration - flight) / 50 - elapsed}ms`, '--deal-flight': `${flight}ms` } as CSSProperties} />
        ))}
      </div>}
      <span className="deal-animation__label">{finished ? '等待叫地主通知…' : '正在发牌…'}</span>
    </div>
  );
}
