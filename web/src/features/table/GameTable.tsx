import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { canBeat as canBeatHand, findSmallestLegalResponse, handTypeLabels, parseHand } from '../../game/rules';
import type { GameSocket } from '../../transport/wsClient';
import { useAppStore, useChatStore, type SeatAction, type TableAction } from '../../stores/appStore';
import { cardKey } from '../../shared/cards/cardModel';
import { Card } from '../../shared/cards/Card';
import { CardBack } from '../../shared/cards/CardBack';
import { Hand } from '../../shared/cards/Hand';
import { PlayedCards } from '../../shared/cards/PlayedCards';
import { Icon } from '../../shared/ui/Icon';
import { createChatCommand, dispatchCommand } from '../../transport/commandDispatcher';
import type { CardInfo, PlayerInfo, UtilityDrawer } from '../../protocol/types';
import { remainingSeconds } from '../../stores/slices/clock';

interface GameTableProps {
  socket: GameSocket;
}

const UTILITY_DRAWER_ID = 'table-utility-drawer';
const UTILITY_DRAWER_TITLE_ID = 'table-utility-drawer-title';

export function GameTable({ socket }: GameTableProps) {
  const playerId = useAppStore((state) => state.playerId);
  const players = useAppStore((state) => state.players);
  const phase = useAppStore((state) => state.phase);
  const currentTurn = useAppStore((state) => state.currentTurn);
  const hand = useAppStore((state) => state.hand);
  const selectedCards = useAppStore((state) => state.selectedCards);
  const bottomCards = useAppStore((state) => state.bottomCards);
  const bottomCardsRevealed = useAppStore((state) => state.bottomCardsRevealed);
  const lastPlayed = useAppStore((state) => state.lastPlayed);
  const lastPlayedName = useAppStore((state) => state.lastPlayedName);
  const lastHandType = useAppStore((state) => state.lastHandType);
  const seatActions = useAppStore((state) => state.seatActions);
  const multiplier = useAppStore((state) => state.multiplier);
  const baseScore = useAppStore((state) => state.baseScore);
  const roomCode = useAppStore((state) => state.roomCode);
  const latency = useAppStore((state) => state.latency);
  const drawer = useAppStore((state) => state.drawer);
  const setDrawer = useAppStore((state) => state.setDrawer);
  const toggleCard = useAppStore((state) => state.toggleCard);
  const setSelection = useAppStore((state) => state.setSelection);
  const clearSelection = useAppStore((state) => state.clearSelection);
  const isMyTurn = currentTurn === playerId;
  const seats = useMemo(() => arrangeSeats(players, playerId), [players, playerId]);
  const drawerTriggerRef = useRef<HTMLButtonElement>(null);
  const openDrawer = useCallback((nextDrawer: UtilityDrawer, trigger: HTMLButtonElement) => {
    drawerTriggerRef.current = trigger;
    setDrawer(drawer === nextDrawer ? 'none' : nextDrawer);
  }, [drawer, setDrawer]);
  const closeDrawer = useCallback(() => setDrawer('none'), [setDrawer]);

  return (
    <main className="table-screen">
      <header className="table-topbar" aria-label="牌桌状态">
        <div className="table-brand">
          <span className="table-brand__logo">斗地主</span>
          <span>房间号 {roomCode || '练习桌'}</span>
        </div>
        <div className="table-score-strip">
          <span>底分 {baseScore}</span>
          <strong>倍数 x{multiplier ?? 1}</strong>
        </div>
        <div className="table-network">
          <span className="network-dot" />
          <span>{latency ? `${latency}ms` : '在线'}</span>
        </div>
        <div className="table-tools">
          <ToolButton drawer="counter" label="记牌器" icon="counter" active={drawer === 'counter'} onOpen={openDrawer} />
          <ToolButton drawer="chat" label="聊天" icon="chat" active={drawer === 'chat'} onOpen={openDrawer} />
          <ToolButton drawer="history" label="历史" icon="history" active={drawer === 'history'} onOpen={openDrawer} />
        </div>
      </header>

      <section className="table-arena" aria-label="斗地主牌桌">
        <SeatPanel player={seats.left} side="left" active={currentTurn === seats.left?.id} action={seats.left ? seatActions[seats.left.id] : undefined} />
        <SeatPanel player={seats.right} side="right" active={currentTurn === seats.right?.id} action={seats.right ? seatActions[seats.right.id] : undefined} />

        <BottomCards cards={bottomCards} revealed={bottomCardsRevealed} />

        <div className="felt-table">
          <div className="table-center-hint">{phase === 'bidding' ? '叫抢阶段' : '观察各家出牌'}</div>
          <PlayedCards cards={lastPlayed} handType={lastHandType} playerName={lastPlayedName || '上一手'} />
        </div>

        <TurnBanner />
      </section>

      <ActionBar socket={socket} isMyTurn={isMyTurn} phase={phase} />

      <section className="hand-zone">
        {seats.me ? <PlayerBadge player={seats.me} active={currentTurn === playerId} me /> : null}
        <div className="self-action-lane">
          {seats.me ? <PlayedActionBubble action={seatActions[seats.me.id]} self /> : null}
        </div>
        <Hand
          cards={hand}
          selected={selectedCards}
          disabled={phase !== 'playing'}
          onToggle={toggleCard}
          onRangeSelect={setSelection}
        />
        {selectedCards.size ? (
          <button className="clear-selection" onClick={clearSelection}>重选</button>
        ) : null}
      </section>

      <UtilityDrawer socket={socket} drawer={drawer} onClose={closeDrawer} returnFocusRef={drawerTriggerRef} />
    </main>
  );
}

