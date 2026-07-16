import type { CardInfo } from '../protocol/types';

export const HandType = {
  Single: 'single',
  Pair: 'pair',
  Trio: 'trio',
  TrioWithSingle: 'trio_with_single',
  TrioWithPair: 'trio_with_pair',
  Straight: 'straight',
  PairStraight: 'pair_straight',
  Plane: 'plane',
  PlaneWithSingles: 'plane_with_singles',
  PlaneWithPairs: 'plane_with_pairs',
  Bomb: 'bomb',
  FourWithTwo: 'four_with_two',
  FourWithTwoPairs: 'four_with_two_pairs',
  Rocket: 'rocket'
} as const;

export type HandType = (typeof HandType)[keyof typeof HandType];

export interface ParsedHand {
  type: HandType;
  keyRank: number;
  length: number;
  cards: CardInfo[];
}

export type HandValue = ParsedHand | readonly CardInfo[];
export type HandComparison = -1 | 0 | 1 | null;

export const handTypeLabels: Readonly<Record<HandType, string>> = {
  [HandType.Single]: '单张',
  [HandType.Pair]: '对子',
  [HandType.Trio]: '三张',
  [HandType.TrioWithSingle]: '三带一',
  [HandType.TrioWithPair]: '三带二',
  [HandType.Straight]: '顺子',
  [HandType.PairStraight]: '连对',
  [HandType.Plane]: '飞机',
  [HandType.PlaneWithSingles]: '飞机带单',
  [HandType.PlaneWithPairs]: '飞机带对',
  [HandType.Bomb]: '炸弹',
  [HandType.FourWithTwo]: '四带二',
  [HandType.FourWithTwoPairs]: '四带两对',
  [HandType.Rocket]: '王炸'
};

interface HandAnalysis {
  counts: ReadonlyMap<number, number>;
  fours: number[];
  trios: number[];
  pairs: number[];
  ones: number[];
}

interface RankGroup {
  rank: number;
  cards: CardInfo[];
}

const sequenceTypes = new Set<HandType>([
  HandType.Straight,
  HandType.PairStraight,
  HandType.Plane,
  HandType.PlaneWithSingles,
  HandType.PlaneWithPairs
]);

const leadTypeOrder: Readonly<Record<HandType, number>> = {
  [HandType.Single]: 0,
  [HandType.Pair]: 1,
  [HandType.Trio]: 2,
  [HandType.TrioWithSingle]: 3,
  [HandType.TrioWithPair]: 4,
  [HandType.Straight]: 5,
  [HandType.PairStraight]: 6,
  [HandType.Plane]: 7,
  [HandType.PlaneWithSingles]: 8,
  [HandType.PlaneWithPairs]: 9,
  [HandType.FourWithTwo]: 10,
  [HandType.FourWithTwoPairs]: 11,
  [HandType.Bomb]: 12,
  [HandType.Rocket]: 13
};

