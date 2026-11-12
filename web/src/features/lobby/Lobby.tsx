import { useCallback, useEffect, useRef, useState } from 'react';
import { MsgType } from '../../protocol/types';
import type { GameSocket } from '../../transport/wsClient';
import { useAppStore, useChatStore } from '../../stores/appStore';
import { Card } from '../../shared/cards/Card';
import { Icon } from '../../shared/ui/Icon';
import { useImeSafeEnter } from '../../shared/ui/imeSafeSubmit';
import { RulesPanel } from '../../shared/ui/RulesPanel';
import { useLobbyRequest } from './useLobbyRequest';
import landlordLogo from '../../assets/landlord-logo-176.webp';

interface LobbyProps {
  socket: GameSocket;
}

export function Lobby({ socket }: LobbyProps) {
  const phase = useAppStore((state) => state.phase);
  const panel = useAppStore((state) => state.lobbyPanel);
  const roomCode = useAppStore((state) => state.roomCode);
  const players = useAppStore((state) => state.players);
  const onlineCount = useAppStore((state) => state.onlineCount);
  const playerName = useAppStore((state) => state.playerName);
  const setLobbyPanel = useAppStore((state) => state.setLobbyPanel);

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
        <div className="lobby-status">
          <span className="status-dot" />
          在线 {onlineCount || 0}
        </div>
      </header>

      {phase === 'matching' ? <MatchingPanel socket={socket} /> : null}
      {phase === 'waiting' ? <RoomWaiting socket={socket} roomCode={roomCode} players={players} /> : null}
      {phase !== 'matching' && phase !== 'waiting' ? (
        panel === 'home' ? <LobbyHome socket={socket} /> : <LobbySubPanel socket={socket} panel={panel} />
      ) : null}

      <nav className="bottom-nav" aria-label="大厅导航">
        <button aria-current={panel === 'home' ? 'page' : undefined} className={panel === 'home' ? 'is-active' : ''} onClick={() => setLobbyPanel('home')}>大厅</button>
        <button aria-current={panel === 'leaderboard' ? 'page' : undefined} className={panel === 'leaderboard' ? 'is-active' : ''} onClick={() => { setLobbyPanel('leaderboard'); }}>
          战绩榜
        </button>
        <button aria-current={panel === 'stats' ? 'page' : undefined} className={panel === 'stats' ? 'is-active' : ''} onClick={() => { setLobbyPanel('stats'); }}>
          我的战绩
        </button>
        <button aria-current={panel === 'chat' ? 'page' : undefined} className={panel === 'chat' ? 'is-active' : ''} onClick={() => setLobbyPanel('chat')}>聊天</button>
      <button aria-current={panel === 'rules' ? 'page' : undefined} className={panel === 'rules' ? 'is-active' : ''} onClick={() => setLobbyPanel('rules')}>规则</button>
      </nav>
    </main>
  );
}

