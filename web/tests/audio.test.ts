import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cardSoundNames, cardSoundUrls } from '../src/audio/cardSounds';
import { GameAudio } from '../src/audio/gameAudio';

const cards = (rank: number, count = 1) => Array.from({ length: count }, (_, suit) => ({ rank, suit, color: 0 }));

it('includes exactly the 54 CLI assets and maps every single/pair/trio rank', () => {
  expect(Object.keys(cardSoundUrls)).toHaveLength(54);
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
  decodeAudioData = vi.fn(async () => ({}));
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
