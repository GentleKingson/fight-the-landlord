import type { CardInfo, PlayerPlayedCards } from '../../protocol/generated';
import { cardKey, initialCounter } from '../../shared/cards/cardModel';

export type PlayedCardLedger = Record<string, CardInfo[]>;

export function ledgerFromSnapshot(entries: PlayerPlayedCards[] | undefined): PlayedCardLedger {
  const ledger: PlayedCardLedger = {};
  for (const entry of entries ?? []) {
    ledger[entry.player_id] = mergeUniqueCards([], entry.cards ?? []);
  }
  return ledger;
}

export function appendPlayedCards(
  ledger: PlayedCardLedger,
  playerId: string,
  cards: CardInfo[]
): PlayedCardLedger {
  return {
    ...ledger,
    [playerId]: mergeUniqueCards(ledger[playerId] ?? [], cards)
  };
}

export function buildCardCounter(
  hand: CardInfo[],
  bottomCards: CardInfo[],
  bottomCardsRevealed: boolean,
  ledger: PlayedCardLedger
): Record<number, number> {
  const known = new Map<string, CardInfo>();
  addKnown(known, hand);
  if (bottomCardsRevealed) addKnown(known, bottomCards);
  for (const cards of Object.values(ledger)) addKnown(known, cards);
  return initialCounter([...known.values()]);
}

function addKnown(known: Map<string, CardInfo>, cards: CardInfo[]): void {
  for (const card of cards) known.set(cardKey(card), card);
}

function mergeUniqueCards(current: CardInfo[], incoming: CardInfo[]): CardInfo[] {
  const cards = new Map(current.map((card) => [cardKey(card), card]));
  for (const card of incoming) cards.set(cardKey(card), card);
  return [...cards.values()];
}
