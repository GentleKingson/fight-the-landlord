import type { CSSProperties, FocusEventHandler, KeyboardEventHandler, MouseEventHandler } from 'react';
import type { CardInfo } from '../../protocol/types';
import { normalizeCard, type CardSuit } from './cardModel';

const suitPaths: Record<Exclude<CardSuit, 'joker'>, string> = {
  spade: 'M12 2C9 6 3 9 3 14a5 5 0 0 0 8 4c0 2-1 3-3 4h8c-2-1-3-2-3-4a5 5 0 0 0 8-4c0-5-6-8-9-12Z',
  heart: 'M12 22 3 13C-3 6 6-2 12 5c6-7 15 1 9 8Z',
  club: 'M12 2a5 5 0 0 0-4 8 5 5 0 1 0 3 8c0 2-1 3-2-3-4h8c-2-1-3-2-3-4a5 5 0 1 0 3-8 5 5 0 0 0-4-8Z',
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
  as?: 'button' | 'option';
  selected?: boolean;
  disabled?: boolean;
  tabIndex?: number;
  size?: 'hand' | 'played' | 'mini' | 'action';
  style?: CSSProperties;
  onClick?: MouseEventHandler<HTMLElement>;
  onFocus?: FocusEventHandler<HTMLElement>;
  onKeyDown?: KeyboardEventHandler<HTMLElement>;
  onKeyUp?: KeyboardEventHandler<HTMLElement>;
}

export function Card({
  card,
  as = 'button',
  selected = false,
  disabled = false,
  tabIndex,
  size = 'hand',
  style,
  onClick,
  onFocus,
  onKeyDown,
  onKeyUp
}: CardProps) {
  const face = normalizeCard(card);
  const className = [
    'card',
    `card--${size}`,
    `card--${face.color}`,
    `card--${face.suit}`,
    selected ? 'is-selected' : '',
    disabled ? 'is-disabled' : '',
    face.isJoker ? 'is-joker' : ''
  ].filter(Boolean).join(' ');

  const content = face.isJoker ? (
    <>
      <span className="card__joker-word" aria-hidden="true">JOKER</span>
      <span className="card__joker-star" aria-hidden="true">{face.suitSymbol}</span>
    </>
  ) : (
    <>
      <span className="card__index card__index--top" aria-hidden="true">
        <b>{face.rankLabel}</b>
        <i><SuitIcon suit={face.suit} /></i>
      </span>
      <span className="card__pip" aria-hidden="true"><SuitIcon suit={face.suit} /></span>
      <span className="card__index card__index--bottom" aria-hidden="true">
        <b>{face.rankLabel}</b>
        <i><SuitIcon suit={face.suit} /></i>
      </span>
    </>
  );

  if (as === 'option') {
    return (
      <div
        role="option"
        className={className}
        style={style}
        tabIndex={tabIndex}
        aria-selected={selected}
        aria-disabled={disabled || undefined}
        aria-label={face.label}
        onClick={onClick as MouseEventHandler<HTMLDivElement> | undefined}
        onFocus={onFocus as FocusEventHandler<HTMLDivElement> | undefined}
        onKeyDown={onKeyDown as KeyboardEventHandler<HTMLDivElement> | undefined}
        onKeyUp={onKeyUp as KeyboardEventHandler<HTMLDivElement> | undefined}
      >
        {content}
      </div>
    );
  }

  return (
    <button
      className={className}
      type="button"
      disabled={disabled}
      tabIndex={tabIndex}
      style={style}
      onClick={onClick as MouseEventHandler<HTMLButtonElement> | undefined}
      onFocus={onFocus as FocusEventHandler<HTMLButtonElement> | undefined}
      onKeyDown={onKeyDown as KeyboardEventHandler<HTMLButtonElement> | undefined}
      onKeyUp={onKeyUp as KeyboardEventHandler<HTMLButtonElement> | undefined}
      aria-pressed={selected}
      aria-label={face.label}
    >
      {content}
    </button>
  );
}
