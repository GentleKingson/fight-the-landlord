import { useEffect, useMemo, useState } from 'react';
import { Lobby } from './features/lobby/Lobby';
import { GameTable } from './features/table/GameTable';
import { GameResult } from './features/table/GameResult';
import { GameEnd } from './features/table/GameEnd';
import { createGameSocket, type GameSocket } from './transport/wsClient';
import { useAppStore } from './stores/appStore';
import { seedDemoState } from './stores/demoState';
import { connectGameAudio } from './audio/connectGameAudio';

const ERROR_TOAST_DURATION_MS = 5000;

export function App() {
  const [collecting, setCollecting] = useState(false);
  useEffect(() => {
    fetch('/api/config').then(r => r.ok ? r.json() : null).then(c => setCollecting(c?.collect_training_data === true)).catch(() => {});
  }, []);
  const socket = useMemo<GameSocket>(() => createGameSocket(), []);
  const phase = useAppStore((state) => state.phase);
  const endView = useAppStore((state) => state.endView);
  const connected = useAppStore((state) => state.connected);
  const error = useAppStore((state) => state.error);
  const maintenance = useAppStore((state) => state.maintenance);

  useEffect(() => connectGameAudio(socket), [socket]);

  useEffect(() => {
    if (!connected || !error) return;

    const timeout = window.setTimeout(() => {
      useAppStore.getState().setError('');
    }, ERROR_TOAST_DURATION_MS);

    return () => window.clearTimeout(timeout);
  }, [connected, error]);

  useEffect(() => {
    const demo = new URLSearchParams(window.location.search).get('demo');
    if (demo) {
      seedDemoState(demo);
      return;
    }
    socket.connect();
    return () => socket.close();
  }, [socket]);

  return (
    <div className="app-shell">
      {collecting ? <div className="collection-notice" role="status">本服务已启用训练数据采集：记录匿名牌局、操作及胜负；不记录昵称、聊天或重连令牌。</div> : null}
      {maintenance ? <div className="maintenance-banner">服务器维护中，暂时无法开始新对局</div> : null}
      {!connected && !new URLSearchParams(window.location.search).get('demo') ? (
        <ConnectionState error={error} />
      ) : null}
      {connected && error ? <div className="connection-toast" role="alert"><span>{error}</span><button onClick={() => useAppStore.getState().setError('')}>关闭</button></div> : null}
      {phase === 'game_over' ? (
        endView === 'result' ? <GameResult socket={socket} /> : <GameEnd />
      ) : phase === 'dealing' || phase === 'bidding' || phase === 'playing' ? (
        <GameTable socket={socket} />
      ) : (
        <Lobby socket={socket} />
      )}
    </div>
  );
}

function ConnectionState({ error }: { error: string }) {
  return (
    <div className="connection-toast" role="status">
      {!error ? <span className="spinner" /> : null}
      <span>{error || '正在连接服务器...'}</span>
    </div>
  );
}