function BottomCards({ cards, revealed }: { cards: CardInfo[]; revealed: boolean }) {
  return (
    <div className={`bottom-cards ${revealed ? 'is-revealed' : 'is-hidden'}`} aria-label="底牌">
      <span>{revealed ? '底牌' : '底牌未揭'}</span>
      <div>
        {revealed && cards.length ? cards.map((card, index) => (
          <Card key={`${card.suit}_${card.rank}_${index}`} card={card} size="mini" />
        )) : (
          <>
            <span className="mini-card-placeholder" />
            <span className="mini-card-placeholder" />
            <span className="mini-card-placeholder" />
          </>
        )}
      </div>
    </div>
  );
}

function ToolButton({ drawer, label, icon, active, onOpen }: {
  drawer: UtilityDrawer;
  label: string;
  icon: 'chat' | 'counter' | 'history' | 'rules';
  active: boolean;
  onOpen: (drawer: UtilityDrawer, trigger: HTMLButtonElement) => void;
}) {
  return (
    <button
      className="tool-button"
      onClick={(event) => onOpen(drawer, event.currentTarget)}
      title={label}
      aria-label={label}
      aria-haspopup="dialog"
      aria-controls={UTILITY_DRAWER_ID}
      aria-expanded={active}
    >
      <Icon name={icon} />
      <span>{label}</span>
    </button>
  );
}

function SeatPanel({ player, side, active, action }: { player?: PlayerInfo; side: 'left' | 'right'; active: boolean; action?: SeatAction }) {
  if (!player) {
    return <aside className={`seat-panel seat-panel--${side}`} aria-hidden="true" />;
  }
  return (
    <aside className={`seat-panel seat-panel--${side} ${active ? 'is-active' : ''}`}>
      <PlayerBadge player={player} active={active} />
      <PlayedActionBubble action={action} side={side} />
      <CardBack count={player.cards_count ?? 0} />
    </aside>
  );
}

function PlayedActionBubble({ action, side, self = false }: { action?: SeatAction; side?: 'left' | 'right'; self?: boolean }) {
  if (!action) {
    return <div className={`played-action played-action--empty ${self ? 'played-action--self' : ''}`} aria-hidden="true" />;
  }

  const label = action.label || (action.type === 'pass' ? '不出' : action.hand_type || '出牌');
  const hasCards = action.type === 'play' && Boolean(action.cards?.length);

  return (
    <div className={`played-action played-action--${action.type} ${side ? `played-action--${side}` : ''} ${self ? 'played-action--self' : ''}`}>
      {hasCards ? (
        <div className="played-action__cards" aria-label={label}>
          {action.cards?.map((card, index) => (
            <Card key={`${card.suit}_${card.rank}_${index}`} card={card} size="action" />
          ))}
        </div>
      ) : (
        <strong>{label}</strong>
      )}
      {hasCards && action.hand_type ? <span>{action.hand_type}</span> : null}
    </div>
  );
}

