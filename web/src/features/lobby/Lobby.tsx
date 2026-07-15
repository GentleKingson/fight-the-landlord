import { useEffect, useRef, useState } from 'react';
import type { GameSocket } from '../../transport/wsClient';
import { selectChatMessages, useAppStore, useChatStore } from '../../stores/appStore';
import { Icon } from '../../shared/ui/Icon';
import { createChatCommand, dispatchCommand } from '../../transport/commandDispatcher';

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
  const pendingQuickMatch = useAppStore((state) => state.pendingCommands['quick-match']);
  const pendingPracticeMatch = useAppStore((state) => state.pendingCommands['practice-match']);
  const showingMatch = phase === 'matching' || Boolean(pendingQuickMatch || pendingPracticeMatch);
  const canLogout = phase === 'lobby' && roomCode === '' && !showingMatch;
  const [loggingOut, setLoggingOut] = useState(false);

  async function logout() {
    if (loggingOut) return;
    setLoggingOut(true);
    const revoked = await socket.logout();
    if (revoked) {
      window.location.reload();
      return;
    }
    setLoggingOut(false);
  }

  return (
    <main className="lobby-screen">
      <header className="lobby-header">
        <div className="brand-lockup">
          <span className="brand-mark">斗</span>
          <div>
            <h1>斗地主</h1>
            <p>{playerName ? `${playerName}，准备开局` : '轻量牌桌，快速开局'}</p>
          </div>
        </div>
        <div className="lobby-header__actions">
          <div className="lobby-status">
            <span className="status-dot" />
            在线 {onlineCount || 0}
          </div>
          {canLogout ? (
            <button
              className="logout-action"
              type="button"
              disabled={loggingOut}
              onClick={() => { void logout(); }}
              aria-label="退出并撤销本机会话"
              title="退出并撤销本机会话"
            >
              <Icon name="logout" />
              <span>{loggingOut ? '退出中...' : '退出'}</span>
            </button>
          ) : null}
        </div>
      </header>

      {showingMatch ? <MatchingPanel socket={socket} acknowledged={phase === 'matching'} /> : null}
      {phase === 'waiting' ? <RoomWaiting socket={socket} roomCode={roomCode} players={players} /> : null}
      {!showingMatch && phase !== 'waiting' ? (
        panel === 'home' ? <LobbyHome socket={socket} /> : <LobbySubPanel socket={socket} panel={panel} />
      ) : null}

      <nav className="bottom-nav" aria-label="大厅导航">
        <button className={panel === 'home' ? 'is-active' : ''} onClick={() => setLobbyPanel('home')}>大厅</button>
        <button className={panel === 'leaderboard' ? 'is-active' : ''} onClick={() => { setLobbyPanel('leaderboard'); dispatchCommand(socket, { kind: 'leaderboard', leaderboardType: 'total', offset: 0, limit: 30 }); }}>
          战绩榜
        </button>
        <button className={panel === 'stats' ? 'is-active' : ''} onClick={() => { setLobbyPanel('stats'); dispatchCommand(socket, { kind: 'stats' }); }}>
          我的战绩
        </button>
        <button className={panel === 'chat' ? 'is-active' : ''} onClick={() => setLobbyPanel('chat')}>聊天</button>
      </nav>
    </main>
  );
}