/** Parse one selected play using the same shape rules and priority as Go ParseHand. */
export function parseHand(cards: readonly CardInfo[]): ParsedHand | null {
  if (cards.length === 0) return null;

  const analysis = analyzeCards(cards);

  if (
    cards.length === 2
    && analysis.counts.get(16) === 1
    && analysis.counts.get(17) === 1
  ) {
    return parsed(HandType.Rocket, 17, 0, cards);
  }

  if (cards.length === 4 && analysis.fours.length === 1) {
    return parsed(HandType.Bomb, analysis.fours[0], 0, cards);
  }

  if (analysis.fours.length === 1) {
    const keyRank = analysis.fours[0];
    if (cards.length === 6 && (analysis.ones.length === 2 || analysis.pairs.length === 1)) {
      return parsed(HandType.FourWithTwo, keyRank, 0, cards);
    }
    if (cards.length === 8 && analysis.pairs.length === 2) {
      return parsed(HandType.FourWithTwoPairs, keyRank, 0, cards);
    }
  }

  if (analysis.trios.length === 1) {
    const keyRank = analysis.trios[0];
    if (cards.length === 4 && analysis.ones.length === 1) {
      return parsed(HandType.TrioWithSingle, keyRank, 0, cards);
    }
    if (cards.length === 5 && analysis.pairs.length === 1) {
      return parsed(HandType.TrioWithPair, keyRank, 0, cards);
    }
  }

  const planeLength = analysis.trios.length;
  if (planeLength >= 2 && isContinuous(analysis.trios)) {
    const keyRank = analysis.trios[0];
    if (cards.length === planeLength * 3) {
      return parsed(HandType.Plane, keyRank, planeLength, cards);
    }
    if (cards.length === planeLength * 4 && analysis.ones.length === planeLength) {
      return parsed(HandType.PlaneWithSingles, keyRank, planeLength, cards);
    }
    if (cards.length === planeLength * 5 && analysis.pairs.length === planeLength) {
      return parsed(HandType.PlaneWithPairs, keyRank, planeLength, cards);
    }
  }

  if (
    cards.length >= 5
    && analysis.ones.length === cards.length
    && isContinuous(analysis.ones)
  ) {
    return parsed(HandType.Straight, analysis.ones[0], cards.length, cards);
  }

  if (
    analysis.pairs.length >= 3
    && analysis.pairs.length * 2 === cards.length
    && isContinuous(analysis.pairs)
  ) {
    return parsed(HandType.PairStraight, analysis.pairs[0], analysis.pairs.length, cards);
  }

  if (analysis.counts.size === 1) {
    if (cards.length === 1) return parsed(HandType.Single, analysis.ones[0], 0, cards);
    if (cards.length === 2) return parsed(HandType.Pair, analysis.pairs[0], 0, cards);
    if (cards.length === 3) return parsed(HandType.Trio, analysis.trios[0], 0, cards);
  }

  return null;
}

/**
 * Compare two valid hands. `null` means the shapes are not comparable; otherwise
 * the result is negative, zero, or positive in the same sense as a sort comparator.
 */
export function compareHands(a: HandValue, b: HandValue): HandComparison {
  const left = resolveHand(a);
  const right = resolveHand(b);
  if (!left || !right) return null;

  if (left.type === HandType.Rocket || right.type === HandType.Rocket) {
    if (left.type === right.type) return 0;
    return left.type === HandType.Rocket ? 1 : -1;
  }

  if (left.type === HandType.Bomb || right.type === HandType.Bomb) {
    if (left.type !== right.type) return left.type === HandType.Bomb ? 1 : -1;
    return compareNumbers(left.keyRank, right.keyRank);
  }

  if (left.type !== right.type) return null;
  if (sequenceTypes.has(left.type) && left.length !== right.length) return null;
  return compareNumbers(left.keyRank, right.keyRank);
}

/** A valid candidate may lead an empty trick, otherwise it must strictly beat the previous hand. */
export function canBeat(candidate: HandValue, previous?: HandValue | null): boolean {
  const next = resolveHand(candidate);
  if (!next) return false;
  if (isEmptyPrevious(previous)) return true;

  const last = resolveHand(previous);
  return last !== null && compareParsedHands(next, last) === 1;
}

/**
 * Return every legal logical response in deterministic order. Suit-only variants
 * of the same rank multiset are equivalent, so only one physical realization is
 * returned for each rank multiset.
 */
