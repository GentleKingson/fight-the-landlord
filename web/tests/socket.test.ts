import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { GameSocket } from '../src/transport/wsClient';
import { encodeMessage, decodeMessage } from '../src/protocol/codec';
import { useAppStore } from '../src/stores/appStore';
import { connectGameAudio } from '../src/audio/connectGameAudio';
import type { GameAudio } from '../src/audio/gameAudio';

class FakeSocket {
  static OPEN = 1;
  static latest: FakeSocket;
  readyState = 1;
  sent: Uint8Array[] = [];
  onopen?: () => void;
  onmessage?: (event: { data: Uint8Array }) => void;
  onclose?: (event: { code: number }) => void;
  constructor() { FakeSocket.latest = this; }
  send(data: Uint8Array) {
    this.sent.push(data);
    const message = decodeMessage(data);
    if (message.type === 'ping') this.receive('pong', { client_timestamp: (message.payload as { timestamp: number }).timestamp, server_timestamp: Date.now() });
  }
  close(code = 1000) { this.onclose?.({ code }); }
  receive(type: string, payload: Record<string, unknown>) { this.onmessage?.({ data: encodeMessage(type, payload) }); }
}

beforeEach(() => { sessionStorage.clear(); vi.stubGlobal('WebSocket', FakeSocket); });

const sentTypes = (socket: FakeSocket) => socket.sent.map((data) => decodeMessage(data).type);

test('opening queries online count immediately and applies online_count responses', () => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  const transport = FakeSocket.latest;
  expect(transport.sent).toHaveLength(0);
  expect(vi.getTimerCount()).toBe(1);
  transport.onopen?.();
  expect(sentTypes(transport)).toEqual(['ping', 'get_online_count', 'get_maintenance_status']);
  expect(vi.getTimerCount()).toBe(1);
  for (const count of [12, 8, 0]) {
    transport.receive('online_count', { count });
    expect(useAppStore.getState().onlineCount).toBe(count);
  }
  socket.close();
});

test('queries online count every two five-second heartbeats using one timer', () => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  const transport = FakeSocket.latest;
  transport.onopen?.();
  transport.sent = [];
  for (let cycle = 0; cycle < 3; cycle++) {
    vi.advanceTimersByTime(5000);
    expect(sentTypes(transport)).toEqual(['ping']);
    expect(decodeMessage(transport.sent[0]).payload).toMatchObject({ timestamp: Date.now() });
    vi.advanceTimersByTime(4999);
    expect(sentTypes(transport)).toEqual(['ping']);
    vi.advanceTimersByTime(1);
    expect(sentTypes(transport)).toEqual(['ping', 'ping', 'get_online_count']);
    expect(vi.getTimerCount()).toBe(1);
    transport.sent = [];
  }
  socket.close();
});

test.each(['manual', 'replaced', 'network'])('%s disconnect stops heartbeat queries and close cancels retries', (reason) => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  const transport = FakeSocket.latest;
  transport.onopen?.();
  vi.advanceTimersByTime(5000);
  if (reason === 'manual') socket.close();
  else transport.close(reason === 'replaced' ? 4001 : 1006);
  expect(useAppStore.getState().connected).toBe(false);
  expect(vi.getTimerCount()).toBe(reason === 'network' ? 1 : 0);
  const messagesBeforeClose = sentTypes(transport);
  socket.close();
  expect(vi.getTimerCount()).toBe(0);
  vi.advanceTimersByTime(20000);
  expect(sentTypes(transport)).toEqual(messagesBeforeClose);
  expect(FakeSocket.latest).toBe(transport);
});

