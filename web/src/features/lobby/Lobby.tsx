import { useCallback, useEffect, useRef, useState } from 'react';
import { MsgType } from '../../protocol/types';
import type { GameSocket } from '../../transport/wsClient';
import { useAppStore } from '../../stores/appStore';
import { SoundToggle } from '../../audio/SoundToggle';
import { Card } from '../../shared/cards/Card';
import { Icon } from '../../shared/ui/Icon';
import { useImeSafeEnter } from '../../shared/ui/imeSafeSubmit';
import { RulesPanel } from '../../shared/ui/RulesPanel';
import { useLobbyRequest } from './useLobbyRequest';
import landlordLogo from '../../assets/landlord-logo-176.webp';

interface LobbyProps {
  socket: GameSocket;
}

const navItems = [['home', '大厅'], ['leaderboard', '战绩榜'], ['stats', '我的战绩'], ['rules', '规则']] as const;
const roomRequestLabels = { create_room: '创建房间', join_room: '加入房间', ready: '准备', cancel_ready: '取消准备' } as const;

export function Lobby({ socket }: LobbyProps) {
  const phase = useAppStore((state) => state.phase);
  const panel = useAppStore((state) => state.lobbyPanel);
  const roomCode = useAppStore((state) => state.roomCode);
  const players = useAppStore((state) => state.players);
  const onlineCount = useAppStore((state) => state.onlineCount);
  const playerName = useAppStore((state) => state.playerName);
  const setLobbyPanel = useAppStore((state) => state.setLobbyPanel);
  const roomRequest = useAppStore(state => state.roomRequest);
  const activeSession = phase === 'matching' || phase === 'waiting';

  return (
    <main className="lobby-screen">
      <header className="lobby-header">
        <div className="brand-lockup">
          <img className="brand-mark" src={landlordLogo} alt="" width={44} height={44} />
          <div>
            <h1>斗地主</h1>
            <p>{playerName ? `${playerName}，准备开局` : '轻量牌桌，快速开局'}</p>
          </div>
        </div>
        <div className="lobby-header-tools">
          <SoundToggle />
          <div className="lobby-status">
            <span className="status-dot" />
            在线 {onlineCount || 0}
          </div>
        </div>
      </header>

      {phase === 'matching' ? <MatchingPanel socket={socket} /> : null}
      {phase === 'waiting' ? <RoomWaiting socket={socket} roomCode={roomCode} players={players} /> : null}
      {!activeSession ? (
        panel === 'home' ? <LobbyHome socket={socket} /> : <LobbySubPanel socket={socket} panel={panel} />
      ) : null}

      <nav className={`bottom-nav${activeSession ? ' is-locked' : ''}`} aria-label="大厅导航">
        {activeSession && <span className="nav-session" id="lobby-nav-reason">{phase === 'waiting'
          ? '房间等待中 · 离开房间后可使用大厅导航'
          : '匹配中 · 匹配结束后可使用大厅导航'}</span>}
        {navItems.map(([key, label]) => (
          <button key={key} disabled={activeSession || !!roomRequest} aria-current={!activeSession && panel === key ? 'page' : undefined}
            aria-describedby={activeSession ? 'lobby-nav-reason' : undefined}
            className={!activeSession && panel === key ? 'is-active' : ''} onClick={() => setLobbyPanel(key)}>{label}</button>
        ))}
      </nav>
    </main>
  );
}

function submitRoomRequest(socket: GameSocket, type: keyof typeof roomRequestLabels, roomCode?: string) {
  const state = useAppStore.getState();
  if (state.roomRequest) return;
  const label = roomRequestLabels[type];
  if (socket.send(type, roomCode ? { room_code: roomCode } : undefined)) {
    useAppStore.setState({ roomRequest: type, error: '', matchMode: type === MsgType.CreateRoom || type === MsgType.JoinRoom ? 'online' : state.matchMode });
  } else {
    state.setError(`${label}请求未发送，请连接恢复后重试`);
  }
}