export function listLegalResponses(
  hand: readonly CardInfo[],
  previous?: HandValue | null
): CardInfo[][] {
  if (hand.length === 0) return [];

  const leading = isEmptyPrevious(previous);
  const previousHand = leading ? null : resolveHand(previous);
  if (!leading && previousHand === null) return [];
  if (previousHand?.type === HandType.Rocket) return [];

  const groups = groupCardsByRank(hand);
  const selectedCounts = new Array<number>(groups.length).fill(0);
  const allowedLengths = previousHand ? responseLengths(previousHand) : null;
  const maxLength = allowedLengths ? Math.max(...allowedLengths) : hand.length;
  const suffixCardCounts = buildSuffixCardCounts(groups);
  const responses: Array<{ cards: CardInfo[]; parsed: ParsedHand }> = [];

  const visit = (groupIndex: number, selectedTotal: number): void => {
    if (selectedTotal > maxLength) return;
    if (allowedLengths && !canReachAllowedLength(allowedLengths, selectedTotal, suffixCardCounts[groupIndex])) {
      return;
    }

    if (groupIndex === groups.length) {
      if (selectedTotal === 0 || (allowedLengths && !allowedLengths.has(selectedTotal))) return;

      const candidate = materializeCandidate(groups, selectedCounts);
      const candidateHand = parseHand(candidate);
      if (!candidateHand) return;
      if (previousHand && compareParsedHands(candidateHand, previousHand) !== 1) return;
      responses.push({ cards: candidate, parsed: candidateHand });
      return;
    }

    const available = groups[groupIndex].cards.length;
    for (let count = 0; count <= available && selectedTotal + count <= maxLength; count += 1) {
      selectedCounts[groupIndex] = count;
      visit(groupIndex + 1, selectedTotal + count);
    }
    selectedCounts[groupIndex] = 0;
  };

  visit(0, 0);
  responses.sort((a, b) => compareResponses(a, b, previousHand));
  return responses.map((response) => response.cards);
}

export function findSmallestLegalResponse(
  hand: readonly CardInfo[],
  previous?: HandValue | null
): CardInfo[] | null {
  if (hand.length === 0) return null;
  if (isEmptyPrevious(previous)) {
    return [[...hand].sort((a, b) => a.rank - b.rank || comparePhysicalCards(a, b))[0]];
  }
  return listLegalResponses(hand, previous)[0] ?? null;
}

function parsed(
  type: HandType,
  keyRank: number,
  length: number,
  cards: readonly CardInfo[]
): ParsedHand {
  return { type, keyRank, length, cards: [...cards] };
}

function analyzeCards(cards: readonly CardInfo[]): HandAnalysis {
  const counts = new Map<number, number>();
  for (const card of cards) counts.set(card.rank, (counts.get(card.rank) ?? 0) + 1);

  const fours: number[] = [];
  const trios: number[] = [];
  const pairs: number[] = [];
  const ones: number[] = [];
  for (const [rank, count] of counts) {
    if (count === 4) fours.push(rank);
    else if (count === 3) trios.push(rank);
    else if (count === 2) pairs.push(rank);
    else if (count === 1) ones.push(rank);
  }

  const ascending = (a: number, b: number): number => a - b;
  fours.sort(ascending);
  trios.sort(ascending);
  pairs.sort(ascending);
  ones.sort(ascending);
  return { counts, fours, trios, pairs, ones };
}

function isContinuous(ranks: readonly number[]): boolean {
  if (ranks.length === 0) return false;
  for (let index = 0; index < ranks.length; index += 1) {
    if (ranks[index] >= 15) return false;
    if (index > 0 && ranks[index] !== ranks[index - 1] + 1) return false;
  }
  return true;
}

function resolveHand(value: HandValue): ParsedHand | null {
  return isParsedHand(value) ? value : parseHand(value);
}

function isParsedHand(value: HandValue): value is ParsedHand {
  return 'type' in value;
}

function isEmptyPrevious(previous?: HandValue | null): previous is null | undefined | readonly CardInfo[] {
  return previous === null || previous === undefined || (Array.isArray(previous) && previous.length === 0);
}

