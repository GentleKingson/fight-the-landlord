import { useEffect, useRef } from 'react';
import { MsgType } from '../../protocol/types';
import type { GameSocket } from '../../transport/wsClient';
import { useAppStore } from '../../stores/appStore';

export function GameResult({ socket }: { socket: GameSocket }) {
  const winnerName = useAppStore((state) => state.winnerName);
  const winnerIsLandlord = useAppStore((state) => state.winnerIsLandlord);
  const finalMultiplier = useAppStore((state) => state.finalMultiplier);
  const scores = useAppStore((state) => state.scores);
  const snapshot = useAppStore((state) => state.endSnapshot);
  const reviewEndTable = useAppStore((state) => state.reviewEndTable);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => { heading.current?.focus(); }, []);

  return (
    <main className="result-screen">
      <section className="result-panel">
        <header className="result-heading">
          <span className="result-badge">{winnerIsLandlord ? '地主获胜' : '农民获胜'}</span>
          <h1 ref={heading} tabIndex={-1}>{winnerName || '本局'} 获胜</h1>
          <p>最终倍数 x{finalMultiplier || 1}</p>
        </header>
        <div className="score-list">
          {scores.map((score) => (
            <div className="score-row" key={score.player_id}>
              <span>{score.player_name}</span>
              <strong className={score.score >= 0 ? 'is-positive' : 'is-negative'}>{score.score >= 0 ? '+' : ''}{score.score}</strong>
            </div>
          ))}
        </div>
        {snapshot ? <button className="secondary-action" onClick={reviewEndTable}>查看本局牌面</button> : null}
        <div className="room-actions">
          <button className="primary-action" onClick={() => { const mode = useAppStore.getState().matchMode; useAppStore.getState().leaveLocalRoom(); useAppStore.setState({ phase: 'matching' }); socket.send(mode === 'practice' ? MsgType.PracticeMatch : MsgType.QuickMatch); }}>再来一局</button>
          <button className="secondary-action" onClick={() => { useAppStore.getState().leaveLocalRoom(); }}>返回大厅</button>
        </div>
      </section>
    </main>
  );
}
