import lobbyMusic from '../assets/audio/lobby/bgm_welcome.mp3?url&no-inline';
import type { Phase } from '../protocol/types';
import { useAppStore } from '../stores/appStore';
import { gameAudio, SFX_VOLUME, type GameAudio } from './gameAudio';

/** Background music volume at 50% of standard sound-effect volume. */
export const BGM_VOLUME = SFX_VOLUME * 0.5;

const LOBBY_PHASES: ReadonlySet<Phase> = new Set<Phase>(['lobby', 'matching', 'waiting']);

/**
 * Connects lobby background music playback to application phase and audio settings.
 * Background music plays only in the lobby and pauses during matches and settlement.
 */
export function connectBackgroundMusic(audio: GameAudio = gameAudio): () => void {
  const music = new Audio();
  music.loop = true;
  music.volume = BGM_VOLUME;
  music.preload = 'none';
  let unlocked = false;
  let disposed = false;
  let sourceLoaded = false;
  const pause = () => {
    if (!music.paused) {
      music.pause();
    }
  };
  const sync = () => {
    const state = useAppStore.getState();
    const inLobby = LOBBY_PHASES.has(state.phase);
    if (
      disposed ||
      !unlocked ||
      document.hidden ||
      !state.connected ||
      !audio.getEnabled() ||
      !inLobby
    ) {
      pause();
      if (!inLobby && music.currentTime > 0) {
        music.currentTime = 0;
      }
      return;
    }
    if (!sourceLoaded) {
      sourceLoaded = true;
      music.src = lobbyMusic;
    }
    if (music.paused) {
      try {
        void music.play()?.catch(() => {
          // Retry on the next gesture or foreground transition.
        });
      } catch {
        // Unsupported media playback must not affect the game.
      }
    }
  };
  // Safari requires play() to be called synchronously from a user gesture.
  const unlock = () => {
    unlocked = true;
    sync();
  };
  const unsubscribeStore = useAppStore.subscribe((state, previous) => {
    if (state.phase !== previous.phase || state.connected !== previous.connected) {
      sync();
    }
  });
  const unsubscribeAudio = audio.subscribe(() => {
    if (audio.getEnabled()) {
      unlocked = true;
    }
    sync();
  });
  document.addEventListener('pointerup', unlock);
  document.addEventListener('touchend', unlock);
  document.addEventListener('keydown', unlock);
  document.addEventListener('visibilitychange', sync);
  window.addEventListener('pageshow', sync);
  window.addEventListener('pagehide', pause);
  return () => {
    disposed = true;
    unsubscribeStore();
    unsubscribeAudio();
    document.removeEventListener('pointerup', unlock);
    document.removeEventListener('touchend', unlock);
    document.removeEventListener('keydown', unlock);
    document.removeEventListener('visibilitychange', sync);
    window.removeEventListener('pageshow', sync);
    window.removeEventListener('pagehide', pause);
    pause();
    if (sourceLoaded) {
      music.removeAttribute('src');
      music.load();
    }
  };
}
