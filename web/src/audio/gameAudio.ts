import type { CardInfo } from '../protocol/types';
import { cardSoundNames, cardSoundUrls } from './cardSounds';

const PREFERENCE_KEY = 'ddz_sound_enabled';
const MAX_PENDING_PLAYS = 3;
const MAX_AGE_MS = 3000;

export class GameAudio {
  private enabled = this.loadPreference();
  private context: AudioContext | null = null;
  private buffers = new Map<string, AudioBuffer>();
  private listeners = new Set<() => void>();
  private queue: { names: string[]; time: number }[] = [];
  private generation = 0;
  private draining = false;
  private cancelClip: (() => void) | null = null;

  getEnabled = (): boolean => this.enabled;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  setEnabled(enabled: boolean): void {
    this.enabled = enabled;
    try { localStorage.setItem(PREFERENCE_KEY, String(enabled)); } catch { /* Storage is optional. */ }
    if (enabled) this.unlock();
    else this.stop();
    this.listeners.forEach(listener => listener());
  }

  // Call synchronously from a user gesture, including the first gesture after
  // reload or iOS interruption. Never replay events received while locked.
  unlock = (): void => {
    if (!this.enabled || document.hidden) return;
    try {
      if (!this.context || this.context.state === 'closed') {
        const AudioContextClass = window.AudioContext ?? (window as Window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
        if (!AudioContextClass) return;
        this.context = new AudioContextClass();
        this.context.onstatechange = () => {
          if (this.context?.state !== 'running') this.stop();
        };
      }
      if (this.context.state === 'running') return;
      void this.context.resume().catch(() => {});
      const silent = this.context.createBufferSource();
      silent.buffer = this.context.createBuffer(1, 1, this.context.sampleRate);
      silent.connect(this.context.destination);
      silent.onended = () => silent.disconnect();
      silent.start();
    } catch { /* Unsupported audio or autoplay policy must not affect the game. */ }
  };

  play(handType: string, cards: CardInfo[]): void {
    if (!this.enabled || document.hidden || this.context?.state !== 'running') return;
    this.queue.push({ names: cardSoundNames(handType, cards), time: Date.now() });
    // Keep rapid bot turns bounded; discard the oldest pending announcements.
    this.queue = this.queue.slice(-MAX_PENDING_PLAYS);
    if (!this.draining) void this.drain(this.generation);
  }

  stop = (): void => {
    this.generation++;
    this.queue = [];
    this.draining = false;
    this.cancelClip?.();
    this.cancelClip = null;
  };

  private async drain(generation: number): Promise<void> {
    this.draining = true;
    try {
      while (generation === this.generation && this.queue.length) {
        const next = this.queue.shift()!;
        if (Date.now() - next.time > MAX_AGE_MS) continue;
        for (const name of next.names) {
          if (generation !== this.generation || Date.now() - next.time > MAX_AGE_MS) break;
          await this.playClip(name, generation);
        }
      }
    } finally {
      if (generation === this.generation) this.draining = false;
    }
  }

  private async playClip(name: string, generation: number): Promise<void> {
    const context = this.context!;
    const abort = new AbortController();
    let source: AudioBufferSourceNode | null = null;
    let finish: (() => void) | undefined;
    const cancel = () => {
      abort.abort();
      if (source) {
        source.onended = null;
        try { source.stop(); } catch { /* Already ended. */ }
        source.disconnect();
      }
      finish?.();
    };
    this.cancelClip = cancel;
    // Also bound stalled downloads/decode/playback, so they cannot hold a queue.
    const timeout = window.setTimeout(() => {
      if (this.cancelClip === cancel) this.stop();
    }, MAX_AGE_MS);
    try {
      let buffer = this.buffers.get(name);
      if (!buffer) {
        const response = await fetch(cardSoundUrls[name], { signal: abort.signal });
        if (!response.ok) return;
        buffer = await context.decodeAudioData(await response.arrayBuffer());
        if (abort.signal.aborted || generation !== this.generation) return;
        this.buffers.set(name, buffer);
      }
      if (abort.signal.aborted || generation !== this.generation || context.state !== 'running') return;
      source = context.createBufferSource();
      source.buffer = buffer;
      source.connect(context.destination);
      await new Promise<void>((resolve) => {
        finish = resolve;
        source!.onended = () => resolve();
        source!.start();
      });
    } catch { /* Network, decode and playback failures are intentionally silent. */ }
    finally {
      window.clearTimeout(timeout);
      source?.disconnect();
      if (this.cancelClip === cancel) this.cancelClip = null;
    }
  }

  private loadPreference(): boolean {
    try { return localStorage.getItem(PREFERENCE_KEY) !== 'false'; }
    catch { return true; }
  }
}

export const gameAudio = new GameAudio();