function LobbyHome({ socket }: LobbyProps) {
  const roomCodeInput = useAppStore((state) => state.roomCodeInput);
  const roomList = useAppStore((state) => state.roomList);
  const setRoomCodeInput = useAppStore((state) => state.setRoomCodeInput);
  const pending = useAppStore((state) => state.pendingCommands);
  const roomTransitionPending = Boolean(
    pending['create-room']
    || pending['join-room']
    || pending['quick-match']
    || pending['practice-match']
  );

  function joinRoom() {
    const roomCode = roomCodeInput.trim();
    dispatchCommand(socket, { kind: 'join-room', roomCode });
  }

  function refreshRooms() {
    dispatchCommand(socket, { kind: 'room-list' });
  }

  function joinListedRoom(roomCode: string) {
    setRoomCodeInput(roomCode);
    dispatchCommand(socket, { kind: 'join-room', roomCode });
  }

  return (
    <section className="lobby-home">
      <div className="quick-card">
        <div className="quick-card__copy">
          <h2>快速开局</h2>
          <p>匹配在线玩家，进入标准三人牌桌。</p>
        </div>
        <button
          className="primary-action"
          disabled={roomTransitionPending}
          onClick={() => dispatchCommand(socket, { kind: 'quick-match' })}
        >
          <Icon name="play" /> {pending['quick-match'] ? '请求中...' : '快速开局'}
        </button>
      </div>

      <div className="entry-grid">
        <button className="entry-tile" disabled={roomTransitionPending} onClick={() => dispatchCommand(socket, { kind: 'create-room' })}>
          <Icon name="room" />
          <strong>创建房间</strong>
          <span>好友同玩</span>
        </button>
        <button className="entry-tile" disabled={roomTransitionPending} onClick={() => dispatchCommand(socket, { kind: 'practice-match' })}>
          <Icon name="bot" />
          <strong>人机练习</strong>
          <span>随时练手</span>
        </button>
      </div>

      <div className="join-strip">
        <label htmlFor="room-code">加入房间</label>
        <input id="room-code" value={roomCodeInput} onChange={(event) => setRoomCodeInput(event.target.value)} maxLength={8} placeholder="输入房间号" />
        <button disabled={roomTransitionPending} onClick={joinRoom}>{pending['join-room'] ? '加入中...' : '加入'}</button>
      </div>

      <section className="room-browser" aria-label="可加入房间">
        <div className="room-browser__head">
          <strong>可加入房间</strong>
          <button className="secondary-action" onClick={refreshRooms}>刷新</button>
        </div>
        <div className="room-browser__list">
          {roomList.length ? roomList.map((room) => (
            <button className="room-browser__row" disabled={roomTransitionPending} key={room.room_code} onClick={() => joinListedRoom(room.room_code)}>
              <span>{room.room_code}</span>
              <em>{room.player_count}/{room.max_players || 3}</em>
            </button>
          )) : <p className="empty-text">暂无可加入房间，点击刷新查看。</p>}
        </div>
      </section>
    </section>
  );
}

function MatchingPanel({ socket, acknowledged }: LobbyProps & { acknowledged: boolean }) {
  const deadlineMs = useAppStore((state) => state.matchDeadlineMs);
  const practice = useAppStore((state) => state.matchPractice);
  const cancelPending = useAppStore((state) => Boolean(state.pendingCommands['cancel-match']));
  const setBusinessError = useAppStore((state) => state.setBusinessError);
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    if (!acknowledged || !deadlineMs) return;
    const interval = window.setInterval(() => setNow(Date.now()), 500);
    return () => window.clearInterval(interval);
  }, [acknowledged, deadlineMs]);

  useEffect(() => {
    if (!acknowledged || !deadlineMs) return;
    const delay = Math.max(0, deadlineMs - Date.now() + 250);
    const timer = window.setTimeout(() => {
      const result = dispatchCommand(socket, { kind: 'cancel-match' });
      if (result.ok) {
        setBusinessError(
          'timeout',
          '匹配等待超时，正在退出队列',
          practice ? undefined : { kind: 'quick-match' },
          practice ? 'practice-match' : 'quick-match'
        );
      }
    }, delay);
    return () => window.clearTimeout(timer);
  }, [acknowledged, deadlineMs, practice, setBusinessError, socket]);

  const secondsLeft = acknowledged && deadlineMs ? Math.max(0, Math.ceil((deadlineMs - now) / 1000)) : 0;

  return (
    <section className="state-panel">
      <span className="spinner spinner--large" />
      <h2>{acknowledged ? (practice ? '正在创建练习牌桌' : '正在寻找牌友') : '正在提交匹配请求'}</h2>
      <p>{acknowledged ? `系统正在为你安排一张三人牌桌${secondsLeft ? `，剩余 ${secondsLeft} 秒` : ''}。` : '等待服务器确认匹配请求。'}</p>
      <button
        className="secondary-action"
        disabled={!acknowledged || cancelPending}
        onClick={() => dispatchCommand(socket, { kind: 'cancel-match' })}
      >
        {cancelPending ? '取消中...' : '取消匹配'}
      </button>
    </section>
  );
}

