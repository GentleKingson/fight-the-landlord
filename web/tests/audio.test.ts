import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cardSoundNames, cardSoundUrls } from '../src/audio/cardSounds';
import { GameAudio } from '../src/audio/gameAudio';

const cards = (rank: number, count = 1) => Array.from({ length: count }, (_, suit) => ({ rank, suit, color: 0 }));

it('includes exactly the 66 CLI assets and maps every single/pair/trio rank', () => {
  expect(Object.keys(cardSoundUrls)).toHaveLength(66);
  const tokens = ['3', '4', '5', '6', '7', '8', '9', '10', 'J', 'Q', 'K', 'A', '2', 'joker_small', 'joker_big'];
  for (const [type, prefix, count] of [['单张', 'single', 1], ['对子', 'pair', 2], ['三张', 'trio', 3]] as const) {
    for (let rank = 3; rank <= (count === 1 ? 17 : 15); rank++) {
      const name = `${prefix}_${tokens[rank - 3]}`;
      expect(cardSoundNames(type, cards(rank, count))).toEqual([name]);
      expect(cardSoundUrls[name]).toContain(`${name}.mp3`);
    }
  }
});

it.each([
  ['三带一', ['type_trio_single']], ['三带二', ['type_trio_pair']],
  ['顺子', ['type_straight', 'straight']], ['连对', ['type_pairstraight']],
  ['飞机', ['type_plane', 'plane']], ['飞机带单', ['type_plane', 'plane']], ['飞机带对', ['type_plane', 'plane']],
  ['炸弹', ['type_bomb', 'bomb']], ['王炸', ['type_rocket', 'bomb']],
  ['四带二', ['type_four_two']], ['四带两对', ['type_four_twopair']],
  ['未来牌型', ['play']], ['constructor', ['play']],
])('maps %s to voice/effects or a safe fallback', (type, expected) => {
  expect(cardSoundNames(type as string, [])).toEqual(expected);
  for (const name of expected) expect(cardSoundUrls[name]).toBeTruthy();
});

it('falls back for missing ranks and uses the most frequent rank', () => {
  expect(cardSoundNames('单张', [])).toEqual(['play']);
  expect(cardSoundNames('对子', cards(16, 2))).toEqual(['play']);
  expect(cardSoundNames('三张', [...cards(4), ...cards(8, 3)])).toEqual(['trio_8']);
});

class FakeSource {
  buffer: unknown;
  onended: (() => void) | null = null;
  connect = vi.fn();
  disconnect = vi.fn();
  start = vi.fn();
  stop = vi.fn();
}
class FakeContext {
  static latest: FakeContext;
  state = 'suspended';
  sampleRate = 44100;
  destination = {};
  onstatechange?: () => void;
  sources: FakeSource[] = [];
  resume = vi.fn(async () => { this.state = 'running'; });
  createBuffer = vi.fn(() => ({}));
  decodeAudioData = vi.fn(async (): Promise<object> => ({ duration: 2.5 }));
  createGain = vi.fn(() => ({ gain: { value: 1 }, connect: vi.fn(), disconnect: vi.fn() }));
  createBufferSource() { const source = new FakeSource(); this.sources.push(source); return source; }
  constructor() { FakeContext.latest = this; }
}
let audio: GameAudio;
const flush = async () => { for (let i = 0; i < 15; i++) await Promise.resolve(); };
const spoken = () => FakeContext.latest.sources.slice(1); // First source unlocks with silence.

beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal('AudioContext', FakeContext);
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, arrayBuffer: async () => new ArrayBuffer(1) })));
  audio = new GameAudio();
});
afterEach(() => { audio.stop(); vi.useRealTimers(); vi.unstubAllGlobals(); });

it('defaults to enabled, persists both choices and drops events before unlocking after reload', async () => {
  expect(audio.getEnabled()).toBe(true);
  audio.play('单张', cards(3));
  expect(fetch).not.toHaveBeenCalled();
  audio.setEnabled(false);
  expect(localStorage.getItem('ddz_sound_enabled')).toBe('false');
  expect(new GameAudio().getEnabled()).toBe(false);
  audio.setEnabled(true);
  expect(localStorage.getItem('ddz_sound_enabled')).toBe('true');
  const reloaded = new GameAudio();
  expect(reloaded.getEnabled()).toBe(true);
  reloaded.play('单张', cards(3));
  reloaded.unlock();
  await flush();
  expect(fetch).not.toHaveBeenCalled();
  reloaded.play('单张', cards(4));
  await flush();
  expect(spoken()).toHaveLength(1);
  reloaded.stop();
});

