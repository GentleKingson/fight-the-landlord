import { useAppStore } from './appStore';
import type { CardInfo, PlayerInfo } from '../protocol/types';
import { initialCounter, sortCards } from '../shared/cards/cardModel';

const players: PlayerInfo[] = [
  { id: 'p1', name: '青竹', seat: 0, ready: true, is_landlord: false, cards_count: 16, online: true },
  { id: 'p2', name: '山月', seat: 1, ready: true, is_landlord: true, cards_count: 13, online: true },
  { id: 'p3', name: '松风', seat: 2, ready: true, is_landlord: false, cards_count: 15, online: true }
];

const hand: CardInfo[] = [
  c(4, 17), c(4, 16), c(1, 15), c(0, 14), c(2, 14), c(1, 13), c(3, 13), c(0, 12),
  c(1, 11), c(2, 10), c(3, 10), c(0, 9), c(2, 9), c(1, 8), c(0, 7), c(3, 6), c(2, 3)
];

export function seedDemoState(mode: string): void {
  useAppStore.getState().leaveLocalRoom();
  const sortedHand = sortCards(hand);
  const isBidding = mode === 'bidding';
  const isWaiting = mode === 'waiting';
  const isLobbyScene = ['lobby', 'leaderboard', 'stats', 'stats-empty'].includes(mode);
  const demoPlayers = isWaiting
    ? [
      { id: 'p1', name: '青竹', seat: 0, ready: false, is_landlord: false, cards_count: 0, online: true },
      { id: 'p2', name: '山月', seat: 1, ready: true, is_landlord: false, cards_count: 0, online: true }
    ]
    : players.map(player => ({
      ...player,
      is_landlord: !isBidding && player.is_landlord,
      is_bot: mode === 'practice' && player.id !== 'p1'
    }));
  useAppStore.setState({
    connected: true,
    phase: isLobbyScene ? 'lobby' : isWaiting ? 'waiting' : isBidding ? 'bidding' : 'playing',
    lobbyPanel: mode === 'leaderboard' ? 'leaderboard' : mode === 'stats' || mode === 'stats-empty' ? 'stats' : 'home',
    playerId: 'p1',
    playerName: '青竹',
    roomCode: '836219',
    ownerId: isWaiting ? 'p1' : '',
    players: demoPlayers,
    onlineCount: 128,
    leaderboard: isLobbyScene && mode !== 'stats-empty' ? [
      { rank: 1, player_id: 'p9', player_name: '云鹤', score: 268, wins: 42, win_rate: 67.7 },
      { rank: 2, player_id: 'p8', player_name: '墨竹轩', score: 214, wins: 35, win_rate: 62.5 },
      { rank: 3, player_id: 'p1', player_name: '青竹', score: 186, wins: 29, win_rate: 60.4 },
      { rank: 4, player_id: 'p2', player_name: '山月', score: 152, wins: 24, win_rate: 55.8 },
      { rank: 5, player_id: 'p3', player_name: '松风', score: 129, wins: 21, win_rate: 52.5 },
      { rank: 6, player_id: 'p4', player_name: '听雨眠', score: 96, wins: 17, win_rate: 48.6 }
    ] : [],
    stats: isLobbyScene && mode !== 'stats-empty' ? {
      player_id: 'p1',
      player_name: '青竹',
      total_games: 48,
      wins: 29,
      losses: 19,
      win_rate: 60.4,
      landlord_games: 18,
      landlord_wins: 11,
      farmer_games: 30,
      farmer_wins: 18,
      score: 186,
      rank: 3,
      current_streak: 4,
      max_win_streak: 7
    } : null,
    hand: sortedHand,
    bottomCards: [c(1, 5), c(2, 11), c(3, 15)],
    bottomCardsRevealed: !isBidding,
    currentTurn: isBidding ? 'p1' : 'p1',
    lastPlayed: isBidding ? [] : [c(2, 8), c(3, 8)],
    lastPlayedBy: isBidding ? '' : 'p3',
    lastPlayedName: isBidding ? '' : '松风',
    lastHandType: isBidding ? '' : '对子',
    mustPlay: false,
    canBeat: true,
    multiplier: 3,
    timeout: 25,
    timerStart: Date.now(),
    cardCounter: initialCounter(sortedHand),
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
    finalMultiplier: 3
  });
  if (['result', 'reveal', 'review'].includes(mode)) {
    const state = useAppStore.getState();
    state.handleMessage({ type: 'game_over', payload: {
      winner_id: 'p3', winner_name: '松风', is_landlord: false, multiplier: 3, scores: state.scores,
      player_hands: [
        { player_id: 'p1', player_name: '青竹', cards: sortedHand },
        { player_id: 'p2', player_name: '山月', cards: [c(1, 4), c(2, 4), c(3, 7)] },
        { player_id: 'p3', player_name: '松风', cards: [] }
      ]
    } });
    if (mode === 'result') state.showSettlement();
    if (mode === 'review') state.reviewEndTable();
  }
}

function c(suit: number, rank: number): CardInfo {
  return { suit, rank, color: suit === 1 || suit === 3 || rank === 17 ? 1 : 0 };
}
