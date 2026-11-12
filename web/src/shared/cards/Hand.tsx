import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent } from 'react';
import type { CardInfo } from '../../protocol/types';
import { cardKey } from './cardModel';
import { Card } from './Card';

interface HandProps {
  cards: CardInfo[];
  visibleCount?: number;
  selected: Set<string>;
  disabled?: boolean;
  onToggle: (key: string) => void;
  onRangeSelect: (keys: string[]) => void;
}

export interface HandCardLayout {
  card: CardInfo;
  key: string;
  index: number;
  groupIndex: number;
  groupSize: number;
  groupOffset: number;
  groupPosition: number;
  row: number;
  rowIndex: number;
  rowCount: number;
  singleX: number;
  compactX: number;
  rowX: number;
}

interface HandGroup {
  cards: CardInfo[];
  startIndex: number;
}

export type SpatialDirection = 'left' | 'right' | 'up' | 'down';

export interface SpatialBox {
  left: number;
  top: number;
  right: number;
  bottom: number;
  width: number;
  height: number;
}

const ARROW_TO_DIRECTION: Record<string, SpatialDirection> = {
  ArrowLeft: 'left',
  ArrowRight: 'right',
  ArrowUp: 'up',
  ArrowDown: 'down'
};

export function Hand({ cards, visibleCount = cards.length, selected, disabled = false, onToggle, onRangeSelect }: HandProps) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const lastPointerDownIndex = useRef<number | null>(null);
  const sameCardPointerDown = useRef(false);
  const selectionAtStart = useRef<Set<string>>(new Set());
  const dragRects = useRef<{ index: number; rect: DOMRect }[]>([]);
  const lastHitIndexRef = useRef<number | null>(null);
  const [dragStart, setDragStart] = useState<number | null>(null);
  const [dragMoved, setDragMoved] = useState(false);
  const [dragKeys, setDragKeys] = useState<Set<string>>(new Set());
  const [activeCardId, setActiveCardId] = useState<string | null>(null);
  const [fallbackIndex, setFallbackIndex] = useState(0);
  const [width, setWidth] = useState(280);
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const measure = () => setWidth(root.clientWidth);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(root);
    return () => observer.disconnect();
  }, []);
  const layout = useMemo(() => buildHandLayout(cards, width), [cards, width]);
  const keys = useMemo(() => layout.map((item) => item.key), [layout]);
  const exposedCount = Math.max(0, Math.min(layout.length, visibleCount));

  const resolvedActiveIndex = useMemo(() => {
    if (exposedCount === 0) return -1;
    if (activeCardId !== null) {
      const matched = layout.findIndex((item) => item.index < exposedCount && item.key === activeCardId);
      if (matched >= 0) return matched;
    }
    return Math.min(fallbackIndex, exposedCount - 1);
  }, [activeCardId, exposedCount, fallbackIndex, layout]);

  function setRovingFocus(index: number) {
    const item = layout[index];
    if (!item) return;
    setActiveCardId(item.key);
    setFallbackIndex(index);
  }

  function indexFromPoint(clientX: number, clientY: number): number {
    if (dragStart !== null) {
      return dragRects.current.find(({ rect }) => clientX >= rect.left && clientX <= rect.right && clientY >= rect.top && clientY <= rect.bottom)?.index ?? -1;
    }
    const target = document.elementFromPoint(clientX, clientY);
    return indexFromTarget(target);
  }

  function indexFromTarget(target: EventTarget | Element | null): number {
    const element = (target as HTMLElement | null)?.closest?.('[data-card-index]');
    return element ? Number((element as HTMLElement).dataset.cardIndex) : -1;
  }

  function applyRange(endIndex: number) {
    if (dragStart === null || endIndex < 0) return;
    // A click may contain pointer jitter; only crossing cards starts a range.
    if (!dragMoved && endIndex === dragStart) return;
    lastHitIndexRef.current = endIndex;
    if (endIndex !== dragStart) setDragMoved(true);
    const min = Math.min(dragStart, endIndex);
    const max = Math.max(dragStart, endIndex);
    const rangedKeys = keys.slice(min, max + 1);
    setDragKeys(new Set(rangedKeys));
    onRangeSelect(Array.from(new Set([...selectionAtStart.current, ...rangedKeys])));
  }

  function selectGroup(groupIndex: number) {
    const groupKeys = layout.filter((item) => item.groupIndex === groupIndex).map((item) => item.key);
    if (!groupKeys.length) return;
    const allSelected = groupKeys.every((key) => selected.has(key));
    const next = new Set(selected);
    for (const key of groupKeys) {
      if (allSelected) next.delete(key);
      else next.add(key);
    }
    onRangeSelect([...next]);
  }

  function handleSpatialKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    const direction = ARROW_TO_DIRECTION[event.key];
    if (!direction || disabled) return;
    if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;

    const rawTarget = event.nativeEvent.composedPath?.()[0] ?? event.target;
    if (rawTarget instanceof Element && rawTarget.matches(':read-write, select, input[type="range"], [role="slider"]')) {
      return;
    }

    const root = rootRef.current;
    if (!root) return;

    const optionElements = Array.from(root.querySelectorAll<HTMLElement>('[role="option"]')).filter((el) => {
      if (el.getAttribute('aria-disabled') === 'true') return false;
      if (el.closest('[inert], [aria-hidden="true"]')) return false;
      if (typeof el.checkVisibility === 'function' && !el.checkVisibility()) return false;
      return true;
    });

    const currentEl = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!currentEl || !optionElements.includes(currentEl)) return;

    const boxForElement = (el: HTMLElement): SpatialBox => {
      const rect = el.getBoundingClientRect();
      if (rect.width > 0 && rect.height > 0) {
        return {
          left: rect.left,
          top: rect.top,
          right: rect.right,
          bottom: rect.bottom,
          width: rect.width,
          height: rect.height
        };
      }
      // Fallback for non-layout test environments (e.g. jsdom where getBoundingClientRect is 0x0).
      const slot = el.closest<HTMLElement>('[data-card-index]');
      const cardIdx = slot ? Number(slot.dataset.cardIndex) : 0;
      const item = layout[cardIdx];
      const left = width > 80 && item ? item.singleX + 500 : cardIdx * 32;
      const top = selected.has(item?.key ?? '') ? 0 : 22;
      return { left, top, right: left + 72, bottom: top + 104, width: 72, height: 104 };
    };

    const currentBox = boxForElement(currentEl);
    const candidates = optionElements
      .filter((el) => el !== currentEl)
      .map((el) => ({ element: el, box: boxForElement(el) }));

    const next = pickSpatialCandidate(currentBox, candidates, direction);
    if (!next) {
      // Preserve native boundary scrolling when no focusable target exists in the pressed direction.
      return;
    }

    event.preventDefault();
    const nextSlot = next.closest<HTMLElement>('[data-card-index]');
    const nextIndex = nextSlot ? Number(nextSlot.dataset.cardIndex) : -1;
    if (nextIndex >= 0) setRovingFocus(nextIndex);
    next.focus();
    next.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  }

  return (
    <div
      ref={rootRef}
      className={`hand ${cards.length <= 6 ? 'hand--short' : ''} ${disabled ? 'is-disabled' : ''} ${dragStart !== null ? 'is-dragging' : ''}`}
      style={{ '--hand-count': cards.length } as CSSProperties}
      role={exposedCount > 0 ? 'listbox' : 'group'}
      aria-label="手牌"
      aria-multiselectable={exposedCount > 0 ? true : undefined}
      aria-disabled={disabled || undefined}
      onKeyDown={handleSpatialKeyDown}
      onPointerDown={(event) => {
        if (disabled || !event.isPrimary || event.button !== 0) return;
        const index = indexFromTarget(event.target);
        if (index >= 0 && index < exposedCount) {
          setRovingFocus(index);
          sameCardPointerDown.current = lastPointerDownIndex.current === index;
          lastPointerDownIndex.current = index;
          selectionAtStart.current = new Set(selected);
          dragRects.current = Array.from(rootRef.current!.querySelectorAll<HTMLElement>('[data-card-index]')).map(slot => ({ index: Number(slot.dataset.cardIndex), rect: slot.getBoundingClientRect() })).reverse();
          setDragStart(index);
          setDragMoved(false);
          lastHitIndexRef.current = index;
          setDragKeys(new Set([keys[index]]));
          rootRef.current?.setPointerCapture(event.pointerId);
        }
      }}
      onPointerMove={(event) => {
        if (disabled || !event.isPrimary || dragStart === null) return;
        event.preventDefault();
        const index = indexFromPoint(event.clientX, event.clientY);
        applyRange(index);
      }}
      onPointerUp={(event) => {
        if (disabled || !event.isPrimary) return;
        const index = indexFromPoint(event.clientX, event.clientY);
        const hitIndex = index >= 0 ? index : lastHitIndexRef.current;
        if (dragStart !== null && !dragMoved && hitIndex === dragStart) onToggle(keys[dragStart]);
        setDragStart(null);
        setDragMoved(false);
        lastHitIndexRef.current = null;
        setDragKeys(new Set());
        try {
          rootRef.current?.releasePointerCapture(event.pointerId);
        } catch {
          // Pointer capture may already be released by the browser.
        }
      }}
      onPointerCancel={(event) => {
        if (!event.isPrimary) return;
        if (dragStart !== null) onRangeSelect([...selectionAtStart.current]);
        setDragStart(null);
        setDragMoved(false);
        lastHitIndexRef.current = null;
        setDragKeys(new Set());
      }}
    >
      {layout.map((item) => {
        const pending = item.index >= visibleCount;
        const isOptionDisabled = disabled || pending;
        const isSelected = selected.has(item.key) || dragKeys.has(item.key);
        const tabIndex = isOptionDisabled ? -1 : item.index === resolvedActiveIndex ? 0 : -1;
        return (
          <div
            key={`${item.key}_${item.index}`}
            role="none"
            className={`hand__slot ${pending ? 'is-pending' : ''} ${isSelected ? 'is-selected' : ''}`}
            data-card-index={item.index}
            data-group-index={item.groupIndex}
            data-row={item.row}
            style={{
              '--i': item.index,
              '--group': item.groupIndex,
              '--group-offset': item.groupOffset,
              '--group-position': item.groupPosition,
              '--group-size': item.groupSize,
              '--row': item.row,
              '--row-index': item.rowIndex,
              '--row-count': item.rowCount,
              '--single-x': `${item.singleX}px`,
              '--compact-x': `${item.compactX}px`,
              '--row-x': `${item.rowX}px`,
              zIndex: item.index + (item.row === 1 ? 40 : 0)
            } as CSSProperties}
            aria-hidden={pending || undefined}
            inert={pending}
            onDoubleClick={() => {
              if (!disabled && sameCardPointerDown.current) selectGroup(item.groupIndex);
            }}
          >
            <Card
              as="option"
              card={item.card}
              selected={isSelected}
              disabled={isOptionDisabled}
              tabIndex={tabIndex}
              onFocus={() => {
                if (!isOptionDisabled) setRovingFocus(item.index);
              }}
              onClick={(event) => {
                if (!isOptionDisabled && event.detail === 0) onToggle(item.key);
              }}
              onKeyDown={(event) => {
                if (isOptionDisabled || event.altKey || event.ctrlKey || event.metaKey) return;
                if (event.key === 'Enter') {
                  event.preventDefault();
                  onToggle(item.key);
                } else if (event.key === ' ') {
                  // Prevent page scrolling on Spacebar keydown; activation occurs on keyup.
                  event.preventDefault();
                }
              }}
              onKeyUp={(event) => {
                if (isOptionDisabled || event.altKey || event.ctrlKey || event.metaKey) return;
                if (event.key === ' ') {
                  event.preventDefault();
                  onToggle(item.key);
                }
              }}
            />
          </div>
        );
      })}
    </div>
  );
}

