import { MsgType, type IncomingPayloadMap } from '../protocol/types';
import { useAppStore } from '../stores/appStore';
import type { GameSocket } from '../transport/wsClient';
import { gameAudio, type GameAudio } from './gameAudio';

// App lifetime, not table-render lifetime: only live socket messages can speak.
export function connectGameAudio(socket: GameSocket, audio: GameAudio = gameAudio): () => void {
  const unsubscribe = socket.subscribe(message => {
    if (message.type === MsgType.CardPlayed && useAppStore.getState().phase === 'playing') {
      const payload = message.payload as IncomingPayloadMap['card_played'];
      audio.play(payload.hand_type, payload.cards ?? []);
    } else if (message.type === MsgType.Reconnected || message.type === MsgType.GameStart) {
      audio.stop();
    }
  });
  const unsubscribeStore = useAppStore.subscribe((state, previous) => {
    if (!state.connected || state.roomCode !== previous.roomCode ||
        !['bidding', 'playing', 'game_over'].includes(state.phase)) audio.stop();
  });
  const onVisibility = () => { if (document.hidden) audio.stop(); };
  document.addEventListener('pointerup', audio.unlock);
  document.addEventListener('keydown', audio.unlock);
  document.addEventListener('visibilitychange', onVisibility);
  window.addEventListener('pagehide', audio.stop);
  return () => {
    unsubscribe();
    unsubscribeStore();
    document.removeEventListener('pointerup', audio.unlock);
    document.removeEventListener('keydown', audio.unlock);
    document.removeEventListener('visibilitychange', onVisibility);
    window.removeEventListener('pagehide', audio.stop);
    audio.stop();
  };
}