test('reconnect queries immediately and restarts the full heartbeat cycle without duplicate timers', () => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  let transport = FakeSocket.latest;
  transport.onopen?.();
  for (let cycle = 0; cycle < 2; cycle++) {
    vi.advanceTimersByTime(5000);
    transport.close(1006);
    const previous = transport;
    const previousMessages = sentTypes(previous);
    vi.advanceTimersByTime(2500 * 2 ** cycle);
    transport = FakeSocket.latest;
    expect(transport).not.toBe(previous);
    vi.advanceTimersByTime(10000);
    expect(sentTypes(previous)).toEqual(previousMessages);
    expect(transport.sent).toHaveLength(0);
    expect(vi.getTimerCount()).toBe(1);
    transport.onopen?.();
    socket.connect();
    expect(FakeSocket.latest).toBe(transport);
    expect(sentTypes(transport)).toEqual(['ping', 'get_online_count', 'get_maintenance_status']);
    expect(vi.getTimerCount()).toBe(1);
    transport.sent = [];
    vi.advanceTimersByTime(5000);
    expect(sentTypes(transport)).toEqual(['ping']);
    vi.advanceTimersByTime(5000);
    expect(sentTypes(transport)).toEqual(['ping', 'ping', 'get_online_count']);
    expect(vi.getTimerCount()).toBe(1);
  }
  socket.close();
  expect(vi.getTimerCount()).toBe(0);
});

test('reload relies on server cookie authentication and removes legacy JS credentials', () => {
  sessionStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'old', token: 'old-token' }));
  const socket = new GameSocket('ws://localhost/ws');
  for (let i = 0; i < 2; i++) {
    socket.connect();
    const transport = FakeSocket.latest;
    transport.onopen?.();
    expect(sentTypes(transport)).not.toContain('reconnect');
    transport.receive('connected', { player_id: 'old', player_name: 'Player' });
    transport.receive('reconnected', { player_id: 'old', player_name: 'Player' });
    expect(useAppStore.getState().playerId).toBe('old');
    expect(sessionStorage.getItem('ddz_next_reconnect')).toBeNull();
    socket.close();
  }
});

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); vi.unstubAllGlobals(); });

test('a replaced tab stops reconnecting while network failures still retry', () => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  const first = FakeSocket.latest;
  first.onopen?.();
  first.close(1006);
  vi.advanceTimersByTime(2500);
  const second = FakeSocket.latest;
  expect(second).not.toBe(first);
  second.onopen?.();
  second.close(4001);
  vi.advanceTimersByTime(15000);
  expect(FakeSocket.latest).toBe(second);
  expect(useAppStore.getState().connected).toBe(false);
  expect(useAppStore.getState().error).toContain('本页面已停止重连');
  socket.close();
});

test('audio only follows confirmed live plays, never sends, errors, snapshots or remounts', () => {
  const audio = { play: vi.fn(), stop: vi.fn(), unlock: vi.fn(), endDeal: vi.fn(), endTurn: vi.fn(), subscribe: vi.fn(() => () => {}) };
  const socket = new GameSocket('ws://localhost/ws');
  let dispose = connectGameAudio(socket, audio as unknown as GameAudio);
  socket.connect();
  FakeSocket.latest.onopen?.();
  useAppStore.setState({ phase: 'playing', roomCode: '123456', playerId: 'me' });
  const cards = [{ rank: 3, suit: 0, color: 0 }];
  socket.send('play_cards', { cards });
  FakeSocket.latest.receive('error', { code: 1, message: '非法出牌' });
  expect(audio.play).not.toHaveBeenCalled();
  for (const player_id of ['me', 'opponent']) {
    FakeSocket.latest.receive('card_played', { player_id, cards, hand_type: '单张', cards_left: 16 });
  }
  expect(audio.play).toHaveBeenCalledTimes(2);
  expect(audio.play).toHaveBeenLastCalledWith('单张', cards, 16);
  FakeSocket.latest.receive('reconnected', { player_id: 'me', room_code: '123456', game_state: {
    phase: 'playing', last_played: cards, last_player_id: 'opponent',
  } });
  dispose();
  dispose = connectGameAudio(socket, audio as unknown as GameAudio);
  useAppStore.setState({ drawer: 'history' });
  expect(audio.play).toHaveBeenCalledTimes(2);
  // Rebinding does not leave duplicate listeners behind.
  FakeSocket.latest.receive('card_played', { player_id: 'me', cards, hand_type: '单张', cards_left: 15 });
  expect(audio.play).toHaveBeenCalledTimes(3);
  audio.stop.mockClear();
  useAppStore.getState().leaveLocalRoom();
  expect(audio.stop).toHaveBeenCalledOnce();
  FakeSocket.latest.receive('card_played', { cards, hand_type: '单张' });
  expect(audio.play).toHaveBeenCalledTimes(3);
  dispose();
  socket.close();
});