it('serializes voices and effects and bounds rapid pending turns', async () => {
  audio.setEnabled(true);
  audio.play('炸弹', cards(3, 4));
  await flush();
  expect(spoken()).toHaveLength(1);
  spoken()[0].onended?.();
  await flush();
  expect(spoken()).toHaveLength(2);
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.bomb, expect.anything());
  for (const rank of [4, 5, 6, 7]) audio.play('单张', cards(rank));
  for (let i = 1; i <= 4; i++) { spoken()[i].onended?.(); await flush(); }
  expect(vi.mocked(fetch).mock.calls.map(call => call[0])).toEqual(
    ['type_bomb', 'bomb', 'single_5', 'single_6', 'single_7'].map(name => cardSoundUrls[name]),
  );
});

it('mute stops the active clip and clears all pending audio', async () => {
  audio.setEnabled(true);
  audio.play('王炸', cards(16, 2));
  audio.play('单张', cards(4));
  await flush();
  const active = spoken()[0];
  audio.setEnabled(false);
  expect(active.stop).toHaveBeenCalledOnce();
  audio.setEnabled(true);
  await flush();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('does not revive an in-flight decode after stop and re-enable', async () => {
  audio.setEnabled(true);
  let decode!: (value: object) => void;
  FakeContext.latest.decodeAudioData.mockImplementationOnce(() => new Promise(resolve => { decode = resolve; }));
  audio.play('单张', cards(3));
  await flush();
  audio.setEnabled(false);
  audio.setEnabled(true);
  audio.play('单张', cards(4));
  await flush();
  decode({});
  await flush();
  expect(spoken()).toHaveLength(1);
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.single_4, expect.anything());
});

it.each(['fetch', 'decode', 'start'])('silently recovers from %s failure', async (failure) => {
  audio.setEnabled(true);
  if (failure === 'fetch') vi.mocked(fetch).mockRejectedValueOnce(new Error('offline'));
  if (failure === 'decode') FakeContext.latest.decodeAudioData.mockRejectedValueOnce(new Error('invalid mp3'));
  if (failure === 'start') vi.spyOn(FakeContext.latest, 'createBufferSource').mockImplementationOnce(() => {
    const source = new FakeSource();
    source.start.mockImplementation(() => { throw new Error('blocked'); });
    return source;
  });
  audio.play('单张', cards(3));
  await flush();
  audio.play('单张', cards(4));
  await flush();
  expect(spoken()).toHaveLength(1);
});

it('drops interrupted audio and resumes only on a new gesture', async () => {
  audio.setEnabled(true);
  audio.play('炸弹', []);
  await flush();
  FakeContext.latest.state = 'interrupted';
  FakeContext.latest.onstatechange?.();
  audio.play('单张', cards(3));
  audio.unlock();
  await flush();
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(FakeContext.latest.resume).toHaveBeenCalledTimes(2);
});

it('drops stale pending turns and recovers from a stalled decode', async () => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  FakeContext.latest.decodeAudioData.mockImplementationOnce(() => new Promise(() => {}));
  audio.play('单张', cards(3));
  audio.play('单张', cards(4));
  await flush();
  await vi.advanceTimersByTimeAsync(3001);
  audio.play('单张', cards(5));
  await flush();
  expect(spoken()).toHaveLength(1);
  expect(fetch).toHaveBeenCalledTimes(2);
});