export function pickSpatialCandidate<T>(
  current: SpatialBox,
  candidates: Array<{ element: T; box: SpatialBox }>,
  direction: SpatialDirection
): T | null {
  const tolerance = 1;
  const fallbackWeight = 2;
  const currCenterX = (current.left + current.right) / 2;
  const currCenterY = (current.top + current.bottom) / 2;

  interface ScoredCandidate {
    element: T;
    aligned: boolean;
    primaryDistance: number;
    perpOffset: number;
    fallbackScore: number;
  }

  const valid: ScoredCandidate[] = [];

  for (const { element, box: cand } of candidates) {
    const minH = Math.min(current.height, cand.height);
    const overlapX = Math.min(current.right, cand.right) - Math.max(current.left, cand.left);
    const overlapY = Math.min(current.bottom, cand.bottom) - Math.max(current.top, cand.top);
    const sameVisualRow = overlapY > minH * 0.4;
    const candCenterX = (cand.left + cand.right) / 2;
    const candCenterY = (cand.top + cand.bottom) / 2;

    if (direction === 'right' || direction === 'left') {
      const strictEdgePass = direction === 'right'
        ? cand.left >= current.right - tolerance
        : cand.right <= current.left + tolerance;
      const fannedRowPass = sameVisualRow && (direction === 'right'
        ? cand.left > current.left + tolerance && cand.right > current.right + tolerance
        : cand.right < current.right - tolerance && cand.left < current.left - tolerance);

      if (!strictEdgePass && !fannedRowPass) continue;

      const edgeGap = direction === 'right'
        ? Math.max(0, cand.left - current.right)
        : Math.max(0, current.left - cand.right);
      const leadingStep = direction === 'right'
        ? cand.left - current.left
        : current.right - cand.right;
      const perpOffset = Math.abs(candCenterY - currCenterY);

      if (sameVisualRow) {
        valid.push({
          element,
          aligned: true,
          primaryDistance: leadingStep,
          perpOffset,
          fallbackScore: edgeGap + fallbackWeight * perpOffset
        });
      } else if (strictEdgePass) {
        valid.push({
          element,
          aligned: false,
          primaryDistance: edgeGap,
          perpOffset,
          fallbackScore: edgeGap + fallbackWeight * perpOffset
        });
      }
    } else {
      // Exclude neighbors in the same row whose selected transform lifts them slightly.
      if (sameVisualRow) continue;

      const strictEdgePass = direction === 'down'
        ? cand.top >= current.bottom - tolerance
        : cand.bottom <= current.top + tolerance;
      const liftedRowPass = direction === 'down'
        ? cand.top > current.top + tolerance && cand.bottom > current.bottom + tolerance
        : cand.bottom < current.bottom - tolerance && cand.top < current.top - tolerance;

      if (!strictEdgePass && !liftedRowPass) continue;

      const edgeGap = direction === 'down'
        ? Math.max(0, cand.top - current.bottom)
        : Math.max(0, current.top - cand.bottom);
      const perpOffset = Math.abs(candCenterX - currCenterX);

      valid.push({
        element,
        aligned: overlapX > 0,
        primaryDistance: edgeGap,
        perpOffset,
        fallbackScore: edgeGap + fallbackWeight * perpOffset
      });
    }
  }

  if (!valid.length) return null;

  const aligned = valid.filter((item) => item.aligned);
  if (aligned.length > 0) {
    aligned.sort((a, b) => (a.primaryDistance - b.primaryDistance) || (a.perpOffset - b.perpOffset));
    return aligned[0].element;
  }

  valid.sort((a, b) => a.fallbackScore - b.fallbackScore);
  return valid[0].element;
}

