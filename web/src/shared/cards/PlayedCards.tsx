import { useEffect, useRef, useState, type CSSProperties } from 'react';
import type { CardInfo } from '../../protocol/types';
import { Card } from './Card';
import { sortPlayedCards } from './cardModel';

interface PlayedCardsProps {
  cards: CardInfo[];
  handType?: string;
  playerName?: string;
  compact?: boolean;
  isTarget?: boolean;
}

export function PlayedCards({ cards, handType, playerName, compact = false, isTarget = false }: PlayedCardsProps) {
  const rowRef = useRef<HTMLDivElement>(null);
  const [overflow, setOverflow] = useState(false);
  useEffect(() => {
    const row = rowRef.current;
    if (!row) return;
    const measure = () => setOverflow(row.scrollWidth > row.clientWidth + 1);
    const observer = new ResizeObserver(measure);
    observer.observe(row);
    window.addEventListener('resize', measure);
    row.scrollLeft = 0;
    measure();
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, [cards]);
  if (!cards.length) {
    return <div className="played-cards played-cards--empty">等待出牌</div>;
  }

  return (
    <div className={`played-cards ${compact ? 'played-cards--compact' : ''}`}>
      <div className="played-cards__meta">
        {isTarget ? <b className="played-cards__target-label">待压过</b> : null}
        {playerName ? <span title={playerName}>{playerName}</span> : null}
        {handType ? <strong title={handType}>{handType}</strong> : null}
        {overflow ? <small className="played-cards__scroll-hint">{cards.length}张 ↔</small> : null}
      </div>
      <div ref={rowRef} className={`played-cards__row${cards.length === 1 ? ' played-cards__row--single' : ''}`} tabIndex={overflow ? 0 : undefined} role="region" aria-label={overflow ? `已出 ${cards.length} 张牌，左右滑动查看` : "已出牌"} style={{ '--played-count': cards.length } as CSSProperties}>
        {sortPlayedCards(cards).map((card, index) => (
          <Card key={`${card.suit}_${card.rank}_${index}`} card={card} size="played" />
        ))}
      </div>
    </div>
  );
}