function PlayerBadge({ player, active, me = false }: { player: PlayerInfo; active: boolean; me?: boolean }) {
  return (
    <div className={`player-badge ${active ? 'is-active' : ''} ${me ? 'is-me' : ''}`}>
      <span className="player-avatar" aria-hidden="true">{player.name.slice(0, 1)}</span>
      <div>
        <strong>{player.name}{me ? ' 我' : ''}</strong>
        <span>{player.is_landlord ? '地主' : '农民'} · 剩余 {player.cards_count ?? 0}</span>
      </div>
      {player.is_landlord ? <em>地主</em> : null}
      {!player.online ? <em>离线</em> : null}
    </div>
  );
}

function TurnBanner() {
  const playerId = useAppStore((state) => state.playerId);
  const players = useAppStore((state) => state.players);
  const currentTurn = useAppStore((state) => state.currentTurn);
  const turnDeadlineMs = useAppStore((state) => state.turnDeadlineMs);
  const serverClockOffsetMs = useAppStore((state) => state.serverClockOffsetMs);
  const phase = useAppStore((state) => state.phase);
  const isGrabTurn = useAppStore((state) => state.isGrabTurn);
  const recentActions = useAppStore((state) => state.recentActions);
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 400);
    const resumeFromVisibility = () => setNow(Date.now());
    document.addEventListener('visibilitychange', resumeFromVisibility);
    return () => {
      window.clearInterval(id);
      document.removeEventListener('visibilitychange', resumeFromVisibility);
    };
  }, []);

  const remaining = remainingSeconds(turnDeadlineMs, serverClockOffsetMs, now);
  const actor = players.find((player) => player.id === currentTurn);
  const isMe = currentTurn === playerId;
  const lastBid = [...recentActions].reverse().find((action) => action.type === 'bid');
  const title = isMe ? '轮到你' : `等待 ${actor?.name || '玩家'}`;
  const subtitle = phase === 'bidding'
    ? (isGrabTurn ? '抢地主阶段' : '叫地主阶段')
    : '出牌阶段';

  return (
    <div className={`turn-banner ${isMe ? 'is-mine' : ''}`}>
      <div className="turn-banner__copy">
        <strong>{title}</strong>
        <span>{lastBid?.label ? `${lastBid.player_name || '玩家'}：${lastBid.label}` : subtitle}</span>
      </div>
      <time>{remaining}</time>
    </div>
  );
}