export function buildHandLayout(cards: CardInfo[], width = 280): HandCardLayout[] {
  const groups = buildGroups(cards);
  // Keep ranks together, choosing the boundary closest to half the actual cards.
  let splitAt = 0;
  let count = 0;
  let best = Infinity;
  groups.forEach((group, index) => {
    count += group.cards.length;
    const difference = Math.abs(cards.length - 2 * count);
    if (difference < best) { best = difference; splitAt = index + 1; }
  });
  // Preserve rank groups unless that leaves a row more than two cards denser.
  const splitIndex = best <= 2 ? groups.slice(0, splitAt).reduce((sum, group) => sum + group.cards.length, 0) : Math.ceil(cards.length / 2);
  const rowGroups = [buildGroups(cards.slice(0, splitIndex)), buildGroups(cards.slice(splitIndex)).map(group => ({ ...group, startIndex: group.startIndex + splitIndex }))];
  const singlePositions = buildFittedPositions(groups, width, 72, 32, 8);
  const compactPositions = buildFittedPositions(groups, width, 58, 25, 5);
  const rowPositions = new Map<number, number>();

  rowGroups.forEach((row) => {
    for (const [index, x] of buildFittedPositions(row, width, 58, 30, 7)) rowPositions.set(index, x);
  });

  const items: HandCardLayout[] = [];

  rowGroups.forEach((row, rowIndex) => {
    const rowCards = row.flatMap((group) => group.cards);
    let cursor = 0;
    row.forEach((group, groupPosition) => {
      const groupIndex = groups.findIndex(original => group.startIndex >= original.startIndex && group.startIndex < original.startIndex + original.cards.length);
      group.cards.forEach((card, groupOffset) => {
        const index = group.startIndex + groupOffset;
        items.push({
          card,
          key: cardKey(card),
          index,
          groupIndex,
          groupSize: group.cards.length,
          groupOffset,
          groupPosition,
          row: rowIndex,
          rowIndex: cursor,
          rowCount: rowCards.length,
          singleX: singlePositions.get(index) ?? 0,
          compactX: compactPositions.get(index) ?? 0,
          rowX: rowPositions.get(index) ?? 0
        });
        cursor += 1;
      });
    });
  });

  return items.sort((a, b) => a.index - b.index);
}

