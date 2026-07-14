import { useEffect, useMemo, useState } from 'react';
import { Lobby } from './features/lobby/Lobby';
import { GameTable } from './features/table/GameTable';
import { GameResult } from './features/table/GameResult';
import { createGameSocket, type GameSocket } from './transport/wsClient';
import { useAppStore } from './stores/appStore';
import { resolveDemoMode } from './stores/demoMode';
import { seedDemoState } from './stores/demoState';
import { BusinessErrorBanner } from './shared/ui/BusinessErrorBanner';
import { checkVersionCompatibility, type VersionGateResult } from './version/compatibility';
import { VersionGateScreen } from './version/VersionGateScreen';

type VersionGateState =
  | { status: 'checking' }
  | { status: 'compatible' }
  | { status: 'blocked'; result: Exclude<VersionGateResult, { status: 'compatible' }> }
  | { status: 'failed'; error: string };

export function App() {
  const socket = useMemo<GameSocket>(() => createGameSocket(), []);
  const phase = useAppStore((state) => state.phase);
  const connected = useAppStore((state) => state.connected);
  const error = useAppStore((state) => state.error);
  const reconnectNotice = useAppStore((state) => state.reconnectNotice);
  const maintenance = useAppStore((state) => state.maintenance);
  const demoMode = resolveDemoMode(window.location.search);
  const [versionGate, setVersionGate] = useState<VersionGateState>(
    demoMode ? { status: 'compatible' } : { status: 'checking' }
  );
  const [versionCheckAttempt, setVersionCheckAttempt] = useState(0);

  useEffect(() => {
    if (demoMode) {
      seedDemoState(demoMode);
      return;
    }
    const controller = new AbortController();
    setVersionGate({ status: 'checking' });
    void checkVersionCompatibility(undefined, (input, init) => fetch(input, { ...init, signal: controller.signal }))
      .then((result) => {
        if (controller.signal.aborted) return;
        setVersionGate(result.status === 'compatible'
          ? { status: 'compatible' }
          : { status: 'blocked', result });
      })
      .catch((caught: unknown) => {
        if (controller.signal.aborted) return;
        setVersionGate({ status: 'failed', error: errorMessage(caught) });
      });
    return () => controller.abort();
  }, [demoMode, versionCheckAttempt]);

  useEffect(() => {
    if (demoMode || versionGate.status !== 'compatible') return;
    socket.connect();
    return () => socket.shutdown();
  }, [demoMode, socket, versionGate.status]);

  if (!demoMode && versionGate.status !== 'compatible') {
    return (
      <VersionGateScreen
        checking={versionGate.status === 'checking'}
        result={versionGate.status === 'blocked' ? versionGate.result : undefined}
        error={versionGate.status === 'failed' ? versionGate.error : undefined}
        onRetry={() => {
          if (versionGate.status === 'blocked') window.location.reload();
          else setVersionCheckAttempt((attempt) => attempt + 1);
        }}
      />
    );
  }

  return (
    <div className="app-shell">
      {maintenance ? <div className="maintenance-banner">服务器维护中，暂时无法开始新对局</div> : null}
      {!connected && !demoMode ? (
        <ConnectionState error={error} />
      ) : null}
      {connected && reconnectNotice ? (
        <div className="connection-toast" role="status">{reconnectNotice}</div>
      ) : null}
      {connected ? <BusinessErrorBanner socket={socket} /> : null}
      {phase === 'game_over' ? (
        <GameResult socket={socket} />
      ) : phase === 'bidding' || phase === 'playing' ? (
        <GameTable socket={socket} />
      ) : (
        <Lobby socket={socket} />
      )}
    </div>
  );
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function ConnectionState({ error }: { error: string }) {
  return (
    <div className="connection-toast" role="status">
      <span className="spinner" />
      <span>{error || '正在连接服务器...'}</span>
    </div>
  );
}