it.each([
  ['fetch', 100], ['fetch', 1000], ['decode', 100], ['decode', 1000],
] as const)('includes queue time when checking expiry after %s takes %i ms', async (stage, loadMs) => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  audio.play('单张', cards(3));
  await flush();
  audio.play('单张', cards(4));

  if (stage === 'fetch') {
    vi.mocked(fetch).mockImplementationOnce(() => new Promise(resolve => {
      window.setTimeout(() => resolve({ ok: true, arrayBuffer: async () => new ArrayBuffer(1) } as Response), loadMs);
    }));
  } else {
    FakeContext.latest.decodeAudioData.mockImplementationOnce(() => new Promise(resolve => {
      window.setTimeout(() => resolve({}), loadMs);
    }));
  }

  // The second play has already waited 2.8 seconds before loading begins.
  await vi.advanceTimersByTimeAsync(2800);
  spoken()[0].onended?.();
  await flush();
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.single_4, expect.anything());
  await vi.advanceTimersByTimeAsync(loadMs);

  const expired = loadMs === 1000;
  expect(spoken()).toHaveLength(expired ? 1 : 2);
  if (!expired) spoken()[1].onended?.();
  await flush();
  // Dropping an expired clip must not stop a subsequent fresh announcement.
  audio.play('单张', cards(5));
  await flush();
  expect(spoken()).toHaveLength(expired ? 2 : 3);
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.single_5, expect.anything());
});

it.each([1, 2])('announces %i remaining cards after the hand voice and effect', async (remaining) => {
  audio.setEnabled(true);
  audio.play('炸弹', cards(7, 4), remaining);
  await flush();
  expect(fetch).toHaveBeenCalledTimes(1);
  for (let i = 0; i < 2; i++) { spoken()[i].onended?.(); await flush(); }
  expect(vi.mocked(fetch).mock.calls.map(call => call[0])).toEqual(
    ['type_bomb', 'bomb', `last${remaining}`].map(name => cardSoundUrls[name]),
  );
});

it.each([
  [true, false, 'bid_call'], [false, false, 'bid_nocall'],
  [true, true, 'bid_grab'], [false, true, 'bid_nograb'],
] as const)('maps confirmed bid=%s grab=%s to %s', async (bid, grab, name) => {
  audio.setEnabled(true);
  audio.bid(bid, grab);
  await flush();
  expect(fetch).toHaveBeenCalledWith(cardSoundUrls[name], expect.anything());
});

it('serializes passing with the following announcement', async () => {
  audio.setEnabled(true);
  audio.pass();
  audio.play('单张', cards(3), 3);
  await flush();
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.pass_buyao, expect.anything());
  spoken()[0].onended?.();
  await flush();
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.single_3, expect.anything());
});

it('cancels an active turn cue and keeps the confirmed action behind it', async () => {
  audio.setEnabled(true);
  audio.turn(true);
  await flush();
  audio.play('单张', cards(3), 2);
  audio.endTurn();
  await flush();
  expect(spoken()[0].stop).toHaveBeenCalledOnce();
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.single_3, expect.anything());
  expect(FakeContext.latest.createGain.mock.results[0].value.gain.value).toBe(0.3);
});

it('drops queued turn cues without interrupting the current voice', async () => {
  audio.setEnabled(true);
  audio.play('单张', cards(3));
  await flush();
  audio.turn();
  audio.turn(true);
  audio.endTurn();
  expect(spoken()[0].stop).not.toHaveBeenCalled();
  spoken()[0].onended?.();
  await flush();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('cannot revive a cancelled turn cue after a delayed decode', async () => {
  audio.setEnabled(true);
  let decode!: (value: object) => void;
  FakeContext.latest.decodeAudioData.mockImplementationOnce(() => new Promise(resolve => { decode = resolve; }));
  audio.turn(true);
  await flush();
  audio.endTurn();
  audio.pass();
  await flush();
  decode({ duration: 1 });
  await flush();
  expect(spoken()).toHaveLength(1);
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.pass_buyao, expect.anything());
});

it('preserves the winning hand before settlement and discards older pending actions', async () => {
  audio.setEnabled(true);
  audio.pass();
  await flush();
  audio.bid(true, true);
  audio.play('王炸', cards(16, 2), 0);
  audio.result(true);
  await flush();
  expect(spoken()[0].stop).toHaveBeenCalledOnce();
  for (let i = 1; i <= 2; i++) { spoken()[i].onended?.(); await flush(); }
  expect(vi.mocked(fetch).mock.calls.map(call => call[0])).toEqual(
    ['pass_buyao', 'type_rocket', 'bomb', 'win'].map(name => cardSoundUrls[name]),
  );
});

it('waits for an already playing final hand before playing the loss clip', async () => {
  audio.setEnabled(true);
  audio.play('单张', cards(3), 0);
  await flush();
  audio.result(false);
  expect(spoken()[0].stop).not.toHaveBeenCalled();
  expect(fetch).toHaveBeenCalledTimes(1);
  spoken()[0].onended?.();
  await flush();
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.lose, expect.anything());
});

