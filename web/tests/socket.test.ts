import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { GameSocket } from '../src/transport/wsClient';
import { encodeMessage, decodeMessage } from '../src/protocol/codec';
import { loadReconnect, useAppStore } from '../src/stores/appStore';
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
  send(data: Uint8Array) { this.sent.push(data); }
  close(code = 1000) { this.onclose?.({ code }); }
  receive(type: string, payload: Record<string, unknown>) { this.onmessage?.({ data: encodeMessage(type, payload) }); }
}

beforeEach(() => { sessionStorage.clear(); vi.stubGlobal('WebSocket', FakeSocket); });

test('reload and repeated reconnect preserve the original identity and token', () => {
  sessionStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'old', token: 'old-token' }));
  const socket = new GameSocket('ws://localhost/ws');
  for (let i = 0; i < 2; i++) {
    socket.connect();
    const transport = FakeSocket.latest;
    transport.onopen?.();
    expect(decodeMessage(transport.sent[0]).payload).toMatchObject({ player_id: 'old', token: 'old-token' });
    transport.receive('connected', { player_id: 'temporary', player_name: 'Temp', reconnect_token: 'temporary-token' });
    expect(loadReconnect()).toEqual({ id: 'old', token: 'old-token' });
    transport.receive('reconnected', { player_id: 'old', player_name: 'Player' });
    expect(useAppStore.getState().playerId).toBe('old');
    socket.close();
    expect(loadReconnect()).toEqual({ id: 'old', token: 'old-token' });
  }
});

test('expired reconnect falls back to the new server identity', () => {
  sessionStorage.setItem('ddz_next_reconnect', JSON.stringify({ id: 'old', token: 'expired' }));
  const socket = new GameSocket('ws://localhost/ws');
  socket.connect();
  FakeSocket.latest.onopen?.();
  FakeSocket.latest.receive('connected', { player_id: 'new', player_name: 'New', reconnect_token: 'new-token' });
  FakeSocket.latest.receive('error', { code: 1, message: 'expired' });
  expect(loadReconnect()).toEqual({ id: 'new', token: 'new-token' });
  expect(useAppStore.getState().phase).toBe('lobby');
  socket.close();
});


afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

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

test.each(['bid_turn', 'play_turn'])('%s warns only the local player at five seconds, with a start cue only for bidding', (type) => {
  vi.useFakeTimers();
  const { audio, receive, close } = audioSession();
  receive(type, { player_id: 'opponent', timeout: 10 });
  vi.advanceTimersByTime(10000);
  expect(audio.turn).not.toHaveBeenCalled();
  const deadline = Date.now() + 10000;
  receive(type, { player_id: 'me', timeout: 10 });
  const startCues = type === 'bid_turn' ? 1 : 0;
  if (startCues) expect(audio.turn).toHaveBeenCalledExactlyOnceWith(false, deadline);
  else expect(audio.turn).not.toHaveBeenCalled();
  vi.advanceTimersByTime(4999);
  expect(audio.turn).toHaveBeenCalledTimes(startCues);
  vi.advanceTimersByTime(1);
  expect(audio.turn).toHaveBeenLastCalledWith(true, deadline);
  vi.advanceTimersByTime(4999);
  expect(audio.turn).toHaveBeenCalledTimes(startCues + 1);
  audio.endTurn.mockClear();
  vi.advanceTimersByTime(1);
  expect(audio.endTurn).toHaveBeenCalledOnce();
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
  expect(audio.turn).toHaveBeenCalledTimes(phase === 'bidding' ? 1 : 0);
  const deadline = Date.now() + 10000;
  vi.advanceTimersByTime(5000);
  expect(audio.turn).toHaveBeenLastCalledWith(true, deadline);
  expect(audio.turn).toHaveBeenCalledTimes(phase === 'bidding' ? 2 : 1);
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
  const { audio, receive, close } = audioSession();
  const timing = { deal_id: 'r1', deal_start_at: 100000, deal_end_at: 103870, server_time: 100000 };
  receive('deal_cards', { cards: Array.from({length:17}, () => ({rank:3,suit:0,color:0})), ...timing });
  expect(audio.deal).toHaveBeenLastCalledWith(Date.now(), Date.now() + 3870);
  receive('deal_cards', timing);
  expect(audio.deal).toHaveBeenCalledTimes(1);
  receive('bid_turn', { player_id: 'me', timeout: 15 });
  expect(audio.endDeal).toHaveBeenCalled();
  receive('deal_cards', { ...timing, deal_id: 'r2' });
  expect(audio.deal).toHaveBeenCalledTimes(2);
  receive('reconnected', { player_id: 'me', game_state: { phase: 'dealing', ...timing, server_time: 102000 } });
  expect(audio.deal).toHaveBeenLastCalledWith(Date.now() - 2000, Date.now() + 1870);
  receive('reconnected', { player_id: 'me', game_state: { phase: 'playing', hand: [] } });
  expect(audio.deal).toHaveBeenCalledTimes(3);
  close();
});
