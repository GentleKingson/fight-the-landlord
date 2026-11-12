import { useEffect, useRef } from 'react';
import { MsgType } from '../../protocol/types';
import type { GameSocket } from '../../transport/wsClient';
import { useAppStore } from '../../stores/appStore';
import { resolveSettlementOutcome } from '../../shared/ui/backgroundScene';

export function GameResult({ socket }: { socket: GameSocket }) {
  const playerId = useAppStore((state) => state.playerId);
  const winnerId = useAppStore((state) => state.winnerId);
  const winnerName = useAppStore((state) => state.winnerName);
  const winnerIsLandlord = useAppStore((state) => state.winnerIsLandlord);
  const finalMultiplier = useAppStore((state) => state.finalMultiplier);
  const scores = useAppStore((state) => state.scores);
  const snapshot = useAppStore((state) => state.endSnapshot);
  const reviewEndTable = useAppStore((state) => state.reviewEndTable);
  const outcome = resolveSettlementOutcome(playerId, winnerId, winnerIsLandlord, scores);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => { heading.current?.focus(); }, []);

  function playAgain() {
    const state = useAppStore.getState();
    if (state.phase === 'matching') return;
    if (socket.send(state.matchMode === 'practice' ? MsgType.PracticeMatch : MsgType.QuickMatch)) {
      state.leaveLocalRoom();
      useAppStore.setState({ phase: 'matching', error: '' });
    } else {
      state.setError('匹配请求发送失败，请等待连接恢复后重试');
    }
  }

  return (
    <main className="result-screen" data-outcome={outcome}>
      <section className="result-panel">
        <header className="result-heading">
          <span className="result-badge">{winnerIsLandlord ? '地主获胜' : '农民获胜'}</span>
          <h1 ref={heading} tabIndex={-1}>{winnerName || '本局'} 获胜</h1>
          <p>最终倍数 x{finalMultiplier || 1}</p>
        </header>
        <div className="score-list">
          {scores.map((score) => (
            <div className={`score-row${score.player_id === playerId ? ' is-me' : ''}`} key={score.player_id}>
              <span>{score.player_name}{score.player_id === playerId ? '（我）' : ''}</span>
              <strong className={score.score >= 0 ? 'is-positive' : 'is-negative'}>{score.score >= 0 ? '+' : ''}{score.score}</strong>
            </div>
          ))}
        </div>
        <div className="result-actions">
          <button className="primary-action" onClick={playAgain}>再来一局</button>
          <div className="room-actions">
            {snapshot ? <button className="secondary-action" onClick={reviewEndTable}>查看本局牌面</button> : null}
            <button className="secondary-action" onClick={() => { useAppStore.getState().leaveLocalRoom(); }}>返回大厅</button>
          </div>
        </div>
      </section>
    </main>
  );
}
