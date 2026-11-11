import type { CSSProperties, MouseEventHandler } from 'react';
import type { CardInfo } from '../../protocol/types';
import { normalizeCard, type CardSuit } from './cardModel';

const suitPaths: Record<Exclude<CardSuit, 'joker'>, string> = {
  spade: 'M12 2C9 6 3 9 3 14a5 5 0 0 0 8 4c0 2-1 3-3 4h8c-2-1-3-2-3-4a5 5 0 0 0 8-4c0-5-6-8-9-12Z',
  heart: 'M12 22 3 13C-3 6 6-2 12 5c6-7 15 1 9 8Z',
  club: 'M12 2a5 5 0 0 0-4 8 5 5 0 1 0 3 8c0 2-1 3-3 4h8c-2-1-3-2-3-4a5 5 0 1 0 3-8 5 5 0 0 0-4-8Z',
  diamond: 'M12 2 22 12 12 22 2 12Z'
};

// Paths avoid platform-dependent emoji/font fallback for Unicode suits.
function SuitIcon({ suit }: { suit: CardSuit }) {
  if (suit === 'joker') return null;
  return (
    <svg className="card__suit-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d={suitPaths[suit]} />
    </svg>
  );
}

interface CardProps {
  card: CardInfo;
  selected?: boolean;
  disabled?: boolean;
  size?: 'hand' | 'played' | 'mini' | 'action';
  style?: CSSProperties;
  onClick?: MouseEventHandler<HTMLButtonElement>;
}

export function Card({ card, selected = false, disabled = false, size = 'hand', style, onClick }: CardProps) {
  const face = normalizeCard(card);

  return (
    <button
      className={[
        'card',
        `card--${size}`,
        `card--${face.color}`,
        `card--${face.suit}`,
        selected ? 'is-selected' : '',
        face.isJoker ? 'is-joker' : ''
      ].filter(Boolean).join(' ')}
      type="button"
      disabled={disabled}
      style={style}
      onClick={onClick}
      aria-pressed={selected}
      aria-label={face.label}
    >
      {face.isJoker ? (
        <>
          <span className="card__joker-word">JOKER</span>
          <span className="card__joker-star">{face.suitSymbol}</span>
        </>
      ) : (
        <>
          <span className="card__index card__index--top">
            <b>{face.rankLabel}</b>
            <i><SuitIcon suit={face.suit} /></i>
          </span>
          <span className="card__pip" aria-hidden="true"><SuitIcon suit={face.suit} /></span>
          <span className="card__index card__index--bottom" aria-hidden="true">
            <b>{face.rankLabel}</b>
            <i><SuitIcon suit={face.suit} /></i>
          </span>
        </>
      )}
    </button>
  );
}