function LobbyHome({ socket }: LobbyProps) {
  const { status, retry, requested } = useLobbyRequest(socket, MsgType.GetRoomList, MsgType.RoomListResult, false);
  const roomCodeInput = useAppStore((state) => state.roomCodeInput);
  const roomList = useAppStore((state) => state.roomList);
  const setRoomCodeInput = useAppStore((state) => state.setRoomCodeInput);
  const setError = useAppStore((state) => state.setError);
  const imeEnter = useImeSafeEnter();
  const pending = useAppStore(state => state.roomRequest);

  function joinRoom() {
    const roomCode = roomCodeInput.trim();
    if (!roomCode) {
      setError('请输入房间号');
      return;
    }
    submitRoomRequest(socket, MsgType.JoinRoom, roomCode);
  }

  function startMatch(mode: 'online' | 'practice') {
    if (useAppStore.getState().phase === 'matching') return;
    if (socket.send(mode === 'online' ? MsgType.QuickMatch : MsgType.PracticeMatch)) {
      useAppStore.setState({ phase: 'matching', matchMode: mode, error: '' });
    } else {
      setError('匹配请求发送失败，请等待连接恢复后重试');
    }
  }

  function refreshRooms() {
    retry();
  }

  function joinListedRoom(roomCode: string) {
    setRoomCodeInput(roomCode);
    submitRoomRequest(socket, MsgType.JoinRoom, roomCode);
  }

  return (
    <section className="lobby-home">
      <div className="quick-card">
        <div className="quick-card__art" aria-hidden="true">
          <span className="quick-card__back" />
          <Card card={{ rank: 13, suit: 1, color: 1 }} size="played" disabled />
          <Card card={{ rank: 14, suit: 0, color: 0 }} size="played" disabled />
        </div>
        <div className="quick-card__copy">
          <h2>快速开局</h2>
          <p>匹配在线玩家，进入标准三人牌桌。</p>
        </div>
        <button className="primary-action" disabled={!!pending} onClick={() => startMatch('online')}>
          <Icon name="play" /> 快速开局
        </button>
      </div>

      <div className="entry-grid">
        <button className="entry-tile" disabled={!!pending} onClick={() => submitRoomRequest(socket, MsgType.CreateRoom)}>
          <Icon name="room" />
          <strong>{pending === MsgType.CreateRoom ? '正在创建…' : '创建房间'}</strong>
          <span>好友同玩</span>
        </button>
        <button className="entry-tile" disabled={!!pending} onClick={() => startMatch('practice')}>
          <Icon name="bot" />
          <strong>人机练习</strong>
          <span>随时练手</span>
        </button>
      </div>

      <form className="join-strip" onSubmit={(event) => { event.preventDefault(); joinRoom(); }}>
        <label htmlFor="room-code">加入房间</label>
        <input
          id="room-code"
          name="room_code"
          value={roomCodeInput}
          onChange={(event) => setRoomCodeInput(event.target.value)}
          {...imeEnter}
          maxLength={8}
          placeholder="输入房间号"
          inputMode="numeric"
          enterKeyHint="go"
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="none"
          spellCheck={false}
        />
        <button type="submit" disabled={!!pending}>{pending === MsgType.JoinRoom ? '正在加入…' : '加入'}</button>
      </form>

      <section className="room-browser" aria-label="可加入房间">
        <div className="room-browser__head">
          <strong>可加入房间</strong>
          <button className="secondary-action" disabled={!!pending} onClick={refreshRooms}>刷新</button>
        </div>
        <div className="room-browser__list">
          {status ? <p role="status">{status}</p> : roomList.length ? roomList.map((room) => (
            <button className="room-browser__row" disabled={!!pending} key={room.room_code} onClick={() => joinListedRoom(room.room_code)}>
              <span>{room.room_code}</span>
              <em>{room.player_count}/{room.max_players || 3}</em>
            </button>
          )) : <p className="empty-text">{requested ? '当前没有可加入的房间，可创建房间邀请好友或刷新重试。' : '点击刷新查看可加入房间，也可创建房间邀请好友。'}</p>}
        </div>
      </section>
    </section>
  );
}

