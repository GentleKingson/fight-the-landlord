import type { GameSocket } from '../../transport/wsClient';
import { useAppStore } from '../../stores/appStore';
import { PlayedCards } from '../../shared/cards/PlayedCards';
import { dispatchCommand } from '../../transport/commandDispatcher';

export function GameResult({ socket }: { socket: GameSocket }) {
  const winnerName = useAppStore((state) => state.winnerName);
  const winnerIsLandlord = useAppStore((state) => state.winnerIsLandlord);
  const finalMultiplier = useAppStore((state) => state.finalMultiplier);
  const scores = useAppStore((state) => state.scores);
  const playerHands = useAppStore((state) => state.playerHands);
  const settlementSyncError = useAppStore((state) => state.settlementSyncError);
  const readyPending = useAppStore((state) => Boolean(state.pendingCommands.ready));
  const leavePending = useAppStore((state) => Boolean(state.pendingCommands['leave-room']));

  if (settlementSyncError) {
    return (
      <main className="result-screen">
        <section className="result-panel" role="alert">
          <h1>结算同步失败</h1>
          <p>{settlementSyncError}</p>
          <div className="room-actions">
            <button className="secondary-action" disabled={leavePending} onClick={() => dispatchCommand(socket, { kind: 'leave-room' })}>
              {leavePending ? '返回中...' : '返回大厅'}
            </button>
          </div>
        </section>
      </main>
    );
  }

  return (
    <main className="result-screen">
      <section className="result-panel">
        <span className="result-badge">{winnerIsLandlord ? '地主获胜' : '农民获胜'}</span>
        <h1>{winnerName || '本局'} 获胜</h1>
        <p>最终倍数 x{finalMultiplier || 1}</p>
        <div className="score-list">
          {scores.map((score) => (
            <div className="score-row" key={score.player_id}>
              <span>{score.player_name}</span>
              <strong className={score.score >= 0 ? 'is-positive' : 'is-negative'}>{score.score >= 0 ? '+' : ''}{score.score}</strong>
            </div>
          ))}
        </div>
        <div className="remaining-hands">
          {playerHands.map((playerHand) => (
            <PlayedCards key={playerHand.player_id} cards={playerHand.cards} playerName={playerHand.player_name} compact />
          ))}
        </div>
        <div className="room-actions">
          <button className="primary-action" disabled={readyPending || leavePending} onClick={() => dispatchCommand(socket, { kind: 'ready' })}>
            {readyPending ? '等待确认...' : '再来一局'}
          </button>
          <button className="secondary-action" disabled={leavePending || readyPending} onClick={() => dispatchCommand(socket, { kind: 'leave-room' })}>
            {leavePending ? '返回中...' : '返回大厅'}
          </button>
        </div>
      </section>
    </main>
  );
}