function buildGroups(cards: CardInfo[]): HandGroup[] {
  const groups: HandGroup[] = [];
  cards.forEach((card, index) => {
    const current = groups[groups.length - 1];
    if (!current || current.cards[0].rank !== card.rank) groups.push({ cards: [card], startIndex: index });
    else current.cards.push(card);
  });
  return groups;
}

// Fit every layout variant to the measured container, including single-row landscape hands.
function buildFittedPositions(groups: HandGroup[], width: number, cardWidth: number, maxStep: number, groupGap: number): Map<number, number> {
  const count = groups.reduce((sum, group) => sum + group.cards.length, 0);
  const available = Math.max(0, width - cardWidth - 8);
  const gap = Math.min(groupGap, Math.max(0, available - (count - 1) * 24) / Math.max(1, groups.length - 1));
  const step = Math.min(maxStep, Math.max(0, (available - Math.max(0, groups.length - 1) * gap) / Math.max(1, count - 1)));
  return buildCenteredPositions(groups, step, gap);
}

function buildCenteredPositions(groups: HandGroup[], cardStep: number, groupGap: number): Map<number, number> {
  const positions = new Map<number, number>();
  let cursor = 0;

  groups.forEach((group, groupIndex) => {
    group.cards.forEach((_, groupOffset) => {
      positions.set(group.startIndex + groupOffset, cursor);
      cursor += cardStep;
    });
    if (groupIndex < groups.length - 1) cursor += groupGap;
  });

  const values = [...positions.values()];
  if (!values.length) return positions;
  const center = (Math.min(...values) + Math.max(...values)) / 2;
  for (const [index, x] of positions) positions.set(index, Math.round(x - center));
  return positions;
}