function MatchingPanel({ socket }: LobbyProps) {
  const mode = useAppStore(state => state.matchMode);
  const cancelling = useAppStore(state => state.cancellingMatch);
  const connected = useAppStore(state => state.connected);
  const [started] = useState(Date.now);
  const [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => setElapsed(Math.floor((Date.now() - started) / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [started]);
  return <section className="state-panel">
    <span className="spinner spinner--large" aria-hidden="true" />
    <h2>{mode === 'practice' ? '正在准备人机对局' : '正在匹配在线玩家'}</h2>
    <p>已等待 {elapsed} 秒</p>
    <p role="status">{cancelling ? '正在确认取消，请稍候…' : elapsed >= 30 ? mode === 'online' ? '等待时间较长，可取消匹配后重试或选择人机练习。' : '准备时间较长，请刷新页面恢复服务器状态。' : mode === 'practice' ? '正在安排两位电脑牌友。' : '正在寻找牌友，请稍候。'}</p>
    {mode === 'online' ? <button className="secondary-action" disabled={!connected || cancelling} onClick={() => {
      if (socket.send(MsgType.CancelMatch)) useAppStore.setState({ cancellingMatch: true });
      else useAppStore.getState().setError('取消请求未发送，请连接恢复后重试');
    }}>取消匹配</button> : elapsed >= 30 ? <p>若始终未进入牌桌，请刷新页面恢复服务器状态。</p> : null}
  </section>;
}

function legacyCopyText(text: string): boolean {
  if (typeof document === 'undefined' || typeof document.execCommand !== 'function') {
    return false;
  }
  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  textarea.style.pointerEvents = 'none';
  document.body.appendChild(textarea);
  textarea.select();
  try {
    return document.execCommand('copy');
  } catch {
    return false;
  } finally {
    textarea.remove();
  }
}

async function writeTextToClipboard(text: string): Promise<void> {
  if (typeof navigator !== 'undefined' && navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
    await navigator.clipboard.writeText(text);
    return;
  }
  if (!legacyCopyText(text)) {
    throw new Error('Clipboard API unavailable');
  }
}

function RoomWaiting({ socket, roomCode, players }: LobbyProps & { roomCode: string; players: ReturnType<typeof useAppStore.getState>['players'] }) {
  const playerId = useAppStore((state) => state.playerId);
  const me = players.find((player) => player.id === playerId);
  const pending = useAppStore(state => state.roomRequest);
  const codeRef = useRef<HTMLElement | null>(null);
  const resetTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const [statusMessage, setStatusMessage] = useState('');

  const supportsInvokerCommands = typeof window !== 'undefined' && 'CommandEvent' in window;
  const canShare = typeof navigator !== 'undefined' && typeof navigator.share === 'function';

  const announce = useCallback((nextState: 'copied' | 'failed', message: string) => {
    if (resetTimerRef.current !== null) {
      clearTimeout(resetTimerRef.current);
    }
    setCopyState(nextState);
    setStatusMessage((prev) => (prev === message ? `${message}\u00A0` : message));
    resetTimerRef.current = setTimeout(() => {
      setCopyState('idle');
      setStatusMessage('');
      resetTimerRef.current = null;
    }, 2000);
  }, []);

  useEffect(() => {
    return () => {
      if (resetTimerRef.current !== null) {
        clearTimeout(resetTimerRef.current);
      }
    };
  }, []);

  const handleCopyRoomCode = useCallback(async () => {
    if (!roomCode) return;
    try {
      await writeTextToClipboard(roomCode);
      announce('copied', `已复制房间号 ${roomCode}`);
    } catch {
      announce('failed', '复制失败，请手动复制房间号');
    }
  }, [announce, roomCode]);

  const handleShareRoomCode = useCallback(async () => {
    if (!roomCode || typeof navigator === 'undefined' || typeof navigator.share !== 'function') return;
    const shareData: ShareData = {
      title: '斗地主房间邀请',
      text: `快来加入我的斗地主房间，房间号：${roomCode}`
    };
    try {
      if (typeof navigator.canShare === 'function' && !navigator.canShare(shareData)) {
        await handleCopyRoomCode();
        return;
      }
      await navigator.share(shareData);
      announce('copied', `已分享房间号 ${roomCode}`);
    } catch (error) {
      if ((error as { name?: string } | null)?.name === 'AbortError') {
        return;
      }
      announce('failed', '分享失败，请复制房间号');
    }
  }, [announce, handleCopyRoomCode, roomCode]);

  useEffect(() => {
    const codeElement = codeRef.current;
    if (!supportsInvokerCommands || !codeElement) return;
    const onCommand = (event: Event) => {
      if ((event as Event & { command?: string }).command === '--copy') {
        void handleCopyRoomCode();
      }
    };
    codeElement.addEventListener('command', onCommand);
    return () => {
      codeElement.removeEventListener('command', onCommand);
    };
  }, [handleCopyRoomCode, supportsInvokerCommands]);

  return (
    <section className="room-waiting">
      <div className="room-code-panel">
        <span>房间号</span>
        <strong id="waiting-room-code" ref={codeRef}>{roomCode || '...'}</strong>
        <div className="room-code-tools">
          <button
            type="button"
            className="room-copy-button"
            command="--copy"
            commandfor="waiting-room-code"
            disabled={!roomCode}
            data-copied={copyState === 'copied' ? '' : undefined}
            data-failed={copyState === 'failed' ? '' : undefined}
            onClick={supportsInvokerCommands ? undefined : () => void handleCopyRoomCode()}
          >
            <Icon name={copyState === 'copied' ? 'check' : 'copy'} />
            <span>{copyState === 'copied' ? '已复制' : copyState === 'failed' ? '复制失败' : '复制房间号'}</span>
          </button>
          {canShare ? (
            <button
              type="button"
              className="room-share-button"
              disabled={!roomCode}
              onClick={() => void handleShareRoomCode()}
            >
              <Icon name="share" />
              <span>分享房间号</span>
            </button>
          ) : null}
        </div>
        <div className="room-code-footer">
          <p className={copyState !== 'idle' ? 'is-muted-out' : ''}>
            {players.length}/3 人已入座 · {players.length < 3 ? `还缺 ${3 - players.length} 人` : '等待所有玩家准备'}
          </p>
          <div
            className={`room-copy-status ${copyState !== 'idle' ? 'is-visible' : ''} ${copyState === 'failed' ? 'is-error' : ''}`}
            role="status"
            aria-live="polite"
          >
            {statusMessage}
          </div>
        </div>
      </div>
      <div className="seat-list">
        {Array.from({ length: 3 }, (_, index) => {
          const player = players.find((item) => item.seat === index);
          return (
            <div className={`seat-row${player?.id === playerId ? ' is-me' : ''}${!player ? ' is-empty' : ''}`} key={index}>
              <span>{index + 1}</span>
              <strong>{player?.name || '等待加入'}</strong>
              {player ? <em>{!player.online ? '离线' : player.ready ? '已准备' : '未准备'}</em> : null}
            </div>
          );
        })}
      </div>
      <div className="room-actions">
        <button className="primary-action" disabled={!!pending} onClick={() => submitRoomRequest(socket, me?.ready ? MsgType.CancelReady : MsgType.Ready)}>
          {pending ? `正在${roomRequestLabels[pending]}…` : me?.ready ? '取消准备' : '准备开始'}
        </button>
        <button className="secondary-action" disabled={!!pending} onClick={() => {
          if (socket.send(MsgType.LeaveRoom)) useAppStore.getState().leaveLocalRoom();
          else useAppStore.getState().setError('离房请求未发送，请连接恢复后重试');
        }}>
          离开房间
        </button>
      </div>
    </section>
  );
}

function LobbySubPanel({ socket, panel }: LobbyProps & { panel: string }) {
  if (panel === 'leaderboard') return <LeaderboardPanel socket={socket} />;
  if (panel === 'stats') return <StatsPanel socket={socket} />;
  return (
    <section className="sub-panel">
      <header className="sub-panel__head">
        <div className="sub-panel__title">
          <h2>玩法说明</h2>
          <p>标准三人斗地主规则与操作指引</p>
        </div>
      </header>
      <RulesPanel />
    </section>
  );
}

function LeaderboardPanel({ socket }: LobbyProps) {
  const { status, retry } = useLobbyRequest(socket, MsgType.GetLeaderboard, MsgType.LeaderboardResult);
  const entries = useAppStore((state) => state.leaderboard);
  const playerId = useAppStore((state) => state.playerId);
  const isLoading = status === '正在加载…';

  return (
    <section className="sub-panel sub-panel--leaderboard">
      <header className="sub-panel__head">
        <div className="sub-panel__title">
          <h2>战绩榜</h2>
          <p>按累计积分展示前 30 名牌友</p>
        </div>
        <button
          type="button"
          className={`secondary-action sub-panel__reload${isLoading ? ' is-loading' : ''}`}
          onClick={retry}
        >
          <Icon name="refresh" />
          <span>重新加载</span>
        </button>
      </header>

      {status ? (
        <div className={`sub-panel__state${isLoading ? '' : ' is-error'}`} role="status">
          {isLoading ? <span className="spinner spinner--large" aria-hidden="true" /> : null}
          <p>{status}</p>
        </div>
      ) : entries.length ? (
        <div className="ranking-board">
          <div className="ranking-head" aria-hidden="true">
            <span>排名</span>
            <span>玩家</span>
            <span>胜场 / 胜率</span>
            <span>总积分</span>
          </div>
          <div className="ranking-list">
            {entries.map((entry, index) => {
              const rank = entry.rank || index + 1;
              const isMe = Boolean(playerId && entry.player_id === playerId);
              const wins = entry.wins ?? 0;
              const winRate = Number.isFinite(entry.win_rate) ? entry.win_rate : 0;
              return (
                <div
                  className={`ranking-row${isMe ? ' is-me' : ''}${rank <= 3 ? ` is-top-${rank}` : ''}`}
                  key={`${entry.player_id}_${index}`}
                >
                  <span className="ranking-rank">{rank <= 3 ? rank : `#${rank}`}</span>
                  <div className="ranking-player">
                    <strong>{entry.player_name}</strong>
                    {isMe ? <small className="me-tag">我</small> : null}
                  </div>
                  <span className="ranking-meta">{wins} 胜 · 胜率 {winRate.toFixed(1)}%</span>
                  <em>{entry.score} 分</em>
                </div>
              );
            })}
          </div>
        </div>
      ) : (
        <div className="sub-panel__state">
          <span className="state-emblem" aria-hidden="true">
            <Icon name="rank" />
          </span>
          <strong>榜单虚位以待</strong>
          <p className="empty-text">尚无玩家积分记录，完成对局后再来查看</p>
        </div>
      )}
    </section>
  );
}

function StatsPanel({ socket }: LobbyProps) {
  const { status, retry } = useLobbyRequest(socket, MsgType.GetStats, MsgType.StatsResult);
  const stats = useAppStore((state) => state.stats);
  const playerName = useAppStore((state) => state.playerName);
  const setLobbyPanel = useAppStore((state) => state.setLobbyPanel);
  const isLoading = status === '正在加载…';

  const hasPlayedGames = Boolean(
    stats && (
      stats.total_games > 0 ||
      stats.wins > 0 ||
      (stats.losses ?? 0) > 0 ||
      stats.score !== 0 ||
      stats.rank > 0
    )
  );
  const displayName = stats?.player_name || playerName;

  const totalGames = stats?.total_games ?? 0;
  const wins = stats?.wins ?? 0;
  const losses = stats?.losses ?? Math.max(0, totalGames - wins);
  const rawWinRate = stats && Number.isFinite(stats.win_rate)
    ? stats.win_rate
    : totalGames > 0 ? (wins / totalGames) * 100 : 0;
  const winRate = Math.min(100, Math.max(0, rawWinRate));
  const score = stats?.score ?? 0;
  const rankLabel = stats && stats.rank > 0 ? `#${stats.rank}` : '未上榜';
  const currentStreak = stats?.current_streak ?? 0;
  const maxWinStreak = stats?.max_win_streak ?? 0;
  const streakBadge = currentStreak > 0
    ? `${currentStreak} 连胜中`
    : currentStreak < 0
      ? `${Math.abs(currentStreak)} 连败`
      : '状态平稳';
  const streakTileValue = currentStreak > 0
    ? `${currentStreak} 连胜`
    : currentStreak < 0
      ? `${Math.abs(currentStreak)} 连败`
      : '暂无连胜';

  const landlordGames = stats?.landlord_games ?? 0;
  const landlordWins = stats?.landlord_wins ?? 0;
  const landlordLosses = Math.max(0, landlordGames - landlordWins);
  const landlordRate = landlordGames > 0 ? Math.min(100, Math.max(0, (landlordWins / landlordGames) * 100)) : 0;

  const farmerGames = stats?.farmer_games ?? 0;
  const farmerWins = stats?.farmer_wins ?? 0;
  const farmerLosses = Math.max(0, farmerGames - farmerWins);
  const farmerRate = farmerGames > 0 ? Math.min(100, Math.max(0, (farmerWins / farmerGames) * 100)) : 0;

  return (
    <section className="sub-panel sub-panel--stats">
      <header className="sub-panel__head">
        <div className="sub-panel__title">
          <h2>我的战绩</h2>
          <p>个人对局总览、连胜走势与阵营胜率</p>
        </div>
        <button
          type="button"
          className={`secondary-action sub-panel__reload${isLoading ? ' is-loading' : ''}`}
          onClick={retry}
        >
          <Icon name="refresh" />
          <span>重新加载</span>
        </button>
      </header>

      {status ? (
        <div className={`sub-panel__state${isLoading ? '' : ' is-error'}`} role="status">
          {isLoading ? <span className="spinner spinner--large" aria-hidden="true" /> : null}
          <p>{status}</p>
        </div>
      ) : hasPlayedGames && stats ? (
        <div className="stats-dashboard">
          <div className="stats-overview">
            <div className="stats-identity">
              <span className="player-avatar stats-avatar" aria-hidden="true">
                {Array.from(displayName || '牌')[0]}
              </span>
              <div className="stats-identity__copy">
                <div className="stats-identity__row">
                  <h3>{displayName || '当前玩家'}</h3>
                  <span className={`streak-pill${currentStreak > 0 ? ' is-hot' : currentStreak < 0 ? ' is-cold' : ''}`}>
                    {streakBadge}
                  </span>
                </div>
                <p>累计对局 {totalGames} 场 · {wins} 胜 {losses} 负</p>
              </div>
            </div>

            <div className="stats-kpis">
              <div className="stats-kpi">
                <span>当前积分</span>
                <strong className={score < 0 ? 'is-negative' : ''}>{score > 0 ? `+${score}` : score}</strong>
              </div>
              <div className="stats-kpi">
                <span>总榜排名</span>
                <strong>{rankLabel}</strong>
              </div>
            </div>

            <div className="stats-winbar">
              <div className="stats-winbar__head">
                <span>综合胜率</span>
                <strong>{winRate.toFixed(1)}%</strong>
              </div>
              <div className="stats-meter" aria-hidden="true">
                <span style={{ width: `${winRate}%` }} />
              </div>
            </div>
          </div>

          <div className="stats-grid">
            <Stat label="总局数" value={totalGames} meta="已完成场次" />
            <Stat label="胜局 / 负局" value={`${wins} / ${losses}`} meta={`胜场差 ${wins - losses >= 0 ? `+${wins - losses}` : wins - losses}`} />
            <Stat label="当前走势" value={streakTileValue} meta="近期连续战况" />
            <Stat label="最高连胜" value={maxWinStreak} meta="历史最佳纪录" />
          </div>

          <div className="stats-roles">
            <RoleStat
              role="landlord"
              title="地主战绩"
              tag="独战两家"
              games={landlordGames}
              wins={landlordWins}
              losses={landlordLosses}
              rate={landlordRate}
              totalGames={totalGames}
            />
            <RoleStat
              role="farmer"
              title="农民战绩"
              tag="联手抗衡"
              games={farmerGames}
              wins={farmerWins}
              losses={farmerLosses}
              rate={farmerRate}
              totalGames={totalGames}
            />
          </div>
        </div>
      ) : (
        <div className="stats-empty-wrap">
          <div className="sub-panel__state">
            <span className="state-emblem" aria-hidden="true">
              <Icon name="stats" />
            </span>
            <strong>{displayName ? `${displayName}，尚未开启战绩档案` : '尚未开启战绩档案'}</strong>
            <p className="empty-text">你还没有对局记录，完成一局后再来查看</p>
            <button type="button" className="primary-action" onClick={() => setLobbyPanel('home')}>
              <Icon name="play" /> 前往大厅开局
            </button>
          </div>
          <div className="stats-preview-strip" aria-hidden="true">
            <div>
              <span>当前积分</span>
              <strong>0</strong>
            </div>
            <div>
              <span>总榜排名</span>
              <strong>未上榜</strong>
            </div>
            <div>
              <span>阵营胜率</span>
              <strong>待解锁</strong>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}

function Stat({ label, value, meta }: { label: string; value: string | number; meta: string }) {
  return (
    <div className="stat-tile">
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{meta}</small>
    </div>
  );
}

function RoleStat({
  role,
  title,
  tag,
  games,
  wins,
  losses,
  rate,
  totalGames
}: {
  role: 'landlord' | 'farmer';
  title: string;
  tag: string;
  games: number;
  wins: number;
  losses: number;
  rate: number;
  totalGames: number;
}) {
  const share = totalGames > 0 ? Math.round((games / totalGames) * 100) : 0;
  return (
    <div className={`role-stat role-stat--${role}`}>
      <div className="role-stat__head">
        <div className="role-stat__title">
          <span className="role-badge">{role === 'landlord' ? '地主' : '农民'}</span>
          <div className="role-stat__heading">
            <h3>{title}</h3>
            <small>{tag}</small>
          </div>
        </div>
        <div className="role-stat__rate-box">
          <span>阵营胜率</span>
          <strong className="role-stat__rate">{games > 0 ? `${rate.toFixed(1)}%` : '—'}</strong>
        </div>
      </div>
      <div className="stats-meter" aria-hidden="true">
        <span style={{ width: `${games > 0 ? rate : 0}%` }} />
      </div>
      <dl className="role-stat__figures">
        <div>
          <dt>出场</dt>
          <dd>{games} 局</dd>
        </div>
        <div>
          <dt>战况</dt>
          <dd>{games > 0 ? `${wins} 胜 ${losses} 负` : '暂无记录'}</dd>
        </div>
        <div>
          <dt>占比</dt>
          <dd>{share}%</dd>
        </div>
      </dl>
    </div>
  );
}
