import './resetStore';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { connectBackgroundMusic } from '../src/audio/backgroundMusic';
import { cardSoundUrls } from '../src/audio/cardSounds';
import { GameAudio } from '../src/audio/gameAudio';
import type { Phase } from '../src/stores/types';
import { useAppStore } from '../src/stores/appStore';

function cards(rank: number, count = 1) {
  return Array.from({ length: count }, (_, suit) => ({ rank, suit, color: 0 }));
}

class FakeSource {
  buffer: unknown;
  onended: (() => void) | null = null;
  readonly connect = vi.fn();
  readonly disconnect = vi.fn();
  readonly start = vi.fn();
  readonly stop = vi.fn();
}

class FakeContext {
  static latest: FakeContext;
  state = 'suspended';
  readonly sampleRate = 44100;
  readonly destination = {};
  onstatechange?: () => void;
  readonly sources: FakeSource[] = [];
  readonly resume = vi.fn(async () => {
    this.state = 'running';
  });
  readonly createBuffer = vi.fn(() => ({}));
  readonly decodeAudioData = vi.fn(async (): Promise<object> => ({ duration: 2.5 }));
  readonly createGain = vi.fn(() => ({ gain: { value: 1 }, connect: vi.fn(), disconnect: vi.fn() }));

  constructor() {
    FakeContext.latest = this;
  }

  createBufferSource() {
    const source = new FakeSource();
    this.sources.push(source);
    return source;
  }
}

let audio: GameAudio;

async function flush() {
  for (let i = 0; i < 15; i++) {
    await Promise.resolve();
  }
}

function spoken() {
  // First source unlocks with silence.
  return FakeContext.latest.sources.slice(1);
}

beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal('AudioContext', FakeContext);
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(new Uint8Array(1)))
  );
  audio = new GameAudio();
});

afterEach(() => {
  audio.stop();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

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
  for (const rank of [4, 5, 6, 7]) {
    audio.play('单张', cards(rank));
  }
  for (let i = 1; i <= 4; i++) {
    spoken()[i].onended?.();
    await flush();
  }
  expect(vi.mocked(fetch).mock.calls.map((call) => call[0])).toEqual(
    ['type_bomb', 'bomb', 'single_5', 'single_6', 'single_7'].map((name) => cardSoundUrls[name])
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
  let decode: ((value: object) => void) | undefined;
  FakeContext.latest.decodeAudioData.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        decode = resolve;
      })
  );
  audio.play('单张', cards(3));
  await flush();
  audio.setEnabled(false);
  audio.setEnabled(true);
  audio.play('单张', cards(4));
  await flush();
  decode?.({});
  await flush();
  expect(spoken()).toHaveLength(1);
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.single_4, expect.anything());
});

it.each(['fetch', 'decode', 'start'])('silently recovers from %s failure', async (failure) => {
  audio.setEnabled(true);
  if (failure === 'fetch') {
    vi.mocked(fetch).mockRejectedValueOnce(new Error('offline'));
  }
  if (failure === 'decode') {
    FakeContext.latest.decodeAudioData.mockRejectedValueOnce(new Error('invalid mp3'));
  }
  if (failure === 'start') {
    vi.spyOn(FakeContext.latest, 'createBufferSource').mockImplementationOnce(() => {
      const source = new FakeSource();
      source.start.mockImplementation(() => {
        throw new Error('blocked');
      });
      return source;
    });
  }
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
  ['fetch', 100],
  ['fetch', 1000],
  ['decode', 100],
  ['decode', 1000]
] as const)('includes queue time when checking expiry after %s takes %i ms', async (stage, loadMs) => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  audio.play('单张', cards(3));
  await flush();
  audio.play('单张', cards(4));

  if (stage === 'fetch') {
    vi.mocked(fetch).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          window.setTimeout(() => {
            resolve(new Response(new Uint8Array(1)));
          }, loadMs);
        })
    );
  } else {
    FakeContext.latest.decodeAudioData.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          window.setTimeout(() => {
            resolve({});
          }, loadMs);
        })
    );
  }

  // The second play has already waited 2.8 seconds before loading begins.
  await vi.advanceTimersByTimeAsync(2800);
  spoken()[0].onended?.();
  await flush();
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.single_4, expect.anything());
  await vi.advanceTimersByTimeAsync(loadMs);

  const expired = loadMs === 1000;
  expect(spoken()).toHaveLength(expired ? 1 : 2);
  if (!expired) {
    spoken()[1].onended?.();
  }
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
  for (let i = 0; i < 2; i++) {
    spoken()[i].onended?.();
    await flush();
  }
  expect(vi.mocked(fetch).mock.calls.map((call) => call[0])).toEqual(
    ['type_bomb', 'bomb', `last${remaining}`].map((name) => cardSoundUrls[name])
  );
});

