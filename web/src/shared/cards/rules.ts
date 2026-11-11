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

// Suits do not affect a move. Keep one deterministic set of physical cards per rank multiset.
export function handSignature(cards: CardInfo[]): string {
  return cards.map(card => card.rank).sort((a, b) => a - b).join(',');
}

const suggestionTypes: HandType[] = ['单张', '对子', '三张', '三带一', '三带二', '顺子',
  '连对', '飞机', '飞机带单', '飞机带对', '四带二', '四带两对', '炸弹', '王炸'];

export function suggestPlays(hand: CardInfo[], lastPlayed: CardInfo[], mustPlay: boolean): CardInfo[][] {
  const groups = groupCards(hand);
  const freeLead = mustPlay || lastPlayed.length === 0;
  const target = freeLead ? null : parseHand(lastPlayed);
  if (!freeLead && (!target || target.type === '王炸')) return [];
  const suggestions = new Map<string, { cards: CardInfo[]; parsed: ParsedHand; splitCost: number }>();
  const wanted = (type: HandType) => freeLead || type === target?.type || type === '炸弹' || type === '王炸';
  const add = (cards: CardInfo[]) => {
    const parsed = parseHand(cards);
    if (!parsed || (!freeLead && !canBeat(parsed, target))) return;
    const counts = groupCards(cards);
    let splitCost = 0;
    for (const [rank, used] of counts) {
      const total = groups.get(rank)!.length;
      if (used.length < total) splitCost += total === 4 ? 100 : total === 3 ? 10 : 1;
    }
    // Preserve the rocket when suggesting ordinary single cards or attachments.
    if (groups.has(16) && groups.has(17) && counts.has(16) !== counts.has(17)) splitCost += 50;
    suggestions.set(handSignature(cards), { cards: [...cards].sort((a, b) => a.rank - b.rank || a.suit - b.suit), parsed, splitCost });
  };

  // Choose wings by rank counts, not physical-card subsets. Single wings may include pairs,
  // but never three/four cards of one rank under this project's parseHand contract.
  const withWings = (body: CardInfo[], count: number, size: 1 | 2) => {
    if (body.length + count * size > hand.length) return;
    const bodyRanks = new Set(body.map(card => card.rank));
    const available = [...groups].filter(([rank, cards]) => !bodyRanks.has(rank) && cards.length >= size);
    const capacity = available.map(([, cards]) => size === 2 ? 1 : Math.min(cards.length, 2));
    const suffix = new Array<number>(capacity.length + 1).fill(0);
    for (let i = capacity.length - 1; i >= 0; i--) suffix[i] = suffix[i + 1] + capacity[i];
    const wings: CardInfo[] = [];
    const choose = (index: number, remaining: number) => {
      if (!remaining) { add([...body, ...wings]); return; }
      if (remaining > suffix[index]) return;
      const cards = available[index][1];
      for (let take = 0; take <= Math.min(capacity[index], remaining); take++) {
        wings.push(...cards.slice(0, take * size));
        choose(index + 1, remaining - take);
        wings.length -= take * size;
      }
    };
    choose(0, count);
  };

  for (const cards of groups.values()) {
    if (wanted('单张')) add(cards.slice(0, 1));
    if (cards.length >= 2 && wanted('对子')) add(cards.slice(0, 2));
    if (cards.length >= 3) {
      const trio = cards.slice(0, 3);
      if (wanted('三张')) add(trio);
      if (wanted('三带一')) withWings(trio, 1, 1);
      if (wanted('三带二')) withWings(trio, 1, 2);
    }
    if (cards.length === 4) {
      add(cards);
      if (wanted('四带二')) withWings(cards, 2, 1);
      if (wanted('四带两对')) withWings(cards, 2, 2);
    }
  }
  // Enumerate each contiguous body and permitted length; a reply only needs the target length.
  for (const repeat of [1, 2, 3]) {
    const type: HandType = repeat === 1 ? '顺子' : repeat === 2 ? '连对' : '飞机';
    if (!wanted(type) && !(repeat === 3 && (wanted('飞机带单') || wanted('飞机带对')))) continue;
    const minLength = repeat === 1 ? 5 : repeat === 2 ? 3 : 2;
    for (const start of groups.keys()) {
      const body: CardInfo[] = [];
      for (let rank = start; rank < 15; rank++) {
        const cards = groups.get(rank);
        if (!cards || cards.length < repeat) break;
        body.push(...cards.slice(0, repeat));
        const length = rank - start + 1;
        if (!freeLead && length > target!.length) break;
        if (length < minLength || (!freeLead && length !== target!.length)) continue;
        if (wanted(type)) add(body);
        if (repeat === 3) {
          if (wanted('飞机带单')) withWings(body, length, 1);
          if (wanted('飞机带对')) withWings(body, length, 2);
        }
      }
    }
  }
  if (groups.has(16) && groups.has(17)) add([groups.get(16)![0], groups.get(17)![0]]);
  return [...suggestions.values()].sort((a, b) => {
    const order = suggestionTypes.indexOf(a.parsed.type) - suggestionTypes.indexOf(b.parsed.type) ||
      a.splitCost - b.splitCost || a.parsed.keyRank - b.parsed.keyRank || a.parsed.length - b.parsed.length;
    if (order) return order;
    for (let i = 0; i < Math.min(a.cards.length, b.cards.length); i++) {
      if (a.cards[i].rank !== b.cards[i].rank) return a.cards[i].rank - b.cards[i].rank;
    }
    return a.cards.length - b.cards.length;
  }).map(suggestion => suggestion.cards);
}
