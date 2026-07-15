import { useAppStore, useChatStore } from './appStore';
import type { CardInfo, PlayerInfo } from '../protocol/types';
import { sortCards } from '../shared/cards/cardModel';
import { buildCardCounter, type PlayedCardLedger } from './slices/cardCounter';

const players: PlayerInfo[] = [
  { id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: false, cards_count: 16, online: true, is_bot: false },
  { id: 'p2', name: '山月', seat: 1, ready: true, is_landlord: true, cards_count: 13, online: true, is_bot: false },
  { id: 'p3', name: '松风', seat: 2, ready: true, is_landlord: false, cards_count: 15, online: true, is_bot: false }
];

const hand: CardInfo[] = [
  c(4, 17), c(4, 16), c(1, 15), c(0, 14), c(2, 14), c(1, 13), c(3, 13), c(0, 12),
  c(1, 11), c(2, 10), c(3, 10), c(0, 9), c(2, 9), c(1, 8), c(0, 7), c(3, 6), c(2, 3)
];

export function seedDemoState(mode: string): void {
  const sortedHand = sortCards(hand);
  const isBidding = mode === 'bidding';
  const demoPlayers = isBidding ? players.map((player) => ({ ...player, is_landlord: false })) : players;
  const bottomCards = [c(1, 5), c(2, 11), c(3, 15)];
  const playedCardsLedger: PlayedCardLedger = isBidding ? {} : { p3: [c(2, 8), c(3, 8)] };
  const turnDeadlineMs = Date.now() + 25_000;
  useChatStore.setState({
    buckets: {
      lobby: [demoLobbyChat('demo-lobby-chat-1', 'p3', '松风', '大厅里有人开桌吗？')],
      'game:demo-game': [
        demoGameChat('demo-chat-1', 'p2', '山月', '这局节奏很快。'),
        demoGameChat('demo-chat-2', 'p1', '青竹', '我先看一手。')
      ]
    }
  });
  useAppStore.setState({
    connected: true,
    connectionStatus: 'connected',
    pendingCommands: {},
    businessError: null,
    matchDeadlineMs: 0,
    matchPractice: false,
    phase: mode === 'lobby' ? 'lobby' : mode === 'result' ? 'game_over' : isBidding ? 'bidding' : 'playing',
    playerId: 'p1',
    playerName: '青竹',
    roomCode: '836219',
    players: demoPlayers,
    onlineCount: 128,
    hand: sortedHand,
    bottomCards,
    bottomCardsRevealed: !isBidding,
    currentTurn: isBidding ? 'p1' : 'p1',
    lastPlayed: isBidding ? [] : [c(2, 8), c(3, 8)],
    lastPlayedBy: isBidding ? '' : 'p3',
    lastPlayedName: isBidding ? '' : '松风',
    lastHandType: isBidding ? '' : '对子',
    mustPlay: false,
    canBeat: true,
    multiplier: 3,
    baseScore: 1,
    timeout: 25,
    timerStart: Date.now(),
    turnDeadlineMs,
    serverTimeMs: Date.now(),
    serverClockOffsetMs: 0,
    clockBestRttMs: Number.POSITIVE_INFINITY,
    gameId: 'demo-game',
    streamId: 'game:demo-game',
    eventVersion: 1,
    turnId: 1,
    seenGameStreams: { 'game:demo-game': 1 },
    playedCardsLedger,
    cardCounter: buildCardCounter(sortedHand, bottomCards, !isBidding, playedCardsLedger),
    seatActions: isBidding ? {
      p2: { type: 'bid', player_id: 'p2', player_name: '山月', label: '不叫' }
    } : {
      p2: { type: 'pass', player_id: 'p2', player_name: '山月', label: '不出' },
      p3: { type: 'play', player_id: 'p3', player_name: '松风', cards: [c(2, 8), c(3, 8)], hand_type: '对子' }
    },
    isGrabTurn: false,
    recentActions: isBidding ? [
      { type: 'bid', player_id: 'p2', player_name: '山月', label: '不叫' }
    ] : [
      { type: 'play', player_id: 'p3', player_name: '松风', cards: [c(2, 8), c(3, 8)], hand_type: '对子' },
      { type: 'pass', player_id: 'p2', player_name: '山月' }
    ],
    scores: [
      { player_id: 'p1', player_name: '青竹', is_landlord: false, score: 6 },
      { player_id: 'p2', player_name: '山月', is_landlord: true, score: -12 },
      { player_id: 'p3', player_name: '松风', is_landlord: false, score: 6 }
    ],
    playerHands: [
      { player_id: 'p2', player_name: '山月', cards: [c(1, 4), c(2, 4), c(3, 7)] },
      { player_id: 'p3', player_name: '松风', cards: [c(0, 5), c(2, 6)] }
    ],
    winnerId: 'p1',
    winnerName: '青竹',
    winnerIsLandlord: false,
    finalMultiplier: 3,
    settlementSyncError: ''
  });
}

function c(suit: number, rank: number): CardInfo {
  return { suit, rank, color: suit === 1 || suit === 3 || rank === 17 ? 1 : 0 };
}

function demoGameChat(messageId: string, senderId: string, senderName: string, content: string) {
  const now = Date.now();
  return {
    sender_id: senderId,
    sender_name: senderName,
    content,
    scope: 'game',
    time: Math.floor(now / 1000),
    is_system: false,
    message_id: messageId,
    room_code: '836219',
    game_id: 'demo-game',
    server_time: now
  };
}

function demoLobbyChat(messageId: string, senderId: string, senderName: string, content: string) {
  const now = Date.now();
  return {
    sender_id: senderId,
    sender_name: senderName,
    content,
    scope: 'lobby',
    time: Math.floor(now / 1000),
    is_system: false,
    message_id: messageId,
    server_time: now
  };
}