function RoomWaiting({ socket, roomCode, players }: LobbyProps & { roomCode: string; players: ReturnType<typeof useAppStore.getState>['players'] }) {
  const playerId = useAppStore((state) => state.playerId);
  const me = players.find((player) => player.id === playerId);
  const pending = useAppStore((state) => state.pendingCommands);
  const readyKind = me?.ready ? 'cancel-ready' : 'ready';
  const readyPending = Boolean(pending[readyKind]);
  const leavePending = Boolean(pending['leave-room']);

  return (
    <section className="room-waiting">
      <div className="room-code-panel">
        <span>房间号</span>
        <strong>{roomCode || '...'}</strong>
        <p>{players.length}/3 人已入座</p>
      </div>
      <div className="seat-list">
        {Array.from({ length: 3 }, (_, index) => {
          const player = players.find((item) => item.seat === index) ?? players[index];
          return (
            <div className={`seat-row ${player?.id === playerId ? 'is-me' : ''}`} key={index}>
              <span>{index + 1}</span>
              <strong>{player?.name || '等待加入'}</strong>
              {player ? <em>{player.ready ? '已准备' : '等待中'}</em> : null}
            </div>
          );
        })}
      </div>
      <div className="room-actions">
        <button
          className="primary-action"
          disabled={readyPending || leavePending}
          onClick={() => dispatchCommand(socket, { kind: readyKind })}
        >
          {readyPending ? '等待确认...' : (me?.ready ? '取消准备' : '准备开始')}
        </button>
        <button className="secondary-action" disabled={leavePending || readyPending} onClick={() => dispatchCommand(socket, { kind: 'leave-room' })}>
          {leavePending ? '离开中...' : '离开房间'}
        </button>
      </div>
    </section>
  );
}

function LobbySubPanel({ socket, panel }: LobbyProps & { panel: string }) {
  if (panel === 'leaderboard') return <LeaderboardPanel />;
  if (panel === 'stats') return <StatsPanel />;
  if (panel === 'chat') return <LobbyChat socket={socket} />;
  return <RulesPanel />;
}

function LeaderboardPanel() {
  const entries = useAppStore((state) => state.leaderboard);
  return (
    <section className="sub-panel">
      <h2>战绩榜</h2>
      <div className="ranking-list">
        {entries.length ? entries.map((entry, index) => (
          <div className="ranking-row" key={`${entry.player_id}_${index}`}>
            <span>#{entry.rank || index + 1}</span>
            <strong>{entry.player_name}</strong>
            <em>{entry.score} 分</em>
          </div>
        )) : <p className="empty-text">暂无排行榜数据</p>}
      </div>
    </section>
  );
}

function StatsPanel() {
  const stats = useAppStore((state) => state.stats);
  return (
    <section className="sub-panel">
      <h2>我的战绩</h2>
      {stats ? (
        <div className="stats-grid">
          <Stat label="总局数" value={stats.total_games} />
          <Stat label="胜局" value={stats.wins} />
          <Stat label="胜率" value={`${stats.win_rate.toFixed(1)}%`} />
          <Stat label="积分" value={stats.score} />
          <Stat label="排名" value={`#${stats.rank || '-'}`} />
          <Stat label="最高连胜" value={stats.max_win_streak} />
        </div>
      ) : <p className="empty-text">点击底部“我的战绩”获取数据</p>}
    </section>
  );
}

function LobbyChat({ socket }: LobbyProps) {
  const messages = useChatStore(selectChatMessages('lobby'));
  const chatInput = useAppStore((state) => state.chatInput);
  const setChatInput = useAppStore((state) => state.setChatInput);
  const chatPending = useAppStore((state) => Boolean(state.pendingCommands.chat));
  const composingRef = useRef(false);

  function send() {
    if (chatPending || composingRef.current) return;
    const content = chatInput.trim();
    if (!content) return;
    const result = dispatchCommand(socket, createChatCommand(content, 'lobby'));
    if (result.ok) setChatInput('');
  }

  return (
    <section className="sub-panel chat-panel">
      <h2>大厅聊天</h2>
      <div className="chat-feed" role="log" aria-label="大厅聊天记录" tabIndex={0}>
        {messages.slice(-20).map((message) => (
          <p key={message.message_id}><strong>{message.sender_name || '玩家'}:</strong> {message.content}</p>
        ))}
      </div>
      <div className="chat-input-row">
        <input
          aria-label="大厅聊天消息"
          value={chatInput}
          maxLength={240}
          onChange={(event) => setChatInput(event.target.value)}
          placeholder="和大厅里的玩家聊聊"
          onCompositionStart={() => { composingRef.current = true; }}
          onCompositionEnd={() => { composingRef.current = false; }}
          onKeyDown={(event) => { if (event.key === 'Enter' && !event.nativeEvent.isComposing) send(); }}
        />
        <button disabled={chatPending} onClick={send}>{chatPending ? '发送中...' : '发送'}</button>
      </div>
    </section>
  );
}

function RulesPanel() {
  return (
    <section className="sub-panel rules-panel">
      <h2>玩法说明</h2>
      <p>地主独自对抗两名农民，任意一方率先出完手牌即可获胜。</p>
      <p>常见牌型包括单张、对子、三张、顺子、连对、飞机、炸弹和王炸。</p>
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
