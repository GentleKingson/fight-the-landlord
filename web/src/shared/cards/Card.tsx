import {
  forwardRef,
  type CSSProperties,
  type FocusEventHandler,
  type KeyboardEventHandler,
  type MouseEventHandler
} from 'react';
import type { CardInfo } from '../../protocol/types';
import { normalizeCard } from './cardModel';

interface CardProps {
  card: CardInfo;
  selected?: boolean;
  disabled?: boolean;
  size?: 'hand' | 'played' | 'mini' | 'action';
  style?: CSSProperties;
  tabIndex?: number;
  onClick?: MouseEventHandler<HTMLButtonElement>;
  onDoubleClick?: MouseEventHandler<HTMLButtonElement>;
  onFocus?: FocusEventHandler<HTMLButtonElement>;
  onKeyDown?: KeyboardEventHandler<HTMLButtonElement>;
}

export const Card = forwardRef<HTMLButtonElement, CardProps>(function Card({
  card,
  selected = false,
  disabled = false,
  size = 'hand',
  style,
  tabIndex,
  onClick,
  onDoubleClick,
  onFocus,
  onKeyDown
}, ref) {
  const face = normalizeCard(card);
  const className = [
    'card',
    `card--${size}`,
    `card--${face.color}`,
    `card--${face.suit}`,
    selected ? 'is-selected' : '',
    face.isJoker ? 'is-joker' : ''
  ].filter(Boolean).join(' ');
  const content = face.isJoker ? (
    <>
      <span className="card__joker-word">JOKER</span>
      <span className="card__joker-star">{face.suitSymbol}</span>
    </>
  ) : (
    <>
      <span className="card__index card__index--top">
        <b>{face.rankLabel}</b>
        <i>{face.suitSymbol}</i>
      </span>
      <span className="card__pip" aria-hidden="true">{face.suitSymbol}</span>
      <span className="card__index card__index--bottom" aria-hidden="true">
        <b>{face.rankLabel}</b>
        <i>{face.suitSymbol}</i>
      </span>
    </>
  );

  if (size !== 'hand') {
    return <span className={className} style={style} role="img" aria-label={face.label}>{content}</span>;
  }

  return (
    <button
      className={className}
      type="button"
      ref={ref}
      style={style}
      disabled={disabled}
      tabIndex={tabIndex}
      onClick={onClick}
      onDoubleClick={onDoubleClick}
      onFocus={onFocus}
      onKeyDown={onKeyDown}
      aria-pressed={selected}
      aria-label={face.label}
    >
      {content}
    </button>
  );
});