test('audio clears on backgrounding/disconnect and gesture listeners are removed on cleanup', () => {
  const audio = { play: vi.fn(), stop: vi.fn(), unlock: vi.fn(), endDeal: vi.fn(), endTurn: vi.fn(), subscribe: vi.fn(() => () => {}) };
  const socket = new GameSocket('ws://localhost/ws');
  const dispose = connectGameAudio(socket, audio as unknown as GameAudio);
  document.dispatchEvent(new Event('pointerup'));
  document.dispatchEvent(new Event('keydown'));
  expect(audio.unlock).toHaveBeenCalledTimes(2);
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
  document.dispatchEvent(new Event('visibilitychange'));
  expect(audio.stop).toHaveBeenCalled();
  audio.stop.mockClear();
  useAppStore.getState().setConnected(false);
  expect(audio.stop).toHaveBeenCalled();
  dispose();
  document.dispatchEvent(new Event('pointerup'));
  expect(audio.unlock).toHaveBeenCalledTimes(2);
  vi.restoreAllMocks();
});

function audioSession() {
  const audio = {
    play: vi.fn(), bid: vi.fn(), pass: vi.fn(), deal: vi.fn(), turn: vi.fn(), result: vi.fn(),
    stop: vi.fn(), endDeal: vi.fn(), endTurn: vi.fn(), unlock: vi.fn(), getEnabled: vi.fn(() => true),
    subscribe: vi.fn((_listener: () => void) => () => {}),
  };
  const socket = new GameSocket('ws://localhost/ws');
  const dispose = connectGameAudio(socket, audio as unknown as GameAudio);
  socket.connect();
  FakeSocket.latest.onopen?.();
  FakeSocket.latest.receive('connected', { player_id: 'me', player_name: 'Me', reconnect_token: 'token' });
  useAppStore.setState({ roomCode: '123456', phase: 'bidding' });
  const receive = (type: string, payload: Record<string, unknown> = {}) => FakeSocket.latest.receive(type, payload);
  return { audio, socket, receive, close: () => { dispose(); socket.close(); } };
}

const testCards = (count: number) => Array.from({ length: count }, (_, i) => ({ rank: i % 13 + 3, suit: 0, color: 0 }));

test('bid/pass sounds require server confirmation and dispatch every call/grab outcome', () => {
  const { audio, socket, receive, close } = audioSession();
  for (const type of ['bid', 'pass']) {
    socket.send(type, { bid: true });
    receive('error', { code: 1, message: '操作失败' });
  }
  expect(audio.bid).not.toHaveBeenCalled();
  expect(audio.pass).not.toHaveBeenCalled();
  for (const bid of [true, false]) for (const is_grab of [true, false]) {
    receive('bid_result', { player_id: 'me', bid, is_grab });
    expect(audio.bid).toHaveBeenLastCalledWith(bid, is_grab);
  }
  receive('player_pass', { player_id: 'opponent' });
  expect(audio.pass).toHaveBeenCalledOnce();
  close();
});

test('deals once per game and does not repeat when the landlord receives bottom cards', () => {
  const { audio, receive, close } = audioSession();
  receive('game_start');
  receive('deal_cards', { cards: testCards(17) });
  receive('deal_cards', { cards: testCards(17) });
  receive('landlord', { player_id: 'me', bottom_cards: testCards(3) });
  receive('deal_cards', { cards: testCards(20) });
  expect(audio.deal).toHaveBeenCalledOnce();
  receive('game_start');
  receive('deal_cards', { cards: testCards(17) });
  expect(audio.deal).toHaveBeenCalledTimes(2);
  close();
});

