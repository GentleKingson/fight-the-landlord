import type { CardInfo } from '../../protocol/types';

// Keep this contract aligned with internal/game/rule and testdata/classic-rules.json.
export type HandType = '单张' | '对子' | '三张' | '三带一' | '三带二' | '顺子' | '连对' |
  '飞机' | '飞机带单' | '飞机带对' | '炸弹' | '四带二' | '四带两对' | '王炸';

export interface ParsedHand {
  type: HandType;
  keyRank: number;
  length: number;
}

function groupCards(cards: CardInfo[]): Map<number, CardInfo[]> {
  const groups = new Map<number, CardInfo[]>();
  for (const card of [...cards].sort((a, b) => a.rank - b.rank || a.suit - b.suit)) {
    const group = groups.get(card.rank) ?? [];
    group.push(card);
    groups.set(card.rank, group);
  }
  return groups;
}

function continuous(ranks: number[]): boolean {
  return ranks.length > 0 && ranks.at(-1)! < 15 &&
    ranks.every((rank, index) => index === 0 || rank === ranks[index - 1] + 1);
}

export function parseHand(cards: CardInfo[]): ParsedHand | null {
  if (!cards.length) return null;
  const groups = groupCards(cards);
  if ([...groups].some(([rank, group]) => rank < 3 || rank > 17 || !Number.isInteger(rank) ||
    group.length > (rank >= 16 ? 1 : 4))) return null;
  const ranksWith = (count: number) => [...groups].filter(([, group]) => group.length === count).map(([rank]) => rank);
  const ones = ranksWith(1), pairs = ranksWith(2), trios = ranksWith(3), fours = ranksWith(4);
  const n = cards.length;
  const result = (type: HandType, keyRank: number, length = 0): ParsedHand => ({ type, keyRank, length });
  if (n === 2 && groups.has(16) && groups.has(17)) return result('王炸', 17);
  if (n === 4 && fours.length === 1) return result('炸弹', fours[0]);
  if (fours.length === 1) {
    if (n === 6 && (ones.length === 2 || pairs.length === 1)) return result('四带二', fours[0]);
    if (n === 8 && pairs.length === 2) return result('四带两对', fours[0]);
  }
  if (trios.length === 1) {
    if (n === 4 && ones.length === 1) return result('三带一', trios[0]);
    if (n === 5 && pairs.length === 1) return result('三带二', trios[0]);
  }
  if (trios.length >= 2 && continuous(trios)) {
    const length = trios.length;
    if (n === length * 3) return result('飞机', trios[0], length);
    if (n === length * 4 && ones.length + pairs.length * 2 === length) return result('飞机带单', trios[0], length);
    if (n === length * 5 && pairs.length === length) return result('飞机带对', trios[0], length);
  }
  if (n >= 5 && ones.length === n && continuous(ones)) return result('顺子', ones[0], n);
  if (pairs.length >= 3 && n === pairs.length * 2 && continuous(pairs)) return result('连对', pairs[0], pairs.length);
  if (groups.size === 1) {
    if (n === 1) return result('单张', ones[0]);
    if (n === 2) return result('对子', pairs[0]);
    if (n === 3) return result('三张', trios[0]);
  }
  return null;
}

export function canBeat(hand: ParsedHand | null, target: ParsedHand | null): boolean {
  if (!hand || !target || target.type === '王炸') return false;
  if (hand.type === '王炸') return true;
  if (hand.type === '炸弹' && target.type !== '炸弹') return true;
  return hand.type === target.type && hand.length === target.length && hand.keyRank > target.keyRank;
}

export function suggestPlays(hand: CardInfo[], lastPlayed: CardInfo[], mustPlay: boolean): CardInfo[][] {
  const groups = groupCards(hand);
  if (mustPlay || lastPlayed.length === 0) {
    const first = groups.values().next().value;
    return first ? [[first[0]]] : [];
  }
  const target = parseHand(lastPlayed);
  if (!target || target.type === '王炸') return [];
  const suggestions: CardInfo[][] = [];
  const add = (cards: CardInfo[]) => {
    if (canBeat(parseHand(cards), target)) suggestions.push(cards);
  };
  // Construct the same type first, with the smallest body rank first.
  let repeat = 1, length = target.length || 1, wings = 0, wingSize = 1;
  switch (target.type) {
    case '对子': case '连对': repeat = 2; break;
    case '三张': case '飞机': repeat = 3; break;
    case '三带一': repeat = 3; wings = 1; break;
    case '三带二': repeat = 3; wings = 1; wingSize = 2; break;
    case '飞机带单': repeat = 3; wings = length; break;
    case '飞机带对': repeat = 3; wings = length; wingSize = 2; break;
    case '炸弹': repeat = 4; break;
    case '四带二': repeat = 4; wings = 2; break;
    case '四带两对': repeat = 4; wings = 2; wingSize = 2; break;
  }
  for (const start of groups.keys()) {
    if (start <= target.keyRank || (length > 1 && start + length - 1 >= 15)) continue;
    const body: CardInfo[] = [];
    for (let rank = start; rank < start + length; rank++) {
      const group = groups.get(rank) ?? [];
      if (group.length < repeat) break;
      body.push(...group.slice(0, repeat));
    }
    if (body.length !== length * repeat) continue;
    let remaining = wings;
    for (const [rank, group] of groups) {
      if (!remaining || (rank >= start && rank < start + length)) continue;
      if (wingSize === 2) {
        if (group.length >= 2) { body.push(...group.slice(0, 2)); remaining--; }
      } else {
        const count = Math.min(group.length, 2, remaining);
        body.push(...group.slice(0, count));
        remaining -= count;
      }
    }
    if (!remaining) add(body);
  }
  if (target.type !== '炸弹') {
    for (const group of groups.values()) if (group.length === 4) add(group);
  }
  if (groups.has(16) && groups.has(17)) add([groups.get(16)![0], groups.get(17)![0]]);
  return suggestions;
}
