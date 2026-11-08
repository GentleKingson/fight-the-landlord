import type { CardInfo } from '../protocol/types';
import { cardSoundNames, cardSoundUrls } from './cardSounds';

const PREFERENCE_KEY = 'ddz_sound_enabled';
const MAX_PENDING_PLAYS = 3;
const MAX_AGE_MS = 3000;

type AudioKind = 'event' | 'turn' | 'final-play' | 'result';
interface Announcement { names: string[]; expiresAt: number; kind: AudioKind; volume: number }

export class GameAudio {
  private enabled = this.loadPreference();
  private context: AudioContext | null = null;
  private buffers = new Map<string, AudioBuffer>();
  private listeners = new Set<() => void>();
  private queue: Announcement[] = [];
  private active: Announcement | null = null;
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

  play(handType: string, cards: CardInfo[], cardsLeft?: number): void {
    const names = [...cardSoundNames(handType, cards)];
    if (cardsLeft === 1 || cardsLeft === 2) names.push(`last${cardsLeft}`);
    this.enqueue(names, cardsLeft === 0 ? 'final-play' : 'event');
  }

  bid(bid: boolean, isGrab: boolean): void {
    this.enqueue([isGrab ? (bid ? 'bid_grab' : 'bid_nograb') : (bid ? 'bid_call' : 'bid_nocall')]);
  }

  pass(): void { this.enqueue(['pass_buyao']); }
  deal(): void { this.enqueue(['deal'], 'event', 0.45); }
  turn(warning = false): void { this.enqueue([warning ? 'warning' : 'turn'], 'turn', 0.3); }

  endTurn(): void {
    this.queue = this.queue.filter(item => item.kind !== 'turn');
    if (this.active?.kind === 'turn') this.interrupt();
  }

  result(won: boolean): void {
    // Keep the winning hand, but discard older pending actions before settlement.
    this.queue = this.queue.filter(item => item.kind === 'final-play');
    if (this.active && this.active.kind !== 'final-play') this.interrupt();
    this.enqueue([won ? 'win' : 'lose'], 'result', 0.6);
  }

  private enqueue(names: string[], kind: AudioKind = 'event', volume = 1): void {
    if (!this.enabled || document.hidden || this.context?.state !== 'running') return;
    this.queue.push({ names, kind, volume, expiresAt: Date.now() + (kind === 'result' ? 10000 : MAX_AGE_MS) });
    this.queue = this.queue.slice(-MAX_PENDING_PLAYS);
    if (!this.draining) void this.drain(this.generation);
  }

  private interrupt(): void {
    this.generation++;
    this.draining = false;
    this.active = null;
    this.cancelClip?.();
    this.cancelClip = null;
    if (this.queue.length) void this.drain(this.generation);
  }

  stop = (): void => {
    this.generation++;
    this.queue = [];
    this.draining = false;
    this.active = null;
    this.cancelClip?.();
    this.cancelClip = null;
  };

  private async drain(generation: number): Promise<void> {
    this.draining = true;
    try {
      while (generation === this.generation && this.queue.length) {
        const next = this.queue.shift()!;
        if (Date.now() > next.expiresAt) continue;
        this.active = next;
        for (const [index, name] of next.names.entries()) {
          if (generation !== this.generation) break;
          // Once a hand starts, finish its effect/remaining-card announcement in order.
          const deadline = index === 0 ? next.expiresAt : Date.now() + MAX_AGE_MS;
          const played = await this.playClip(name, generation, deadline, next.volume);
          if (!played && Date.now() > deadline) break;
        }
      }
    } finally {
      if (generation === this.generation) { this.draining = false; this.active = null; }
    }
  }

  private async playClip(name: string, generation: number, expiresAt: number, volume: number): Promise<boolean> {
    const context = this.context!;
    const abort = new AbortController();
    let source: AudioBufferSourceNode | null = null;
    let gain: GainNode | null = null;
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
    let timeout = window.setTimeout(() => {
      if (this.cancelClip === cancel) this.stop();
    }, MAX_AGE_MS);
    try {
      let buffer = this.buffers.get(name);
      if (!buffer) {
        const response = await fetch(cardSoundUrls[name], { signal: abort.signal });
        if (!response.ok) return false;
        buffer = await context.decodeAudioData(await response.arrayBuffer());
        if (abort.signal.aborted || generation !== this.generation) return false;
        this.buffers.set(name, buffer);
      }
      if (abort.signal.aborted || generation !== this.generation || context.state !== 'running') return false;
      // Queueing, downloading and decoding all share this hand's deadline.
      if (Date.now() > expiresAt) return false;
      source = context.createBufferSource();
      source.buffer = buffer;
      gain = context.createGain();
      gain.gain.value = volume;
      source.connect(gain);
      gain.connect(context.destination);
      // Loading is bounded separately; long deal/result clips must play to the end.
      window.clearTimeout(timeout);
      timeout = window.setTimeout(() => {
        if (this.cancelClip === cancel) this.stop();
      }, Math.min(15000, buffer.duration * 1000 + 500));
      await new Promise<void>((resolve) => {
        finish = resolve;
        source!.onended = () => resolve();
        source!.start();
      });
      return true;
    } catch { return false; /* Audio failures must never affect the game. */ }
    finally {
      window.clearTimeout(timeout);
      source?.disconnect();
      gain?.disconnect();
      if (this.cancelClip === cancel) this.cancelClip = null;
    }
  }

  private loadPreference(): boolean {
    try { return localStorage.getItem(PREFERENCE_KEY) !== 'false'; }
    catch { return true; }
  }
}

export const gameAudio = new GameAudio();
