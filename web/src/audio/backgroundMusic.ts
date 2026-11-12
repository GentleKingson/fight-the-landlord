import { useAppStore } from '../stores/appStore';
import { gameAudio } from './gameAudio';
import tableMusic from '../../../internal/sound/gaming/bgm/bgm_normal.mp3?url&no-inline';
import lobbyMusic from '../../../internal/sound/lobby/bgm_welcome.mp3?url&no-inline';

// A separate channel keeps turn announcements from cancelling the music loop.
export function connectBackgroundMusic(): () => void {
  const music = new Audio();
  music.loop = true;
  music.volume = 0.18;
  music.preload = 'none';
  let unlocked = false;
  let disposed = false;
  let source = '';
  const sync = () => {
    const state = useAppStore.getState();
    if (disposed || !unlocked || document.hidden || !state.connected || !gameAudio.getEnabled() || state.phase === 'game_over') {
      music.pause();
      return;
    }
    const next = ['dealing', 'bidding', 'playing'].includes(state.phase) ? tableMusic : lobbyMusic;
    if (source !== next) { source = next; music.src = next; }
    if (music.paused) void music.play().catch(() => { /* Retry on the next gesture or foreground transition. */ });
  };
  // Safari requires play() to be called synchronously from a user gesture.
  const unlock = () => { unlocked = true; sync(); };
  const pause = () => music.pause();
  const unsubscribeStore = useAppStore.subscribe((state, previous) => {
    if (state.phase !== previous.phase || state.connected !== previous.connected) sync();
  });
  const unsubscribeAudio = gameAudio.subscribe(sync);
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
    music.pause();
    music.removeAttribute('src');
    music.load();
  };
}