test.each([
  ['call', 'bid_turn', false],
  ['grab', 'bid_turn', true],
  ['play', 'play_turn', false],
] as const)('%s starts silently and warns only the local player once at five seconds', (_stage, type, is_grab) => {
  vi.useFakeTimers();
  const { audio, receive, close } = audioSession();
  receive(type, { player_id: 'opponent', timeout: 10, is_grab });
  vi.advanceTimersByTime(10000);
  expect(audio.turn).not.toHaveBeenCalled();
  const deadline = Date.now() + 10000;
  receive(type, { player_id: 'me', timeout: 10, is_grab });
  expect(audio.turn).not.toHaveBeenCalled();
  vi.advanceTimersByTime(4999);
  expect(audio.turn).not.toHaveBeenCalled();
  vi.advanceTimersByTime(1);
  expect(audio.turn).toHaveBeenCalledExactlyOnceWith(true, deadline);
  vi.advanceTimersByTime(4999);
  expect(audio.turn).toHaveBeenCalledOnce();
  audio.endTurn.mockClear();
  vi.advanceTimersByTime(1);
  expect(audio.endTurn).toHaveBeenCalledOnce();
  close();
});

test('call, grab and counter-grab start silently while confirmed bids and warnings still play', () => {
  vi.useFakeTimers();
  const { audio, receive, close } = audioSession();
  for (const [player_id, is_grab] of [['me', false], ['opponent', true], ['me', true]] as const) {
    const deadline = Date.now() + 10000;
    audio.turn.mockClear();
    receive('bid_turn', { player_id, is_grab, timeout: 10 });
    expect(audio.turn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(4999);
    expect(audio.turn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    if (player_id === 'me') expect(audio.turn).toHaveBeenCalledExactlyOnceWith(true, deadline);
    else expect(audio.turn).not.toHaveBeenCalled();
    receive('bid_result', { player_id, bid: true, is_grab });
    expect(audio.bid).toHaveBeenLastCalledWith(true, is_grab);
    expect(audio.endTurn).toHaveBeenCalled();
    vi.advanceTimersByTime(5000);
    expect(audio.turn).toHaveBeenCalledTimes(player_id === 'me' ? 1 : 0);
  }
  expect(audio.bid.mock.calls).toEqual([[true, false], [true, true], [true, true]]);
  close();
});

test.each(['bid_result', 'player_pass', 'card_played', 'game_over'])('%s cancels the warning before its deadline', (type) => {
  vi.useFakeTimers();
  const { audio, receive, close } = audioSession();
  receive('play_turn', { player_id: 'me', timeout: 10 });
  receive(type, { player_id: 'me', cards: testCards(1), cards_left: 2 });
  vi.advanceTimersByTime(10000);
  expect(audio.turn).not.toHaveBeenCalled();
  expect(audio.endTurn).toHaveBeenCalled();
  close();
});

test.each(['disconnect', 'leave', 'background', 'pagehide', 'mute', 'dispose'])('%s clears scheduled warnings', (action) => {
  vi.useFakeTimers();
  const { audio, receive, close } = audioSession();
  receive('play_turn', { player_id: 'me', timeout: 10 });
  if (action === 'disconnect') useAppStore.getState().setConnected(false);
  if (action === 'leave') useAppStore.getState().leaveLocalRoom();
  if (action === 'background') {
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    document.dispatchEvent(new Event('visibilitychange'));
  }
  if (action === 'pagehide') window.dispatchEvent(new Event('pagehide'));
  if (action === 'mute') {
    audio.getEnabled.mockReturnValue(false);
    audio.subscribe.mock.calls[0][0]();
    audio.getEnabled.mockReturnValue(true);
  }
  if (action === 'dispose') close();
  vi.advanceTimersByTime(10000);
  expect(audio.turn).not.toHaveBeenCalled();
  if (action !== 'dispose') close();
  vi.restoreAllMocks();
});

test.each(['bidding', 'playing'])('does not replay the restored %s turn or its warning', (phase) => {
  vi.useFakeTimers();
  const { audio, receive, close } = audioSession();
  const type = phase === 'bidding' ? 'bid_turn' : 'play_turn';
  receive('reconnected', { player_id: 'me', room_code: '123456', game_state: {
    phase, current_turn: 'me', hand: testCards(17), last_played: testCards(1), last_player_id: 'opponent',
  } });
  receive(type, { player_id: 'me', timeout: 10 });
  vi.advanceTimersByTime(10000);
  expect(audio.turn).not.toHaveBeenCalled();
  expect(audio.deal).not.toHaveBeenCalled();
  expect(audio.play).not.toHaveBeenCalled();
  receive(type, { player_id: 'opponent', timeout: 10 });
  receive(type, { player_id: 'me', timeout: 10 });
  expect(audio.turn).not.toHaveBeenCalled();
  const deadline = Date.now() + 10000;
  vi.advanceTimersByTime(5000);
  expect(audio.turn).toHaveBeenCalledExactlyOnceWith(true, deadline);
  close();
});

test('does not replay a cached settlement after reconnect or duplicate a live settlement', () => {
  const { audio, receive, close } = audioSession();
  const result = { winner_id: 'teammate', is_landlord: false, scores: [{ player_id: 'me', is_landlord: false }] };
  receive('reconnected', { player_id: 'me', room_code: '123456', game_state: { phase: 'ended' } });
  receive('game_over', result);
  expect(audio.result).not.toHaveBeenCalled();
  receive('game_start');
  receive('game_over', result);
  receive('game_over', result);
  expect(audio.result).toHaveBeenCalledExactlyOnceWith(true);
  close();
});

test.each([
  [false, false, true], [false, true, false], [true, false, false], [true, true, true],
])('settlement uses local landlord=%s and winning landlord=%s to determine won=%s', (mine, winner, won) => {
  const { audio, receive, close } = audioSession();
  receive('game_over', { winner_id: 'another-player', is_landlord: winner, scores: [{ player_id: 'me', is_landlord: mine }] });
  expect(audio.result).toHaveBeenCalledExactlyOnceWith(won);
  close();
});

test('passes authoritative remaining counts to audio after updating the hand', () => {
  const { audio, receive, close } = audioSession();
  receive('play_turn', { player_id: 'me', timeout: 10 });
  receive('card_played', { player_id: 'opponent', cards: testCards(4), cards_left: 1, hand_type: '炸弹' });
  expect(audio.play).toHaveBeenCalledWith('炸弹', testCards(4), 1);
  close();
});

test('authoritative deal rounds replay audio, seek on reconnect, and stop at the official turn', () => {
  // Keep message handling and exact timeline assertions independent of wall-clock drift.
  vi.useFakeTimers();
  const now = 1_000_000;
  vi.setSystemTime(now);
  const { audio, receive, close } = audioSession();
  const timing = { deal_id: 'r1', deal_start_at: 100000, deal_end_at: 103870, server_time: 100000 };
  receive('deal_cards', { cards: Array.from({length:17}, () => ({rank:3,suit:0,color:0})), ...timing });
  expect(audio.deal).toHaveBeenLastCalledWith(now, now + 3870);
  receive('deal_cards', timing);
  expect(audio.deal).toHaveBeenCalledTimes(1);
  receive('bid_turn', { player_id: 'me', timeout: 15 });
  expect(audio.endDeal).toHaveBeenCalled();
  receive('deal_cards', { ...timing, deal_id: 'r2' });
  expect(audio.deal).toHaveBeenCalledTimes(2);
  vi.advanceTimersByTime(2000);
  receive('reconnected', { player_id: 'me', game_state: { phase: 'dealing', ...timing, server_time: 102000 } });
  expect(audio.deal).toHaveBeenLastCalledWith(now, now + 3870);
  receive('reconnected', { player_id: 'me', game_state: { phase: 'playing', hand: [] } });
  expect(audio.deal).toHaveBeenCalledTimes(3);
  close();
});

test.each([500, 1000, 2000])('RTT %d: one submission, matching acknowledgement, no replay', (rtt) => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  const transport = FakeSocket.latest;
  transport.onopen?.();
  transport.receive('connected', { player_id: 'me' });
  transport.receive('reconnected', { player_id: 'me' });
  useAppStore.setState({ currentTurn: 'me', bidToken: 'bid-1', timerStart: Date.now(), timeout: 15 });
  expect(socket.send('bid', { bid: true, bid_token: 'bid-1' })).toBe(true);
  expect(socket.send('bid', { bid: false, bid_token: 'bid-1' })).toBe(false);
  expect(useAppStore.getState().submission).toBe('pending');
  vi.advanceTimersByTime(rtt);
  transport.receive('bid_result', { player_id: 'me', action_token: 'bid-1', bid: true });
  expect(useAppStore.getState().submission).toBe('confirmed');
  expect(sentTypes(transport).filter(t => t === 'bid')).toHaveLength(1);
  socket.close();
});

test.each([true, false])('disconnect during submission resolves committed=%s from snapshot', committed => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  let transport = FakeSocket.latest;
  transport.onopen?.();
  transport.receive('connected', { player_id: 'me' });
  transport.receive('reconnected', { player_id: 'me' });
  useAppStore.setState({ currentTurn: 'me', turnToken: 'play-1', timerStart: Date.now(), timeout: 15 });
  socket.send('play_cards', { cards: [], turn_token: 'play-1' });
  transport.close(1006);
  expect(useAppStore.getState().submission).toBe('unknown');
  vi.advanceTimersByTime(2500);
  transport = FakeSocket.latest;
  transport.onopen?.();
  transport.receive('connected', { player_id: 'me' });
  expect(useAppStore.getState().submission).toBe('unknown');
  expect(sessionStorage.getItem('ddz_pending_action')).toContain('play-1');
  transport.receive('reconnected', { player_id: 'me', game_state: { phase: 'playing', last_action_token: committed ? 'play-1' : '', hand: [{rank: 3}] } });
  expect(useAppStore.getState().submission).toBe(committed ? 'confirmed' : 'rejected');
  expect(useAppStore.getState().hand).toHaveLength(1);
  expect(sentTypes(transport)).not.toContain('play_cards');
  socket.close();
});