it.each([
  [true, false, 'bid_call'],
  [false, false, 'bid_nocall'],
  [true, true, 'bid_grab'],
  [false, true, 'bid_nograb']
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
});

it('drops queued turn cues without interrupting the current voice', async () => {
  audio.setEnabled(true);
  audio.play('单张', cards(3));
  await flush();
  audio.turn(true);
  audio.endTurn();
  expect(spoken()[0].stop).not.toHaveBeenCalled();
  spoken()[0].onended?.();
  await flush();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('cannot revive a cancelled turn cue after a delayed decode', async () => {
  audio.setEnabled(true);
  let decode: ((value: object) => void) | undefined;
  FakeContext.latest.decodeAudioData.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        decode = resolve;
      })
  );
  audio.turn(true);
  await flush();
  audio.endTurn();
  audio.pass();
  await flush();
  decode?.({ duration: 1 });
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
  for (let i = 1; i <= 2; i++) {
    spoken()[i].onended?.();
    await flush();
  }
  expect(vi.mocked(fetch).mock.calls.map((call) => call[0])).toEqual(
    ['pass_buyao', 'type_rocket', 'bomb', 'win'].map((name) => cardSoundUrls[name])
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
  if (name === 'deal') {
    audio.deal();
  } else {
    audio.result(name === 'win');
  }
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
  vi.mocked(fetch).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        window.setTimeout(() => {
          resolve(new Response(new Uint8Array(1)));
        }, 1000);
      })
  );
  spoken()[0].onended?.();
  await flush();
  await vi.advanceTimersByTimeAsync(1000);
  expect(spoken()).toHaveLength(1);
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('keeps a queued warning after a 3.87-second deal while the server turn is still active', async () => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  FakeContext.latest.decodeAudioData.mockResolvedValueOnce({ duration: 3.87 });
  audio.deal();
  audio.turn(true, Date.now() + 5000);
  await flush();
  await vi.advanceTimersByTimeAsync(3870);
  spoken()[0].onended?.();
  await flush();
  expect(spoken()).toHaveLength(2);
  expect(fetch).toHaveBeenLastCalledWith(cardSoundUrls.warning, expect.anything());
});

it('drops a queued warning if the turn ends before dealing finishes', async () => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  FakeContext.latest.decodeAudioData.mockResolvedValueOnce({ duration: 3.87 });
  audio.deal();
  audio.turn(true, Date.now() + 5000);
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
  vi.mocked(fetch).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        window.setTimeout(() => {
          resolve(new Response(new Uint8Array(1)));
        }, 1000);
      })
  );
  audio.turn(true, Date.now() + 500);
  await vi.advanceTimersByTimeAsync(1000);
  expect(spoken()).toHaveLength(0);
});

it('seeks a delayed deal load to the shared timeline and stops on official turn', async () => {
  vi.useFakeTimers();
  audio.setEnabled(true);
  FakeContext.latest.decodeAudioData.mockResolvedValueOnce({ duration: 3.87 });
  vi.mocked(fetch).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        window.setTimeout(() => {
          resolve(new Response(new Uint8Array(1)));
        }, 1000);
      })
  );
  audio.deal(Date.now(), Date.now() + 3870);
  await vi.advanceTimersByTimeAsync(1000);
  expect(spoken()[0].start).toHaveBeenCalledWith(0, 1, 2.87);
  audio.endDeal();
  expect(spoken()[0].stop).toHaveBeenCalledOnce();
});

it.each(['muted', 'locked', 'failed', 'expired'] as const)(
  'drops %s deal audio without throwing or replaying later',
  async (mode) => {
    vi.useFakeTimers();
    if (mode === 'muted') {
      audio.setEnabled(false);
    }
    if (mode === 'failed' || mode === 'expired') {
      audio.setEnabled(true);
    }
    if (mode === 'failed') {
      vi.mocked(fetch).mockRejectedValueOnce(new Error('offline'));
    }
    audio.deal(Date.now() - (mode === 'expired' ? 4000 : 0), Date.now() + (mode === 'expired' ? -130 : 3870));
    await flush();
    audio.endDeal();
    audio.setEnabled(true);
    await flush();
    expect(spoken()).toHaveLength(0);
  }
);