it.each(['deal', 'win', 'lose'])('allows the long %s clip to finish beyond three seconds', async (name) => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  FakeContext.latest.decodeAudioData.mockResolvedValueOnce({ duration: 6 });
  if (name === 'deal') audio.deal();
  else audio.result(name === 'win');
  await flush();
  await vi.advanceTimersByTimeAsync(5500);
  expect(spoken()[0].stop).not.toHaveBeenCalled();
  spoken()[0].onended?.();
  await flush();
  await vi.advanceTimersByTimeAsync(1000);
  expect(spoken()[0].stop).not.toHaveBeenCalled();
});

it('does not play a remaining-card alert for a hand that expired while loading', async () => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  audio.pass();
  await flush();
  audio.play('单张', cards(3), 1);
  await vi.advanceTimersByTimeAsync(2800);
  vi.mocked(fetch).mockImplementationOnce(() => new Promise(resolve => {
    window.setTimeout(() => resolve({ ok: true, arrayBuffer: async () => new ArrayBuffer(1) } as Response), 1000);
  }));
  spoken()[0].onended?.();
  await flush();
  await vi.advanceTimersByTimeAsync(1000);
  expect(spoken()).toHaveLength(1);
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('keeps the first turn cue after a 3.87-second deal while the server turn is still active', async () => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  FakeContext.latest.decodeAudioData.mockResolvedValueOnce({ duration: 3.87 });
  audio.deal();
  audio.turn(false, Date.now() + 15000);
  await flush();
  await vi.advanceTimersByTimeAsync(3870);
  spoken()[0].onended?.();
  await flush();
  expect(spoken()).toHaveLength(2);
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.turn, expect.anything());
});

it('drops the first turn cue if the turn ends before dealing finishes', async () => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  FakeContext.latest.decodeAudioData.mockResolvedValueOnce({ duration: 3.87 });
  audio.deal();
  audio.turn(false, Date.now() + 15000);
  await flush();
  await vi.advanceTimersByTimeAsync(1000);
  audio.endTurn();
  await vi.advanceTimersByTimeAsync(2870);
  spoken()[0].onended?.();
  await flush();
  expect(spoken()).toHaveLength(1);
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('does not start a turn cue whose server deadline passed during loading', async () => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  vi.mocked(fetch).mockImplementationOnce(() => new Promise(resolve => {
    window.setTimeout(() => resolve({ ok: true, arrayBuffer: async () => new ArrayBuffer(1) } as Response), 1000);
  }));
  audio.turn(true, Date.now() + 500);
  await vi.advanceTimersByTimeAsync(1000);
  expect(spoken()).toHaveLength(0);
});

it('seeks a delayed deal load to the shared timeline and stops on official turn', async () => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  FakeContext.latest.decodeAudioData.mockResolvedValueOnce({ duration: 3.87 });
  vi.mocked(fetch).mockImplementationOnce(() => new Promise(resolve => {
    window.setTimeout(() => resolve({ ok: true, arrayBuffer: async () => new ArrayBuffer(1) } as Response), 1000);
  }));
  audio.deal(Date.now(), Date.now() + 3870);
  await vi.advanceTimersByTimeAsync(1000);
  expect(spoken()[0].start).toHaveBeenCalledWith(0, 1, 2.87);
  audio.endDeal();
  expect(spoken()[0].stop).toHaveBeenCalledOnce();
});

it.each(['muted', 'locked', 'failed', 'expired'] as const)('drops %s deal audio without throwing or replaying later', async mode => {
  vi.useFakeTimers();
  if (mode === 'muted') audio.setEnabled(false);
  if (mode === 'failed' || mode === 'expired') audio.setEnabled(true);
  if (mode === 'failed') vi.mocked(fetch).mockRejectedValueOnce(new Error('offline'));
  audio.deal(Date.now() - (mode === 'expired' ? 4000 : 0), Date.now() + (mode === 'expired' ? -130 : 3870));
  await flush();
  audio.endDeal();
  audio.setEnabled(true);
  await flush();
  expect(spoken()).toHaveLength(0);
});