function ActionBar({ socket, isMyTurn, phase }: GameTableProps & { isMyTurn: boolean; phase: string }) {
  const hand = useAppStore((state) => state.hand);
  const selectedCards = useAppStore((state) => state.selectedCards);
  const lastPlayed = useAppStore((state) => state.lastPlayed);
  const mustPlay = useAppStore((state) => state.mustPlay);
  const isGrabTurn = useAppStore((state) => state.isGrabTurn);
  const connectionStatus = useAppStore((state) => state.connectionStatus);
  const maintenance = useAppStore((state) => state.maintenance);
  const pendingPlay = useAppStore((state) => Boolean(state.pendingCommands.play));
  const pendingPass = useAppStore((state) => Boolean(state.pendingCommands.pass));
  const pendingBid = useAppStore((state) => Boolean(state.pendingCommands.bid));
  const setSelection = useAppStore((state) => state.setSelection);
  const clearSelection = useAppStore((state) => state.clearSelection);
  const selected = hand.filter((card) => selectedCards.has(cardKey(card)));
  const parsedSelection = parseHand(selected);
  const summary = parsedSelection ? handTypeLabels[parsedSelection.type] : (selected.length ? '无效牌型' : '');
  const socketReady = connectionStatus === 'connected';
  const selectionBeatsPrevious = mustPlay || canBeatHand(selected, lastPlayed);
  const canPlay = isMyTurn
    && phase === 'playing'
    && parsedSelection !== null
    && selectionBeatsPrevious
    && socketReady
    && !pendingPlay
    && !pendingPass
    && !maintenance;

  function play() {
    if (!canPlay) {
      useAppStore.setState({ tableMessage: selected.length ? '当前牌型不可出' : '请选择要出的牌' });
      return;
    }
    const result = dispatchCommand(socket, { kind: 'play', cards: selected });
    if (!result.ok) {
      if (result.reason !== 'duplicate') useAppStore.setState({ tableMessage: '出牌发送失败，请检查连接后重试' });
    }
  }

  function pass() {
    if (mustPlay) {
      useAppStore.setState({ tableMessage: '本轮必须出牌' });
      return;
    }
    const result = dispatchCommand(socket, { kind: 'pass' });
    if (!result.ok) {
      if (result.reason !== 'duplicate') useAppStore.setState({ tableMessage: '不出操作发送失败，请检查连接后重试' });
    }
  }

  function hint() {
    const suggestion = findSmallestLegalResponse(hand, mustPlay ? null : lastPlayed);
    if (!suggestion) {
      useAppStore.setState({ tableMessage: mustPlay ? '暂无提示' : '没有可压过的牌' });
      return;
    }
    const parsedSuggestion = parseHand(suggestion);
    setSelection(suggestion.map(cardKey));
    useAppStore.setState({ tableMessage: `提示：${parsedSuggestion ? handTypeLabels[parsedSuggestion.type] : '合法牌型'}` });
  }

  if (phase === 'bidding') {
    return (
      <section className="action-bar action-bar--bidding" aria-label="叫地主操作">
        {isMyTurn ? (
          <>
            <button className="primary-action" disabled={!socketReady || maintenance || pendingBid} onClick={() => dispatchCommand(socket, { kind: 'bid', bid: true })}>{pendingBid ? '等待确认...' : (isGrabTurn ? '抢地主' : '叫地主')}</button>
            <button className="secondary-action secondary-action--muted" disabled={!socketReady || maintenance || pendingBid} onClick={() => dispatchCommand(socket, { kind: 'bid', bid: false })}>{pendingBid ? '等待确认...' : (isGrabTurn ? '不抢' : '不叫')}</button>
          </>
        ) : <span>等待其他玩家{isGrabTurn ? '抢地主' : '叫地主'}...</span>}
      </section>
    );
  }

  return (
    <section className="action-bar" aria-label="出牌操作">
      <button className="secondary-action secondary-action--blue" disabled={!isMyTurn || mustPlay || !socketReady || maintenance || pendingPlay || pendingPass} onClick={pass}>{pendingPass ? '等待确认...' : '不出'}</button>
      <button className="secondary-action secondary-action--green" disabled={!isMyTurn || !socketReady || maintenance} onClick={hint}>提示</button>
      <button className="secondary-action secondary-action--muted" disabled={!selectedCards.size} onClick={clearSelection}>重选</button>
      <button className="primary-action" disabled={!canPlay} onClick={play}>{pendingPlay ? '等待确认...' : '出牌'}</button>
      <ActionSummary selectedCount={selected.length} summary={summary} />
    </section>
  );
}

function ActionSummary({ selectedCount, summary }: { selectedCount: number; summary: string }) {
  const tableMessage = useAppStore((state) => state.tableMessage);
  return (
    <small className={tableMessage ? 'is-warning' : ''}>
      {tableMessage || (selectedCount ? `已选 ${selectedCount} 张 · ${summary}` : '选择手牌后出牌')}
    </small>
  );
}