function LobbyHome({ socket }: LobbyProps) {
  const { status, retry, requested } = useLobbyRequest(socket, MsgType.GetRoomList, MsgType.RoomListResult, false);
  const roomCodeInput = useAppStore((state) => state.roomCodeInput);
  const roomList = useAppStore((state) => state.roomList);
  const setRoomCodeInput = useAppStore((state) => state.setRoomCodeInput);
  const setError = useAppStore((state) => state.setError);
  const imeEnter = useImeSafeEnter();

  function joinRoom() {
    const roomCode = roomCodeInput.trim();
    if (!roomCode) {
      setError('请输入房间号');
      return;
    }
    useAppStore.setState({ matchMode: 'online' });
    socket.send(MsgType.JoinRoom, { room_code: roomCode });
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
    useAppStore.setState({ matchMode: 'online' });
    socket.send(MsgType.JoinRoom, { room_code: roomCode });
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
        <button className="primary-action" onClick={() => startMatch('online')}>
          <Icon name="play" /> 快速开局
        </button>
      </div>

      <div className="entry-grid">
        <button className="entry-tile" onClick={() => { useAppStore.setState({ matchMode: 'online' }); socket.send(MsgType.CreateRoom); }}>
          <Icon name="room" />
          <strong>创建房间</strong>
          <span>好友同玩</span>
        </button>
        <button className="entry-tile" onClick={() => startMatch('practice')}>
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
        <button type="submit">加入</button>
      </form>

      <section className="room-browser" aria-label="可加入房间">
        <div className="room-browser__head">
          <strong>可加入房间</strong>
          <button className="secondary-action" onClick={refreshRooms}>刷新</button>
        </div>
        <div className="room-browser__list">
          {status ? <p role="status">{status}</p> : roomList.length ? roomList.map((room) => (
            <button className="room-browser__row" key={room.room_code} onClick={() => joinListedRoom(room.room_code)}>
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
        <div
          className={`room-copy-status ${copyState !== 'idle' ? 'is-visible' : ''} ${copyState === 'failed' ? 'is-error' : ''}`}
          role="status"
          aria-live="polite"
        >
          {statusMessage}
        </div>
        <p>{players.length}/3 人已入座 · {players.length < 3 ? `还缺 ${3 - players.length} 人` : '等待所有玩家准备'}</p>
      </div>
      <div className="seat-list">
        {Array.from({ length: 3 }, (_, index) => {
          const player = players.find((item) => item.seat === index);
          return (
            <div className={`seat-row ${player?.id === playerId ? 'is-me' : ''}`} key={index}>
              <span>{index + 1}</span>
              <strong>{player?.name || '等待加入'}</strong>
              {player ? <em>{!player.online ? '离线' : player.ready ? '已准备' : '未准备'}</em> : null}
            </div>
          );
        })}
      </div>
      <div className="room-actions">
        <button className="primary-action" onClick={() => socket.send(me?.ready ? MsgType.CancelReady : MsgType.Ready)}>
          {me?.ready ? '取消准备' : '准备开始'}
        </button>
        <button className="secondary-action" onClick={() => { socket.send(MsgType.LeaveRoom); useAppStore.getState().leaveLocalRoom(); }}>
          离开房间
        </button>
      </div>
    </section>
  );
}

function LobbySubPanel({ socket, panel }: LobbyProps & { panel: string }) {
  if (panel === 'leaderboard') return <LeaderboardPanel socket={socket} />;
  if (panel === 'stats') return <StatsPanel socket={socket} />;
  if (panel === 'chat') return <LobbyChat socket={socket} />;
  return <section className="sub-panel"><h2>玩法说明</h2><RulesPanel /></section>;
}

function LeaderboardPanel({ socket }: LobbyProps) {
  const { status, retry } = useLobbyRequest(socket, MsgType.GetLeaderboard, MsgType.LeaderboardResult);
  const entries = useAppStore((state) => state.leaderboard);
  return (
    <section className="sub-panel">
      <h2>战绩榜</h2><button className="secondary-action" onClick={retry}>重新加载</button>
      <div className="ranking-list">
        {status ? <p role="status">{status}</p> : entries.length ? entries.map((entry, index) => (
          <div className="ranking-row" key={`${entry.player_id}_${index}`}>
            <span>#{entry.rank || index + 1}</span>
            <strong>{entry.player_name}</strong>
            <em>{entry.score} 分</em>
          </div>
        )) : <p className="empty-text">尚无玩家积分记录，完成对局后再来查看</p>}
      </div>
    </section>
  );
}

function StatsPanel({ socket }: LobbyProps) {
  const { status, retry } = useLobbyRequest(socket, MsgType.GetStats, MsgType.StatsResult);
  const stats = useAppStore((state) => state.stats);
  return (
    <section className="sub-panel">
      <h2>我的战绩</h2><button className="secondary-action" onClick={retry}>重新加载</button>
      {status ? <p role="status">{status}</p> : stats ? (
        <div className="stats-grid">
          <Stat label="总局数" value={stats.total_games} />
          <Stat label="胜局" value={stats.wins} />
          <Stat label="胜率" value={`${stats.win_rate.toFixed(1)}%`} />
          <Stat label="积分" value={stats.score} />
          <Stat label="排名" value={`#${stats.rank || '-'}`} />
          <Stat label="最高连胜" value={stats.max_win_streak} />
        </div>
      ) : <p className="empty-text">你还没有对局记录，完成一局后再来查看</p>}
    </section>
  );
}

function LobbyChat({ socket }: LobbyProps) {
  const messages = useChatStore((state) => state.messages);
  const chatInput = useAppStore((state) => state.chatInput);
  const setChatInput = useAppStore((state) => state.setChatInput);
  const imeEnter = useImeSafeEnter();

  function send() {
    const content = chatInput.trim();
    if (!content) return;
    if (socket.send(MsgType.Chat, { content, scope: 'lobby' })) setChatInput('');
    else useAppStore.getState().setError('聊天发送失败，草稿已保留，请等待连接恢复后重试');
  }

  return (
    <section className="sub-panel chat-panel">
      <h2>大厅聊天</h2>
      <div className="chat-feed">
        {messages.filter((message) => message.scope !== 'room').slice(-20).map((message, index) => (
          <p key={index}><strong>{message.sender_name || '玩家'}:</strong> {message.content}</p>
        ))}
      </div>
      <form className="chat-input-row" onSubmit={(event) => { event.preventDefault(); send(); }}>
        <label className="visually-hidden" htmlFor="lobby-chat-input">大厅聊天消息</label>
        <input
          id="lobby-chat-input"
          name="message"
          enterKeyHint="send"
          value={chatInput}
          onChange={(event) => setChatInput(event.target.value)}
          {...imeEnter}
          placeholder="和大厅里的玩家聊聊"
        />
        <button type="submit">发送</button>
      </form>
    </section>
  );
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="stat-tile">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
