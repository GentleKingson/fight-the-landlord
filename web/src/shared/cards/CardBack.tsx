const MAX_VISIBLE_CARD_BACKS = 17;

/** Props for the opponent remaining-cards stack indicator. */
export interface CardBackProps {
  count?: number;
}

/** Renders a stacked card-back badge displaying an opponent's remaining card count. */
export function CardBack({ count = 1 }: CardBackProps) {
  return (
    <div className="card-back-stack" aria-label={`剩余 ${count} 张`}>
      {Array.from({ length: Math.min(count, MAX_VISIBLE_CARD_BACKS) }, (_, index) => (
        <span key={index} className="card-back" />
      ))}
      <strong>{count}</strong>
    </div>
  );
}