function compareParsedHands(a: ParsedHand, b: ParsedHand): HandComparison {
  if (a.type === HandType.Rocket || b.type === HandType.Rocket) {
    if (a.type === b.type) return 0;
    return a.type === HandType.Rocket ? 1 : -1;
  }
  if (a.type === HandType.Bomb || b.type === HandType.Bomb) {
    if (a.type !== b.type) return a.type === HandType.Bomb ? 1 : -1;
    return compareNumbers(a.keyRank, b.keyRank);
  }
  if (a.type !== b.type) return null;
  if (sequenceTypes.has(a.type) && a.length !== b.length) return null;
  return compareNumbers(a.keyRank, b.keyRank);
}

function compareNumbers(a: number, b: number): -1 | 0 | 1 {
  if (a === b) return 0;
  return a > b ? 1 : -1;
}

function groupCardsByRank(hand: readonly CardInfo[]): RankGroup[] {
  const cardsByRank = new Map<number, CardInfo[]>();
  for (const card of hand) {
    const group = cardsByRank.get(card.rank) ?? [];
    group.push(card);
    cardsByRank.set(card.rank, group);
  }

  return [...cardsByRank.entries()]
    .sort(([leftRank], [rightRank]) => leftRank - rightRank)
    .map(([rank, cards]) => ({
      rank,
      cards: [...cards].sort(comparePhysicalCards)
    }));
}

function responseLengths(previous: ParsedHand): Set<number> {
  if (previous.type === HandType.Rocket) return new Set<number>();
  return new Set([previous.cards.length, 2, 4]);
}

function buildSuffixCardCounts(groups: readonly RankGroup[]): number[] {
  const suffix = new Array<number>(groups.length + 1).fill(0);
  for (let index = groups.length - 1; index >= 0; index -= 1) {
    suffix[index] = suffix[index + 1] + groups[index].cards.length;
  }
  return suffix;
}

function canReachAllowedLength(
  allowedLengths: ReadonlySet<number>,
  selectedTotal: number,
  remainingCards: number
): boolean {
  for (const length of allowedLengths) {
    if (length >= selectedTotal && length <= selectedTotal + remainingCards) return true;
  }
  return false;
}

function materializeCandidate(groups: readonly RankGroup[], selectedCounts: readonly number[]): CardInfo[] {
  const cards: CardInfo[] = [];
  for (let index = 0; index < groups.length; index += 1) {
    cards.push(...groups[index].cards.slice(0, selectedCounts[index]));
  }
  return cards.sort(comparePhysicalCardsForPlay);
}

function comparePhysicalCards(a: CardInfo, b: CardInfo): number {
  return a.suit - b.suit || a.color - b.color;
}

function comparePhysicalCardsForPlay(a: CardInfo, b: CardInfo): number {
  return b.rank - a.rank || comparePhysicalCards(a, b);
}

function compareResponses(
  a: { cards: CardInfo[]; parsed: ParsedHand },
  b: { cards: CardInfo[]; parsed: ParsedHand },
  previous: ParsedHand | null
): number {
  const priorityDifference = responsePriority(a.parsed, previous) - responsePriority(b.parsed, previous);
  if (priorityDifference !== 0) return priorityDifference;

  const rankDifference = a.parsed.keyRank - b.parsed.keyRank;
  if (rankDifference !== 0) return rankDifference;

  const lengthDifference = a.cards.length - b.cards.length;
  if (lengthDifference !== 0) return lengthDifference;

  const aRanks = a.cards.map((card) => card.rank).sort((left, right) => left - right);
  const bRanks = b.cards.map((card) => card.rank).sort((left, right) => left - right);
  for (let index = 0; index < aRanks.length; index += 1) {
    if (aRanks[index] !== bRanks[index]) return aRanks[index] - bRanks[index];
  }
  return 0;
}

function responsePriority(hand: ParsedHand, previous: ParsedHand | null): number {
  if (!previous) return leadTypeOrder[hand.type];
  if (hand.type === previous.type) return 0;
  if (hand.type === HandType.Bomb) return 1;
  if (hand.type === HandType.Rocket) return 2;
  return 3;
}