function UtilityDrawer({ socket, drawer, onClose, returnFocusRef }: GameTableProps & {
  drawer: UtilityDrawer;
  onClose: () => void;
  returnFocusRef: RefObject<HTMLButtonElement | null>;
}) {
  const messages = useChatStore((state) => state.messages);
  const chatInput = useAppStore((state) => state.chatInput);
  const setChatInput = useAppStore((state) => state.setChatInput);
  const counter = useAppStore((state) => state.cardCounter);
  const actions = useAppStore((state) => state.recentActions);
  const chatPending = useAppStore((state) => Boolean(state.pendingCommands.chat));
  const open = drawer !== 'none';
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const closeAndRestoreFocus = useCallback(() => {
    onClose();
    const trigger = returnFocusRef.current;
    if (trigger?.isConnected) trigger.focus();
  }, [onClose, returnFocusRef]);

  useEffect(() => {
    if (!open) return;

    function handleEscape(event: KeyboardEvent) {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      closeAndRestoreFocus();
    }

    document.addEventListener('keydown', handleEscape);
    return () => document.removeEventListener('keydown', handleEscape);
  }, [closeAndRestoreFocus, open]);

  useEffect(() => {
    if (open) closeButtonRef.current?.focus();
  }, [drawer, open]);

  function sendChat() {
    const content = chatInput.trim();
    if (!content) return;
    const result = dispatchCommand(socket, createChatCommand(content, 'room'));
    if (result.ok) setChatInput('');
  }

  return (
    <aside
      id={UTILITY_DRAWER_ID}
      className={`utility-drawer ${open ? 'is-open' : ''}`}
      role="dialog"
      aria-modal="false"
      aria-labelledby={UTILITY_DRAWER_TITLE_ID}
      aria-hidden={!open}
      inert={!open}
    >
      <header>
        <strong id={UTILITY_DRAWER_TITLE_ID}>{drawerTitle(drawer)}</strong>
        <button ref={closeButtonRef} onClick={closeAndRestoreFocus} aria-label="关闭"><Icon name="close" /></button>
      </header>
      {drawer === 'chat' ? (
        <>
          <div className="chat-feed">
            {messages.filter((message) => message.scope === 'room').slice(-24).map((message, index) => (
              <p key={index}><strong>{message.sender_name || '玩家'}:</strong> {message.content}</p>
            ))}
          </div>
          <div className="chat-input-row">
            <input
              aria-label="房间聊天消息"
              value={chatInput}
              maxLength={240}
              onChange={(event) => setChatInput(event.target.value)}
              placeholder="房间聊天"
              onKeyDown={(event) => { if (event.key === 'Enter' && !event.nativeEvent.isComposing) sendChat(); }}
            />
            <button disabled={chatPending} onClick={sendChat}>{chatPending ? '发送中...' : '发送'}</button>
          </div>
        </>
      ) : null}
      {drawer === 'counter' ? <CounterPanel counter={counter} /> : null}
      {drawer === 'history' ? <HistoryPanel actions={actions} /> : null}
      {drawer === 'rules' ? <RulesPanel /> : null}
    </aside>
  );
}

function CounterPanel({ counter }: { counter: Record<number, number> }) {
  return (
    <div className="counter-grid">
      {Object.entries(counter).map(([rank, count]) => (
        <div key={rank}>
          <span>{rankName(Number(rank))}</span>
          <strong>{count}</strong>
        </div>
      ))}
    </div>
  );
}

function HistoryPanel({ actions }: { actions: TableAction[] }) {
  return (
    <div className="history-list">
      {actions.length ? [...actions].reverse().map((action, index) => (
        <div key={index}>
          <span>{action.player_name || '系统'}</span>
          <strong>{action.label || (action.type === 'pass' ? '不出' : action.hand_type || '出牌')}</strong>
        </div>
      )) : <p className="empty-text">暂无动作</p>}
    </div>
  );
}

function RulesPanel() {
  return (
    <div className="rules-panel">
      <p>新一轮开始必须出牌；跟牌时需要大过上一手。</p>
      <p>炸弹可以压大多数牌型，王炸最大。</p>
    </div>
  );
}

function arrangeSeats(players: PlayerInfo[], playerId: string): { me?: PlayerInfo; left?: PlayerInfo; right?: PlayerInfo } {
  const me = players.find((player) => player.id === playerId);
  if (!me) return { left: players[1], right: players[2], me: players[0] };
  const left = players.find((player) => player.seat === (me.seat + 1) % 3);
  const right = players.find((player) => player.seat === (me.seat + 2) % 3);
  return { me, left, right };
}

function drawerTitle(drawer: UtilityDrawer): string {
  return ({ chat: '房间聊天', counter: '记牌器', history: '动作历史', rules: '玩法说明', none: '' } satisfies Record<UtilityDrawer, string>)[drawer];
}

function rankName(rank: number): string {
  if (rank === 16) return '小王';
  if (rank === 17) return '大王';
  if (rank === 15) return '2';
  if (rank === 14) return 'A';
  if (rank === 13) return 'K';
  if (rank === 12) return 'Q';
  if (rank === 11) return 'J';
  return String(rank);
}
