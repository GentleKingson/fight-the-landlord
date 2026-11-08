import { MsgType, type IncomingPayloadMap } from '../protocol/types';
import { useAppStore } from '../stores/appStore';
import type { GameSocket } from '../transport/wsClient';
import { gameAudio, type GameAudio } from './gameAudio';

const WARNING_SECONDS = 5;

// App lifetime, not table-render lifetime: only live socket messages can speak.
export function connectGameAudio(socket: GameSocket, audio: GameAudio = gameAudio): () => void {
  let warningTimer: number | undefined;
  let expiryTimer: number | undefined;
  let replayTurn: { type: string; playerId: string } | null = null;
  let replayResult = false;
  let dealt = false;
  let dealID = '';
  const endTurn = () => {
    window.clearTimeout(warningTimer);
    window.clearTimeout(expiryTimer);
    warningTimer = expiryTimer = undefined;
    audio.endTurn();
  };
  const stop = () => { endTurn(); audio.stop(); };
  const unsubscribe = socket.subscribe(message => {
    const state = useAppStore.getState();
    if (message.type === MsgType.Reconnected) {
      stop();
      const snapshot = (message.payload as IncomingPayloadMap['reconnected']).game_state;
      // The server follows the snapshot with a restored turn or cached settlement.
      replayTurn = snapshot && ['bidding', 'playing'].includes(snapshot.phase)
        ? { type: snapshot.phase === 'bidding' ? MsgType.BidTurn : MsgType.PlayTurn, playerId: snapshot.current_turn }
        : null;
      replayResult = snapshot?.phase === 'ended';
      dealt = Boolean(snapshot);
      dealID = snapshot?.deal_id ?? '';
      if (snapshot?.phase === 'dealing' && state.deal && state.deal.endAt > Date.now()) {
        audio.deal(state.deal.startAt, state.deal.endAt);
      }
      return;
    }
    if (message.type === MsgType.GameStart) {
      stop();
      replayTurn = null;
      replayResult = dealt = false;
      dealID = '';
      return;
    }
    if (!state.connected || !['dealing', 'bidding', 'playing', 'game_over'].includes(state.phase)) return;
    switch (message.type) {
      case MsgType.DealCards: {
        const payload = message.payload as IncomingPayloadMap['deal_cards'];
        // The landlord also receives a 20-card hand after taking the bottom cards.
        if (payload.deal_id && payload.deal_id !== dealID && state.deal) {
          endTurn();
          dealID = payload.deal_id;
          audio.deal(state.deal.startAt, state.deal.endAt);
          dealt = true;
        } else if (!payload.deal_id && !dealt && payload.cards?.length === 17) { audio.deal(); dealt = true; }
        break;
      }
      case MsgType.BidTurn:
      case MsgType.PlayTurn: {
        audio.endDeal();
        endTurn();
        const payload = message.payload as IncomingPayloadMap['play_turn'];
        const restored = replayTurn?.type === message.type && replayTurn.playerId === payload.player_id;
        replayTurn = null;
        if (restored || payload.player_id !== state.playerId || payload.timeout <= 0 || document.hidden || !audio.getEnabled()) break;
        const deadline = Date.now() + payload.timeout * 1000;
        audio.turn(false, deadline);
        warningTimer = window.setTimeout(() => {
          // A throttled background timer must never announce an expired turn.
          if (Date.now() < deadline) audio.turn(true, deadline);
        }, Math.max(0, (payload.timeout - WARNING_SECONDS) * 1000));
        expiryTimer = window.setTimeout(endTurn, payload.timeout * 1000);
        break;
      }
      case MsgType.BidResult: {
        endTurn();
        const payload = message.payload as IncomingPayloadMap['bid_result'];
        audio.bid(payload.bid, payload.is_grab);
        break;
      }
      case MsgType.CardPlayed: {
        if (state.phase !== 'playing') break;
        endTurn();
        replayResult = false;
        const payload = message.payload as IncomingPayloadMap['card_played'];
        audio.play(payload.hand_type, payload.cards ?? [], payload.cards_left);
        break;
      }
      case MsgType.PlayerPass:
        endTurn();
        audio.pass();
        break;
      case MsgType.Landlord:
        endTurn();
        break;
      case MsgType.GameOver: {
        endTurn();
        if (replayResult) break;
        replayResult = true;
        const payload = message.payload as IncomingPayloadMap['game_over'];
        const me = payload.scores?.find(score => score.player_id === state.playerId)
          ?? state.players.find(player => player.id === state.playerId);
        if (me) audio.result(me.is_landlord === payload.is_landlord);
        break;
      }
    }
  });
  const unsubscribeStore = useAppStore.subscribe((state, previous) => {
    if (!state.connected || state.roomCode !== previous.roomCode ||
        !['dealing', 'bidding', 'playing', 'game_over'].includes(state.phase)) stop();
  });
  const unsubscribeAudio = audio.subscribe(() => { if (!audio.getEnabled()) endTurn(); });
  const onVisibility = () => { if (document.hidden) stop(); };
  document.addEventListener('pointerup', audio.unlock);
  document.addEventListener('keydown', audio.unlock);
  document.addEventListener('visibilitychange', onVisibility);
  window.addEventListener('pagehide', stop);
  return () => {
    unsubscribe();
    unsubscribeStore();
    unsubscribeAudio();
    document.removeEventListener('pointerup', audio.unlock);
    document.removeEventListener('keydown', audio.unlock);
    document.removeEventListener('visibilitychange', onVisibility);
    window.removeEventListener('pagehide', stop);
    stop();
  };
}
