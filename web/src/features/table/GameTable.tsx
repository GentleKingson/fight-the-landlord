import { useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { RulesPanel } from '../../shared/ui/RulesPanel';
import { ChatFeed } from '../../shared/ui/ChatFeed';
import { DealAnimation } from './DealAnimation';
import { useDealPresentation } from './useDealPresentation';
import { assignBotAvatars } from './botAvatars';
import { PlayerAvatar } from './PlayerAvatar';
import { MsgType } from '../../protocol/types';
import type { GameSocket } from '../../transport/wsClient';
import { useAppStore, type AppState, type SeatAction, type TableAction } from '../../stores/appStore';
import { cardKey, generateSimpleSuggestions, sortPlayedCards, summarizeHand } from '../../shared/cards/cardModel';
import { canBeat, handSignature, parseHand } from '../../shared/cards/rules';
import { Card } from '../../shared/cards/Card';
import { Hand } from '../../shared/cards/Hand';
import { PlayedCards } from '../../shared/cards/PlayedCards';
import { Icon } from '../../shared/ui/Icon';
import { useImeSafeEnter } from '../../shared/ui/imeSafeSubmit';
import { SoundToggle } from '../../audio/SoundToggle';
import type { CardInfo, PlayerInfo, UtilityDrawer } from '../../protocol/types';

interface GameTableProps {
  socket: GameSocket;
}

const landscape = window.matchMedia?.('(orientation: landscape)');
function subscribeOrientation(update: () => void) {
  landscape?.addEventListener('change', update);
  return () => landscape?.removeEventListener('change', update);
}
const isLandscape = () => landscape?.matches ?? false;

function selectTargetPlayerId(state: AppState): string {
  return state.phase === 'playing' && !state.mustPlay && state.lastPlayed.length > 0
    ? state.lastPlayedBy : '';
}

function hasSeatPlay(player: PlayerInfo | undefined, seatActions: Record<string, SeatAction>): boolean {
  if (!player) return false;
  const action = seatActions[player.id];
  return Boolean(action?.type === 'play' && action.cards?.length);
}

function seatActionLabel(action?: SeatAction): string {
  return action?.type === 'bid' ? action.label || '' : action?.type === 'pass' ? '不出' : '';
}

export function GameTable({ socket }: GameTableProps) {
  const playerId = useAppStore((state) => state.playerId);
  const players = useAppStore((state) => state.players);
  const phase = useAppStore((state) => state.phase);
  const currentTurn = useAppStore((state) => state.currentTurn);
  const hand = useAppStore((state) => state.hand);
  const deal = useAppStore((state) => state.deal);
  const presentation = useDealPresentation(phase === 'dealing' ? deal : null, hand.length);
  const selectedCards = useAppStore((state) => state.selectedCards);
  const bottomCards = useAppStore((state) => state.bottomCards);
  const bottomCardsRevealed = useAppStore((state) => state.bottomCardsRevealed);
  const seatActions = useAppStore((state) => state.seatActions);
  const targetPlayerId = useAppStore(selectTargetPlayerId);
  const lastPlayedBy = useAppStore((state) => state.lastPlayedBy);
  const multiplier = useAppStore((state) => state.multiplier);
  const roomCode = useAppStore((state) => state.roomCode);
  const connected = useAppStore((state) => state.connected);
  const latency = useAppStore((state) => state.latency);
  const drawer = useAppStore((state) => state.drawer);
  const setDrawer = useAppStore((state) => state.setDrawer);
  const toggleCard = useAppStore((state) => state.toggleCard);
  const setSelection = useAppStore((state) => state.setSelection);
  const isMyTurn = (phase === 'bidding' || phase === 'playing') && currentTurn === playerId;
  const landscapeView = useSyncExternalStore(subscribeOrientation, isLandscape);
  const placeTurnAtSeat = landscapeView && (phase === 'bidding' || phase === 'playing');
  const seats = useMemo(() => arrangeSeats(players, playerId), [players, playerId]);
  const botAvatars = useMemo(() => assignBotAvatars(players), [players]);
  const showSeatPlays = phase === 'playing' || phase === 'game_over';
  const leftHasPlay = showSeatPlays && hasSeatPlay(seats.left, seatActions);
  const rightHasPlay = showSeatPlays && hasSeatPlay(seats.right, seatActions);
  const meHasPlay = showSeatPlays && hasSeatPlay(seats.me, seatActions);
  const hasOpponentAction = Boolean(
    (seats.left && seatActionLabel(seatActions[seats.left.id])) ||
    (seats.right && seatActionLabel(seatActions[seats.right.id]))
  );
  const focusPlayerId = useMemo(() => {
    if (!showSeatPlays) return '';
    if (targetPlayerId && seatActions[targetPlayerId]?.type === 'play' && seatActions[targetPlayerId]?.cards?.length) {
      return targetPlayerId;
    }
    if (lastPlayedBy && seatActions[lastPlayedBy]?.type === 'play' && seatActions[lastPlayedBy]?.cards?.length) {
      return lastPlayedBy;
    }
    for (const candidate of [seats.left, seats.right, seats.me]) {
      if (hasSeatPlay(candidate, seatActions)) return candidate!.id;
    }
    return '';
  }, [showSeatPlays, targetPlayerId, lastPlayedBy, seatActions, seats.left, seats.right, seats.me]);

  const feltTableClassName = [
    'felt-table',
    showSeatPlays ? 'felt-table--seats' : '',
    leftHasPlay && !rightHasPlay ? 'felt-table--only-left' : '',
    rightHasPlay && !leftHasPlay ? 'felt-table--only-right' : '',
    meHasPlay ? 'felt-table--has-me' : '',
    focusPlayerId ? 'felt-table--has-focus' : '',
  ].filter(Boolean).join(' ');

  return (
    <main className="table-screen">
      <header className="table-topbar" aria-label="牌桌状态">
        <div className="table-brand">
          <span className="table-brand__logo">斗地主</span>
          <span>房间号 {roomCode || '练习桌'}</span>
        </div>
        <div className="table-score-strip">
          <span>底分 3</span>
          <strong>倍数 x{multiplier || 1}</strong>
        </div>
        <div className={`table-network ${connected ? '' : 'is-offline'}`} role="status">
          <span className="network-dot" />
          <span>{!connected ? '连接中断' : latency ? `${latency}ms` : '已连接'}</span>
        </div>
        <div className="table-tools">
          <SoundToggle />
          <ToolButton drawer="counter" label="记牌器" icon="counter" />
          <ToolButton drawer="chat" label="聊天" icon="chat" />
          <ToolButton drawer="history" label="历史" icon="history" />
          <ToolButton drawer="rules" label="规则" icon="rules" />
        </div>
      </header>

      <section className={`table-arena${hasOpponentAction ? ' table-arena--has-opp-action' : ''}`} aria-label="斗地主牌桌">
        <div className="opponent-row">
          <SeatPanel player={seats.left} avatarSrc={seats.left ? botAvatars.get(seats.left.id) : undefined} side="left" showTurn={placeTurnAtSeat} active={currentTurn === seats.left?.id} action={seats.left ? seatActions[seats.left.id] : undefined} />
          <BottomCards cards={bottomCards} revealed={bottomCardsRevealed} />
          <SeatPanel player={seats.right} avatarSrc={seats.right ? botAvatars.get(seats.right.id) : undefined} side="right" showTurn={placeTurnAtSeat} active={currentTurn === seats.right?.id} action={seats.right ? seatActions[seats.right.id] : undefined} />
        </div>

        <div className={feltTableClassName}>
          {phase === 'dealing' ? (
            <div className="deal-origin">
              <span className="deal-animation__label">{presentation.finished ? '等待叫地主通知…' : '正在发牌…'}</span>
            </div>
          ) : phase === 'bidding' ? (
            <div className="table-center-hint">等待叫抢地主</div>
          ) : (
            <>
              <SeatPlayedCards player={seats.left} action={seats.left ? seatActions[seats.left.id] : undefined} side="left" focusPlayerId={focusPlayerId} />
              <SeatPlayedCards player={seats.me} action={seats.me ? seatActions[seats.me.id] : undefined} side="me" focusPlayerId={focusPlayerId} />
              <SeatPlayedCards player={seats.right} action={seats.right ? seatActions[seats.right.id] : undefined} side="right" focusPlayerId={focusPlayerId} />
            </>
          )}
        </div>
        {!placeTurnAtSeat && <TurnBanner />}
      </section>

      <div className={placeTurnAtSeat ? 'table-controls table-controls--with-turn' : 'table-controls'}>
        {placeTurnAtSeat && isMyTurn && <TurnBanner />}
        <ActionBar socket={socket} isMyTurn={isMyTurn} phase={phase} />
      </div>

      <section className="hand-zone">
        <div className="hand-heading">
          <strong>{seats.me?.name || '我'} · {phase === 'dealing' || phase === 'bidding' ? '等待定地主' : seats.me?.is_landlord ? '地主' : '农民'}</strong>
          <span className={phase === 'playing' && hand.length <= 2 ? 'cards-warning' : undefined}>{phase === 'playing' && hand.length <= 2 ? '⚠ ' : ''}手牌 {presentation.visibleCount} 张</span>
        </div>
        <Hand
          cards={hand}
          visibleCount={presentation.visibleCount}
          selected={selectedCards}
          disabled={phase !== 'playing'}
          onToggle={toggleCard}
          onRangeSelect={setSelection}
        />
      </section>

      {phase === 'dealing' && <DealAnimation deal={deal} count={hand.length} {...presentation} />}
      <UtilityDrawer key={roomCode} socket={socket} drawer={drawer} onClose={() => setDrawer('none')} />
    </main>
  );
}

function SeatPlayedCards({
  player,
  action,
  side,
  focusPlayerId,
}: {
  player?: PlayerInfo;
  action?: SeatAction;
  side: 'left' | 'me' | 'right';
  focusPlayerId: string;
}) {
  const targetPlayerId = useAppStore(selectTargetPlayerId);
  const cards = action?.type === 'play' ? action.cards : undefined;
  const isTarget = Boolean(cards?.length && targetPlayerId && player?.id === targetPlayerId);
  const isFocus = Boolean(cards?.length && focusPlayerId && player?.id === focusPlayerId);
  return (
    <section
      className={`played-seat played-seat--${side}${isTarget ? ' played-seat--target' : ''}${isFocus ? ' played-seat--focus' : ''}`}
      aria-label={player && cards?.length ? `${player.name}的出牌` : undefined}
    >
      {cards?.length ? <PlayedCards cards={cards} handType={action?.hand_type} playerName={player?.name} isTarget={isTarget} /> : null}
    </section>
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

function ToolButton({ drawer, label, icon }: { drawer: UtilityDrawer; label: string; icon: 'chat' | 'counter' | 'history' | 'rules' }) {
  const setDrawer = useAppStore((state) => state.setDrawer);
  const open = useAppStore((state) => state.drawer === drawer);
  return (
    <button
      type="button"
      className="tool-button"
      data-drawer={drawer}
      onClick={() => setDrawer(open ? 'none' : drawer)}
      title={label}
      aria-label={label}
      aria-expanded={open}
      aria-controls={open ? 'table-utility-drawer' : undefined}
    >
      <Icon name={icon} />
      <span>{label}</span>
    </button>
  );
}

function SeatPanel({ player, avatarSrc, side, active, action, showTurn }: { player?: PlayerInfo; avatarSrc?: string; side: 'left' | 'right'; active: boolean; showTurn: boolean; action?: SeatAction }) {
  if (!player) {
    return <aside className={`seat-panel seat-panel--${side}`} aria-hidden="true" />;
  }
  return (
    <aside className={`seat-panel seat-panel--${side} ${active ? 'is-active' : ''}`}>
      <PlayerBadge player={player} avatarSrc={avatarSrc} active={active} action={action} />
      {showTurn && active && <TurnBanner />}
    </aside>
  );
}

function PlayerBadge({ player, avatarSrc, active, action }: { player: PlayerInfo; avatarSrc?: string; active: boolean; action?: SeatAction }) {
  const phase = useAppStore(state => state.phase);
  const pendingRole = phase === 'dealing' || phase === 'bidding';
  const low = phase === 'playing' && player.cards_count > 0 && player.cards_count <= 2;
  const actionLabel = seatActionLabel(action);
  return (
    <div className={`player-badge ${active ? 'is-active' : ''} ${player.is_landlord ? 'is-landlord' : ''}`}>
      <PlayerAvatar name={player.name} src={avatarSrc} />
      <div className="player-info">
        <div className="player-identity"><strong title={player.name}>{player.name}</strong>
        <span>{pendingRole ? '身份待定' : player.is_landlord ? '地主' : '农民'}{active ? ' · 行动中' : ''}</span></div>
        <span className={low ? 'cards-warning' : 'cards-remaining'} role={low ? 'status' : undefined}>{low ? '⚠ ' : ''}剩余 <b>{player.cards_count || 0}</b> 张</span>
      </div>
      {actionLabel ? <em className="player-action" title={actionLabel}>{actionLabel}</em> : null}
      {!player.online ? <em className="player-offline">离线</em> : null}
    </div>
  );
}

function TurnBanner() {
  const playerId = useAppStore((state) => state.playerId);
  const players = useAppStore((state) => state.players);
  const currentTurn = useAppStore((state) => state.currentTurn);
  const timeout = useAppStore((state) => state.timeout);
  const timerStart = useAppStore((state) => state.timerStart);
  const phase = useAppStore((state) => state.phase);
  const isGrabTurn = useAppStore((state) => state.isGrabTurn);
  const recentActions = useAppStore((state) => state.recentActions);
  const winnerName = useAppStore((state) => state.winnerName);
  const targetPlayerId = useAppStore(selectTargetPlayerId);
  const lastPlayedName = useAppStore((state) => state.lastPlayedName);
  const mustPlay = useAppStore((state) => state.mustPlay);
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    if (phase === 'game_over') return;
    const id = window.setInterval(() => setNow(Date.now()), 400);
    return () => window.clearInterval(id);
  }, [phase]);

  if (phase === 'game_over') {
    return <div className="turn-banner" role="status"><div className="turn-banner__copy"><strong>{winnerName} 获胜</strong><span>本局已结束，即将显示结算</span></div></div>;
  }

  const remaining = timeout ? Math.max(0, Math.ceil(timeout - Math.max(0, now - timerStart) / 1000)) : 0;
  const actor = players.find((player) => player.id === currentTurn);
  const isMe = currentTurn === playerId;
  const lastBid = [...recentActions].reverse().find((action) => action.type === 'bid');
  const title = isMe ? '轮到你' : `等待 ${actor?.name || '玩家'}`;
  const subtitle = phase === 'bidding'
    ? (lastBid?.label ? `${lastBid.player_name || '玩家'}：${lastBid.label}` : isGrabTurn ? '抢地主阶段' : '叫地主阶段')
    : mustPlay ? '新一轮，请出牌'
    : targetPlayerId ? `跟牌目标：${players.find(player => player.id === targetPlayerId)?.name || lastPlayedName || '玩家'}`
    : '出牌阶段';

  return (
    <div className={`turn-banner ${isMe ? 'is-mine' : ''} ${isMe && remaining > 0 && remaining <= 5 ? 'is-urgent' : ''}`}>
      <div className="turn-banner__copy" role="status" aria-atomic="true">
        <strong>{phase === 'dealing' ? '正在发牌' : title}</strong>
        <span className={phase === 'playing' ? 'turn-banner__play-hint' : undefined}>{phase === 'dealing' ? '发牌结束后开始叫地主' : subtitle}</span>
      </div>
      {phase !== 'dealing' ? <time>{remaining || 0}</time> : null}
    </div>
  );
}

function ActionBar({ socket, isMyTurn, phase }: GameTableProps & { isMyTurn: boolean; phase: string }) {
  const hintSession = useRef<{ context: string; suggestions: CardInfo[][] } | null>(null);
  const hand = useAppStore((state) => state.hand);
  const selectedCards = useAppStore((state) => state.selectedCards);
  const lastPlayed = useAppStore((state) => state.lastPlayed);
  const mustPlay = useAppStore((state) => state.mustPlay);
  const isGrabTurn = useAppStore((state) => state.isGrabTurn);
  const clearSelection = useAppStore((state) => state.clearSelection);
  const selected = hand.filter((card) => selectedCards.has(cardKey(card)));
  const summary = summarizeHand(selected);
  const parsedHand = parseHand(selected);
  const connected = useAppStore(state => state.connected);
  const submission = useAppStore(state => state.submission);
  const blocked = !connected || submission === 'pending' || submission === 'unknown';
  const target = parseHand(lastPlayed);
  const reason = submission === 'unknown' ? '操作结果待核对，请勿重复提交'
    : submission === 'pending' ? '等待服务器确认，请勿重复提交'
    : !connected ? '网络已断开，请等待连接恢复'
    : !isMyTurn ? '未轮到自己，可提前选牌'
    : !selected.length ? '请选择要出的牌'
    : !parsedHand ? '牌型错误，请重新选牌'
    : !mustPlay && lastPlayed.length > 0 && !canBeat(parsedHand, target)
      ? (target && parsedHand.type === target.type && parsedHand.length === target.length
        ? '点数不足，需要大于当前出牌' : '牌型不匹配，无法压过当前出牌')
      : '';
  const canPlay = phase === 'playing' && !reason;

  function play() {
    if (!canPlay) {
      useAppStore.setState({ tableMessage: reason });
      return;
    }
    socket.send(MsgType.PlayCards, { cards: selected, turn_token: useAppStore.getState().turnToken });
  }

  function pass() {
    if (mustPlay) {
      useAppStore.setState({ tableMessage: '本轮必须出牌' });
      return;
    }
    if (socket.send(MsgType.Pass, { turn_token: useAppStore.getState().turnToken })) clearSelection();
  }

  function hint() {
    const state = useAppStore.getState();
    if (state.phase !== 'playing' || state.currentTurn !== state.playerId) return;
    if (state.submission === 'rejected') useAppStore.setState({ submission: 'idle', submissionMessage: '' });
    const context = JSON.stringify([state.roundId, state.playerId, state.currentTurn, state.turnToken,
      state.hand.map(cardKey).sort(), state.lastPlayedBy, handSignature(state.lastPlayed), state.mustPlay]);
    const isNewContext = hintSession.current?.context !== context;
    if (isNewContext) {
      hintSession.current = { context, suggestions: generateSimpleSuggestions(state.hand, state.lastPlayed, state.mustPlay) };
    }
    const suggestions = hintSession.current!.suggestions;
    if (!suggestions.length) {
      useAppStore.setState({ tableMessage: '暂无提示，请手动选牌' });
      return;
    }
    const selection = handSignature(state.hand.filter(card => state.selectedCards.has(cardKey(card))));
    const current = isNewContext ? -1 : suggestions.findIndex(cards => handSignature(cards) === selection);
    const index = (current + 1) % suggestions.length;
    useAppStore.setState({
      selectedCards: new Set(suggestions[index].map(cardKey)),
      tableMessage: `提示 ${index + 1}/${suggestions.length}：${summarizeHand(suggestions[index])}${suggestions.length > 1 ? ' · 再点切换' : ''}`
    });
  }

  if (phase === 'dealing') return <section className="action-bar action-bar--dealing" aria-label="发牌中">等待发牌完成</section>;
  if (phase === 'game_over') return null;

  if (phase === 'bidding') {
    return (
      <section className="action-bar action-bar--bidding" aria-label="叫地主操作">
        {isMyTurn ? (
          <>
            <button className="primary-action" disabled={blocked} onClick={() => socket.send(MsgType.Bid, { bid: true, bid_token: useAppStore.getState().bidToken })}>{isGrabTurn ? '抢地主' : '叫地主'}</button>
            <button className="secondary-action secondary-action--muted" disabled={blocked} onClick={() => socket.send(MsgType.Bid, { bid: false, bid_token: useAppStore.getState().bidToken })}>{isGrabTurn ? '不抢' : '不叫'}</button>
          </>
        ) : <span>等待其他玩家{isGrabTurn ? '抢地主' : '叫地主'}...</span>}
        <ActionSummary selectedCount={0} summary="" />
      </section>
    );
  }

  return (
    <section className="action-bar" aria-label="出牌操作">
      <button className="secondary-action secondary-action--blue" disabled={blocked || !isMyTurn || mustPlay} onClick={pass}>不出</button>
      <button className="secondary-action secondary-action--green" disabled={!isMyTurn} onClick={hint}>提示</button>
      <button className="secondary-action secondary-action--muted" disabled={!selectedCards.size} onClick={clearSelection}>重选</button>
      <button className="primary-action" disabled={!canPlay} aria-describedby="play-action-summary" onClick={play}>出牌</button>
      <ActionSummary selectedCount={selected.length} summary={summary} reason={reason} blocked={blocked} />
    </section>
  );
}

function ActionSummary({ selectedCount, summary, reason, blocked }: { selectedCount: number; summary: string; reason?: string; blocked?: boolean }) {
  const tableMessage = useAppStore((state) => state.tableMessage);
  const submissionMessage = useAppStore(state => state.submissionMessage);
  const phase = useAppStore(state => state.phase);
  const message = submissionMessage || tableMessage;
  const feedback = blocked ? submissionMessage || reason
    : message && reason && selectedCount > 0 && message !== reason ? `${message} · ${reason}` : message || reason;
  return (
    <small id="play-action-summary" role="status" aria-live="polite" aria-atomic="true" className={reason || tableMessage ? 'is-warning' : ''}>
      {feedback || (phase === 'bidding' ? '请选择叫抢操作' : selectedCount ? `已选 ${selectedCount} 张 · ${summary}` : '选择手牌后出牌')}
    </small>
  );
}

function UtilityDrawer({ socket, drawer, onClose }: GameTableProps & { drawer: UtilityDrawer; onClose: () => void }) {
  const chatInput = useAppStore((state) => state.chatInput);
  const setChatInput = useAppStore((state) => state.setChatInput);
  const counter = useAppStore((state) => state.cardCounter);
  const actions = useAppStore((state) => state.recentActions);
  const imeEnter = useImeSafeEnter();
  const drawerRef = useRef<HTMLElement | null>(null);
  const closeBtnRef = useRef<HTMLButtonElement | null>(null);
  const triggerRef = useRef<HTMLElement | null>(null);
  const wasOpenRef = useRef(false);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  const open = drawer !== 'none';
  const [renderedDrawer, setRenderedDrawer] = useState<UtilityDrawer>(drawer);
  if (open && renderedDrawer !== drawer) {
    setRenderedDrawer(drawer);
  }
  const activeDrawer = open ? drawer : renderedDrawer;

  useLayoutEffect(() => {
    const el = drawerRef.current;
    if (open) {
      const explicitTrigger = document.querySelector<HTMLElement>(`[data-drawer="${drawer}"]`);
      const activeEl = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      if (explicitTrigger) {
        triggerRef.current = explicitTrigger;
      } else if (activeEl && activeEl !== document.body && !el?.contains(activeEl)) {
        triggerRef.current = activeEl;
      }

      if (el && typeof el.showPopover === 'function') {
        try {
          el.showPopover();
        } catch {
          // Already promoted to top layer.
        }
      }
      wasOpenRef.current = true;
      closeBtnRef.current?.focus({ preventScroll: true });
      return;
    }

    if (!wasOpenRef.current) return;
    wasOpenRef.current = false;

    if (el && typeof el.hidePopover === 'function') {
      try {
        el.hidePopover();
      } catch {
        // Already hidden.
      }
    }

    const trigger = triggerRef.current;
    triggerRef.current = null;
    if (trigger && trigger.isConnected) {
      trigger.focus({ preventScroll: true });
    }

    const animations = el && typeof el.getAnimations === 'function' ? el.getAnimations() : [];
    if (!animations.length) {
      setRenderedDrawer('none');
      return;
    }

    let cancelled = false;
    const finish = () => {
      if (!cancelled) setRenderedDrawer('none');
    };
    const timer = window.setTimeout(finish, 260);
    void Promise.allSettled(animations.map((animation) => animation.finished)).then(finish);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [drawer, open]);

  useEffect(() => {
    if (!open) return;
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape' && !event.defaultPrevented) {
        event.preventDefault();
        onCloseRef.current();
      }
    }
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [open]);

  if (activeDrawer === 'none') return null;

  const supportsPopover = typeof HTMLElement !== 'undefined' && typeof HTMLElement.prototype.showPopover === 'function';

  function sendChat() {
    const content = chatInput.trim();
    if (!content) return;
    if (socket.send(MsgType.Chat, { content, scope: 'room' })) setChatInput('');
    else useAppStore.getState().setError('聊天发送失败，草稿已保留，请等待连接恢复后重试');
  }

  return (
    <aside
      ref={drawerRef}
      id="table-utility-drawer"
      popover="manual"
      className={`utility-drawer ${!supportsPopover && open ? 'is-open' : ''}`.trim()}
      aria-label={drawerTitle(activeDrawer)}
    >
      <header>
        <strong>{drawerTitle(activeDrawer)}</strong>
        <button ref={closeBtnRef} type="button" onClick={onClose} aria-label="关闭"><Icon name="close" /></button>
      </header>
      {activeDrawer === 'chat' ? (
        <>
          <ChatFeed scope="room" />
          <form className="chat-input-row" onSubmit={(event) => { event.preventDefault(); sendChat(); }}>
            <label className="visually-hidden" htmlFor="room-chat-input">房间聊天消息</label>
            <input
              id="room-chat-input"
              name="message"
              enterKeyHint="send"
              value={chatInput}
              onChange={(event) => setChatInput(event.target.value)}
              {...imeEnter}
              placeholder="房间聊天"
            />
            <button type="submit">发送</button>
          </form>
        </>
      ) : null}
      {activeDrawer === 'counter' ? <CounterPanel counter={counter} /> : null}
      {activeDrawer === 'history' ? <HistoryPanel actions={actions} /> : null}
      {activeDrawer === 'rules' ? <RulesPanel /> : null}
    </aside>
  );
}

function CounterPanel({ counter }: { counter: Record<number, number> }) {
  if (!Object.keys(counter).length) return <p role="status">重连后缺少完整出牌记录，本局记牌器不可用。下一局将自动恢复。</p>;
  return (
    <div>
      <p className="counter-note">数字为你手牌以外尚未打出的张数；包含对手手牌及尚未归属的底牌，不含你的手牌。</p>
    <div className="counter-grid">
      {Object.entries(counter).map(([rank, count]) => (
        <div key={rank} className={count === 0 ? 'is-zero' : undefined}>
          <span>{rankName(Number(rank))}</span>
          <strong>{count}</strong>
        </div>
      ))}
    </div>
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
          {action.cards?.length ? <div className="history-cards">{sortPlayedCards(action.cards).map((card, i) => <Card key={`${card.suit}_${card.rank}_${i}`} card={card} size="mini" />)}</div> : null}
        </div>
      )) : <p className="empty-text">暂无动作</p>}
    </div>
  );
}

function arrangeSeats(players: PlayerInfo[], playerId: string): { me?: PlayerInfo; left?: PlayerInfo; right?: PlayerInfo } {
  const me = players.find((player) => player.id === playerId);
  if (!me) return { left: players[2], right: players[1], me: players[0] };
  // Turns advance by seat + 1: the previous player sits left, the next sits right.
  const left = players.find((player) => player.seat === (me.seat + 2) % 3);
  const right = players.find((player) => player.seat === (me.seat + 1) % 3);
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