class FakeMusicElement {
  static latest: FakeMusicElement;
  loop = false;
  volume = 1;
  preload = 'auto';
  src = '';
  paused = true;
  currentTime = 0;
  readonly play = vi.fn(async () => {
    this.paused = false;
  });
  readonly pause = vi.fn(() => {
    this.paused = true;
  });
  readonly removeAttribute = vi.fn((attr: string) => {
    if (attr === 'src') {
      this.src = '';
    }
  });
  readonly load = vi.fn();

  constructor() {
    FakeMusicElement.latest = this;
  }
}

it('plays background music only in lobby phases and never during matches', async () => {
  vi.stubGlobal('Audio', FakeMusicElement);
  audio.setEnabled(true);
  audio.play('单张', cards(3));
  await flush();
  useAppStore.setState({ connected: true, phase: 'connecting' });
  const dispose = connectBackgroundMusic(audio);
  const music = FakeMusicElement.latest;
  expect(music.loop).toBe(true);

  // Gesture during handshake ('connecting') does not start music prematurely.
  document.dispatchEvent(new Event('pointerup'));
  expect(music.play).not.toHaveBeenCalled();

  // Entering lobby phases starts and keeps bgm_welcome playing.
  for (const lobbyPhase of ['lobby', 'matching', 'waiting'] as const) {
    useAppStore.setState({ phase: lobbyPhase });
    expect(music.paused).toBe(false);
    expect(music.src).toContain('/src/assets/audio/lobby/bgm_welcome.mp3');
    expect(music.src).not.toContain('bgm_normal');
  }
  expect(music.play).toHaveBeenCalledOnce();
  music.currentTime = 12.5;

  // Entering any match or settlement phase stops BGM immediately and rewinds.
  for (const matchPhase of ['dealing', 'bidding', 'playing', 'game_over'] as Phase[]) {
    useAppStore.setState({ phase: matchPhase });
    document.dispatchEvent(new Event('pointerup'));
    expect(music.paused).toBe(true);
    expect(music.currentTime).toBe(0);
    expect(music.src).not.toContain('bgm_normal');
  }
  expect(music.play).toHaveBeenCalledOnce();

  // Returning from settlement to lobby resumes lobby BGM.
  useAppStore.setState({ phase: 'lobby' });
  expect(music.paused).toBe(false);
  expect(music.play).toHaveBeenCalledTimes(2);

  dispose();
  expect(music.paused).toBe(true);
  expect(music.removeAttribute).toHaveBeenCalledWith('src');
  expect(music.load).toHaveBeenCalledOnce();
});

it('never loads background music when starting directly in a match and respects mute/visibility in lobby', () => {
  vi.stubGlobal('Audio', FakeMusicElement);
  useAppStore.setState({ connected: true, phase: 'playing' });
  const dispose = connectBackgroundMusic(audio);
  const music = FakeMusicElement.latest;

  document.dispatchEvent(new Event('keydown'));
  expect(music.src).toBe('');
  expect(music.play).not.toHaveBeenCalled();

  // Mute -> enter lobby -> unmute via SoundToggle unlocks and plays in lobby.
  audio.setEnabled(false);
  useAppStore.setState({ phase: 'lobby' });
  expect(music.play).not.toHaveBeenCalled();
  audio.setEnabled(true);
  expect(music.paused).toBe(false);
  expect(music.play).toHaveBeenCalledOnce();

  // Backgrounding and disconnection pause lobby music; restoring resumes it.
  const hiddenSpy = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
  document.dispatchEvent(new Event('visibilitychange'));
  expect(music.paused).toBe(true);
  hiddenSpy.mockReturnValue(false);
  document.dispatchEvent(new Event('visibilitychange'));
  expect(music.paused).toBe(false);

  useAppStore.setState({ connected: false });
  expect(music.paused).toBe(true);
  useAppStore.setState({ connected: true });
  expect(music.paused).toBe(false);

  dispose();
  hiddenSpy.mockRestore();
});
