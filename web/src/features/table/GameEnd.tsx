import { useEffect, useRef, useState } from 'react';
import { useAppStore } from '../../stores/appStore';
import { END_REVEAL_DURATION_MS, type EndSnapshot } from '../../stores/endGame';
import { RESULT_ACTION_GUARD_MS } from '../../shared/resultPresentation';
import { SoundToggle } from '../../audio/SoundToggle';
import type { CardInfo, PlayerInfo } from '../../protocol/types';
import { Card } from '../../shared/cards/Card';

export function GameEnd() {
  const snapshot = useAppStore(state => state.endSnapshot);
  const view = useAppStore(state => state.endView);
  const deadline = useAppStore(state => state.revealDeadline);
  const showSettlement = useAppStore(state => state.showSettlement);
  const [now, setNow] = useState(Date.now);
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => { heading.current?.focus(); }, [view, snapshot]);
  useEffect(() => {
    if (!snapshot || view !== 'reveal' || deadline === null) return;
    const tick = () => {
      const time = Date.now();
      setNow(time);
      const state = useAppStore.getState();
      if (time >= deadline && state.endView === 'reveal' && state.revealDeadline === deadline) showSettlement();
    };
    tick();
    const interval = window.setInterval(tick, 250);
    document.addEventListener('visibilitychange', tick);
    window.addEventListener('focus', tick);
    window.addEventListener('pageshow', tick);
    return () => {
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', tick);
      window.removeEventListener('focus', tick);
      window.removeEventListener('pageshow', tick);
    };
  }, [snapshot, view, deadline, showSettlement]);

  if (!snapshot) return (
    <main className="result-screen">
      <section className="result-panel end-recovery">
        <h1 ref={heading} tabIndex={-1}>正在恢复本局结果</h1>
        <p role="status">牌局已结束，正在等待完整结算数据…</p>
        <button className="secondary-action" onClick={() => useAppStore.getState().leaveLocalRoom()}>返回大厅</button>
      </section>
    </main>
  );

  const me = snapshot.players.find(player => player.id === snapshot.playerId) ?? snapshot.players[0];
  const left = snapshot.players.find(player => player.seat === ((me?.seat ?? 0) + 2) % 3);
  const right = snapshot.players.find(player => player.seat === ((me?.seat ?? 0) + 1) % 3);
  const remaining = deadline === null ? 0 : Math.max(0, Math.ceil((deadline - now) / 1000));
  const review = view === 'review';
  const unlockAt = deadline === null ? 0 : deadline - END_REVEAL_DURATION_MS + RESULT_ACTION_GUARD_MS;

  return (
    <main className="table-screen end-table">
      <header className="end-table__heading">
        <div>
          <span className="end-table__eyebrow">斗地主 · 房间 {snapshot.roomCode || '练习桌'}</span>
          <h1 ref={heading} tabIndex={-1}>{review ? '本局牌面' : '本局已结束'}</h1>
        </div>
        <div className="end-table__tools"><span>{review ? '仅供查看 · 不限时' : '三家剩牌已公开'}</span><SoundToggle /></div>
      </header>

      <section className="table-arena end-table__board" aria-label="终局牌桌" tabIndex={0}>
        <section className="end-table__winning" aria-label="获胜最后一手">
          <h2>{snapshot.winnerName || '获胜玩家'} · 获胜最后一手</h2>
          {snapshot.lastPlayed.length ? <>
            <p>{snapshot.lastHandType}</p>
            <ReadOnlyCards cards={snapshot.lastPlayed} />
          </> : <p>最后一手数据暂不可用</p>}
        </section>
        <RemainingHand player={left} side="left" label="上家" snapshot={snapshot} />
        <RemainingHand player={right} side="right" label="下家" snapshot={snapshot} />
        <RemainingHand player={me} side="me" label="我" snapshot={snapshot} />
      </section>

      <footer className="end-table__footer">
        <p>{review ? '查看完成后可返回结算' : <><span aria-live="off">{remaining} 秒后显示结算</span><span className="end-table__review-hint"> · 结算后可不限时回看</span></>}</p>
        <button className="primary-action" disabled={!review && now < unlockAt} onClick={event => {
          // Preserve the final-play gesture guard when replacing the live table.
          if (!review && (event.detail > 1 || Date.now() < unlockAt)) return;
          showSettlement();
        }}>{review ? '返回结算' : '查看结算'}</button>
      </footer>
    </main>
  );
}

function RemainingHand({ player, side, label, snapshot }: {
  player?: PlayerInfo; side: 'left' | 'right' | 'me'; label: string; snapshot: EndSnapshot;
}) {
  if (!player) return null;
  const hand = snapshot.playerHands.find(hand => hand.player_id === player.id);
  return (
    <section className={`end-table__hand end-table__hand--${side}`} aria-label={`${label}剩余手牌`}>
      <h2>{player.name} <span>· {label} · {player.is_landlord ? '地主' : '农民'}</span></h2>
      <p>{hand ? `剩余 ${hand.cards.length} 张` : '剩牌数据暂不可用'}{player.id === snapshot.winnerId ? ' · 本家出完' : ''}</p>
      {hand?.cards.length ? <ReadOnlyCards cards={hand.cards} /> : hand ? <div className="end-table__empty">已出完</div> : null}
    </section>
  );
}

function ReadOnlyCards({ cards }: { cards: CardInfo[] }) {
  return <div className="end-table__cards">
    {cards.map((card, index) => <Card key={`${card.suit}_${card.rank}_${index}`} card={card} size="played" disabled />)}
  </div>;
}
