import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { UtilityDrawer, TableAction } from '../../stores/types';
import { useAppStore } from '../../stores/appStore';
import { Card } from '../../shared/cards/Card';
import { sortPlayedCards } from '../../shared/cards/cardModel';
import { Icon } from '../../shared/ui/Icon';
import { RulesPanel } from '../../shared/ui/RulesPanel';

interface UtilityDrawerPanelProps {
  drawer: UtilityDrawer;
  onClose: () => void;
}

interface CounterPanelProps {
  counter: Record<number, number>;
}

interface HistoryPanelProps {
  actions: TableAction[];
}

export function UtilityDrawerPanel({ drawer, onClose }: UtilityDrawerPanelProps) {
  const counter = useAppStore((state) => state.cardCounter);
  const actions = useAppStore((state) => state.recentActions);
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
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [open]);

  if (activeDrawer === 'none') return null;

  const supportsPopover =
    typeof HTMLElement !== 'undefined' && typeof HTMLElement.prototype.showPopover === 'function';

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
        <button ref={closeBtnRef} type="button" onClick={onClose} aria-label="关闭">
          <Icon name="close" />
        </button>
      </header>
      {activeDrawer === 'counter' ? <CounterPanel counter={counter} /> : null}
      {activeDrawer === 'history' ? <HistoryPanel actions={actions} /> : null}
      {activeDrawer === 'rules' ? <RulesPanel /> : null}
    </aside>
  );
}

function CounterPanel({ counter }: CounterPanelProps) {
  if (!Object.keys(counter).length) {
    return <p role="status">重连后缺少完整出牌记录，本局记牌器不可用。下一局将自动恢复。</p>;
  }
  return (
    <div>
      <p className="counter-note">
        数字为你手牌以外尚未打出的张数；包含对手手牌及尚未归属的底牌，不含你的手牌。
      </p>
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

function HistoryPanel({ actions }: HistoryPanelProps) {
  return (
    <div className="history-list">
      {actions.length ? (
        [...actions].reverse().map((action, index) => (
          <div key={index}>
            <span>{action.player_name || '系统'}</span>
            <strong>{action.label || (action.type === 'pass' ? '不出' : action.hand_type || '出牌')}</strong>
            {action.cards?.length ? (
              <div className="history-cards">
                {sortPlayedCards(action.cards).map((card, i) => (
                  <Card key={`${card.suit}_${card.rank}_${i}`} card={card} size="mini" />
                ))}
              </div>
            ) : null}
          </div>
        ))
      ) : (
        <p className="empty-text">暂无动作</p>
      )}
    </div>
  );
}

function drawerTitle(drawer: UtilityDrawer): string {
  return (
    { counter: '记牌器', history: '动作历史', rules: '玩法说明', none: '' } satisfies Record<
      UtilityDrawer,
      string
    >
  )[drawer];
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