test('half-open socket times out and ignores messages from the old transport', () => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  const transport = FakeSocket.latest;
  transport.onopen?.();
  transport.send = data => { transport.sent.push(data); }; // blackhole pong
  vi.advanceTimersByTime(15000);
  expect(useAppStore.getState().connected).toBe(false);
  vi.advanceTimersByTime(2500);
  expect(FakeSocket.latest).not.toBe(transport);
  const count = useAppStore.getState().onlineCount;
  transport.receive('online_count', { count: 999 });
  expect(useAppStore.getState().onlineCount).toBe(count);
  socket.close();
});

test.each([1, 5])('%d%% heartbeat response loss does not cause false disconnects', loss => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  const transport = FakeSocket.latest;
  transport.onopen?.();
  let pings = 0;
  transport.send = data => {
    transport.sent.push(data);
    const m = decodeMessage(data);
    if (m.type === 'ping' && ++pings % (100 / loss) !== 0) {
      transport.receive('pong', { client_timestamp: Date.now(), server_timestamp: Date.now() });
    }
  };
  vi.advanceTimersByTime(500000);
  expect(useAppStore.getState().connected).toBe(true);
  expect(FakeSocket.latest).toBe(transport);
  socket.close();
});

test('offline submission is explicit; online and background recovery never replay it', () => {
  vi.useFakeTimers();
  const socket = new GameSocket('ws://localhost/ws');
  expect(socket.send('bid', { bid: true })).toBe(false);
  expect(useAppStore.getState().submissionMessage).toContain('未发送');
  socket.connect();
  const transport = FakeSocket.latest;
  transport.onopen?.();
  transport.close(1006);
  window.dispatchEvent(new Event('online'));
  expect(FakeSocket.latest).not.toBe(transport);
  const replacement = FakeSocket.latest;
  replacement.onopen?.();
  document.dispatchEvent(new Event('visibilitychange'));
  expect(sentTypes(replacement)).not.toContain('bid');
  socket.close();
});

test('late RTT sample corrects both the active countdown and its warning', () => {
  vi.useFakeTimers();
  const { audio, receive, close } = audioSession();
  const now = Date.now();
  // Server clock is 20 seconds ahead; frame spent one second in transit.
  receive('play_turn', { player_id: 'me', timeout: 10, server_time: now + 19000, turn_deadline: now + 29000 });
  receive('pong', { client_timestamp: now - 2000, server_timestamp: now + 19000 });
  const state = useAppStore.getState();
  expect(state.timerStart + state.timeout * 1000).toBe(now + 9000);
  vi.advanceTimersByTime(4000);
  expect(audio.turn).toHaveBeenCalledExactlyOnceWith(true, now + 9000);
  vi.advanceTimersByTime(5000);
  expect(audio.turn).toHaveBeenCalledOnce();
  close();
});

function submitPendingPlay() {
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  const transport = FakeSocket.latest;
  transport.onopen?.();
  transport.receive('connected', { player_id: 'me' });
  transport.receive('reconnected', { player_id: 'me' });
  useAppStore.setState({ currentTurn: 'me', turnToken: 'play-1', timerStart: Date.now(), timeout: 30 });
  expect(socket.send('play_cards', { cards: [], turn_token: 'play-1' })).toBe(true);
  return socket;
}

test.each(['quota', 'read-denied', 'storage-disabled'])('reconnect keeps memory pending when storage is %s', failure => {
  vi.useFakeTimers();
  if (failure !== 'read-denied') vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('full', 'QuotaExceededError'); });
  if (failure !== 'quota') vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new DOMException('denied', 'SecurityError'); });
  const socket = submitPendingPlay();
  try {
    FakeSocket.latest.close(1006);
    vi.advanceTimersByTime(2500);
    const transport = FakeSocket.latest;
    transport.onopen?.();
    transport.receive('connected', { player_id: 'me' });
    expect(useAppStore.getState().submission).toBe('unknown');
    transport.receive('reconnected', { player_id: 'me', game_state: {
      phase: 'playing', current_turn: 'me', turn_token: 'play-2', last_action_token: 'play-1',
      turn_deadline: Date.now() + 30000, server_time: Date.now(),
    } });
    expect(useAppStore.getState().submission).toBe('confirmed');
    expect(sentTypes(transport)).not.toContain('play_cards');
    // The recovered page can submit a new turn instead of remaining locked.
    expect(socket.send('pass', { turn_token: 'play-2' })).toBe(true);
  } finally { socket.close(); }
});

test.each([true, false])('reload waits for final recovery response, snapshot available=%s', hasSnapshot => {
  vi.useFakeTimers();
  submitPendingPlay().close();
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  try {
    const transport = FakeSocket.latest;
    transport.onopen?.();
    transport.receive('connected', { player_id: 'me' });
    expect(useAppStore.getState().submission).toBe('unknown');
    expect(sessionStorage.getItem('ddz_pending_action')).toContain('play-1');
    transport.receive('reconnected', { player_id: 'me', ...(hasSnapshot ? {
      game_state: { phase: 'playing', last_action_token: 'play-1' },
    } : {}) });
    expect(useAppStore.getState().submission).toBe(hasSnapshot ? 'confirmed' : 'idle');
    expect(sessionStorage.getItem('ddz_pending_action')).toBeNull();
    expect(sentTypes(transport)).not.toContain('play_cards');
  } finally { socket.close(); }
});

test('identity message alone does not unlock game submissions', () => {
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  try {
    const transport = FakeSocket.latest;
    transport.onopen?.();
    transport.receive('connected', { player_id: 'me' });
    useAppStore.setState({ currentTurn: 'me', bidToken: 'bid-1', timerStart: Date.now(), timeout: 30 });
    expect(socket.send('bid', { bid: true, bid_token: 'bid-1' })).toBe(false);
    expect(sentTypes(transport)).not.toContain('bid');
  } finally { socket.close(); }
});
