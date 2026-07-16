import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type PointerEvent
} from 'react';
import type { CardInfo } from '../../protocol/types';
import { cardKey } from './cardModel';
import { Card } from './Card';

interface HandProps {
  cards: CardInfo[];
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

interface ActiveDrag {
  pointerId: number;
  startIndex: number;
  lastIndex: number;
  moved: boolean;
  mode: 'add' | 'remove';
  originalSelection: Set<string>;
}

export function Hand({ cards, selected, disabled = false, onToggle, onRangeSelect }: HandProps) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const buttonRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const activeDragRef = useRef<ActiveDrag | null>(null);
  const suppressPointerClickRef = useRef(false);
  const suppressedClickTimerRef = useRef<number | null>(null);
  const doubleClickSelectionRef = useRef<Set<string> | null>(null);
  const [focusedIndex, setFocusedIndex] = useState(0);
  const [isDragging, setIsDragging] = useState(false);
  const [dragPreview, setDragPreview] = useState<Set<string> | null>(null);
  const layout = useMemo(() => buildHandLayout(cards), [cards]);
  const keys = useMemo(() => layout.map((item) => item.key), [layout]);
  const displayedSelection = dragPreview ?? selected;

  useEffect(() => {
    buttonRefs.current.length = layout.length;
    setFocusedIndex((current) => Math.max(0, Math.min(current, layout.length - 1)));
  }, [layout.length]);

  useEffect(() => () => {
    if (suppressedClickTimerRef.current !== null) {
      window.clearTimeout(suppressedClickTimerRef.current);
    }
  }, []);

  function indexFromPoint(clientX: number, clientY: number): number {
    const target = typeof document.elementFromPoint === 'function'
      ? document.elementFromPoint(clientX, clientY)
      : null;
    return indexFromTarget(target);
  }

  function indexFromTarget(target: EventTarget | Element | null): number {
    const element = (target as HTMLElement | null)?.closest?.('[data-card-index]');
    const index = element ? Number((element as HTMLElement).dataset.cardIndex) : -1;
    return Number.isInteger(index) ? index : -1;
  }

  function orderedSelection(selection: Set<string>): string[] {
    return keys.filter((key) => selection.has(key));
  }

  function selectGroup(groupIndex: number, baseSelection = selected) {
    const groupKeys = layout.filter((item) => item.groupIndex === groupIndex).map((item) => item.key);
    if (!groupKeys.length) return;
    const allSelected = groupKeys.every((key) => baseSelection.has(key));
    const next = new Set(baseSelection);
    for (const key of groupKeys) {
      if (allSelected) next.delete(key);
      else next.add(key);
    }
    onRangeSelect(orderedSelection(next));
  }

  function moveFocus(nextIndex: number) {
    if (disabled || layout.length === 0) return;
    const normalizedIndex = (nextIndex + layout.length) % layout.length;
    setFocusedIndex(normalizedIndex);
    buttonRefs.current[normalizedIndex]?.focus();
  }

  function handleCardKeyDown(event: KeyboardEvent<HTMLButtonElement>, item: HandCardLayout) {
    if (disabled) return;
    switch (event.key) {
      case 'ArrowLeft':
      case 'ArrowUp':
        event.preventDefault();
        moveFocus(item.index - 1);
        return;
      case 'ArrowRight':
      case 'ArrowDown':
        event.preventDefault();
        moveFocus(item.index + 1);
        return;
      case 'Home':
        event.preventDefault();
        moveFocus(0);
        return;
      case 'End':
        event.preventDefault();
        moveFocus(layout.length - 1);
        return;
      case 'Enter':
      case ' ':
      case 'Spacebar':
        event.preventDefault();
        if (event.repeat) return;
        if (event.shiftKey) selectGroup(item.groupIndex);
        else onToggle(item.key);
        return;
      default:
    }
  }

  function applyDragRange(drag: ActiveDrag, endIndex: number) {
    if (endIndex < 0 || endIndex >= keys.length) return;
    drag.lastIndex = endIndex;
    if (endIndex !== drag.startIndex) drag.moved = true;
    if (!drag.moved) return;

    const min = Math.min(drag.startIndex, endIndex);
    const max = Math.max(drag.startIndex, endIndex);
    const next = new Set(drag.originalSelection);
    for (const key of keys.slice(min, max + 1)) {
      if (drag.mode === 'add') next.add(key);
      else next.delete(key);
    }
    setDragPreview(next);
    onRangeSelect(orderedSelection(next));
  }

  function suppressNextPointerClick() {
    suppressPointerClickRef.current = true;
    if (suppressedClickTimerRef.current !== null) {
      window.clearTimeout(suppressedClickTimerRef.current);
    }
    suppressedClickTimerRef.current = window.setTimeout(() => {
      suppressPointerClickRef.current = false;
      suppressedClickTimerRef.current = null;
    }, 0);
  }

  function releasePointer(event: PointerEvent<HTMLDivElement>) {
    if (event.currentTarget.hasPointerCapture?.(event.pointerId)) {
      event.currentTarget.releasePointerCapture?.(event.pointerId);
    }
  }

  function finishDrag(event: PointerEvent<HTMLDivElement>, cancelled: boolean) {
    const drag = activeDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;

    if (cancelled) {
      onRangeSelect(orderedSelection(drag.originalSelection));
      suppressNextPointerClick();
    } else {
      const pointIndex = indexFromPoint(event.clientX, event.clientY);
      const endIndex = pointIndex >= 0 ? pointIndex : indexFromTarget(event.target);
      if (endIndex >= 0 && endIndex !== drag.lastIndex) applyDragRange(drag, endIndex);
      if (drag.moved) suppressNextPointerClick();
    }

    activeDragRef.current = null;
    setIsDragging(false);
    setDragPreview(null);
    releasePointer(event);
  }

  return (
    <div
      ref={rootRef}
      className={`hand ${disabled ? 'is-disabled' : ''} ${isDragging ? 'is-dragging' : ''}`}
      style={{ '--hand-count': cards.length } as CSSProperties}
      role="group"
      aria-label="手牌"
      aria-disabled={disabled || undefined}
      onPointerDown={(event) => {
        if (disabled) return;
        if (activeDragRef.current || event.isPrimary === false) return;
        if (event.pointerType === 'mouse' && event.button !== 0) return;
        const index = indexFromTarget(event.target);
        if (index < 0 || index >= keys.length) return;

        const originalSelection = new Set(selected);
        activeDragRef.current = {
          pointerId: event.pointerId,
          startIndex: index,
          lastIndex: index,
          moved: false,
          mode: originalSelection.has(keys[index]) ? 'remove' : 'add',
          originalSelection
        };
        setFocusedIndex(index);
        setIsDragging(true);
        setDragPreview(null);
        event.currentTarget.setPointerCapture?.(event.pointerId);
      }}
      onPointerMove={(event) => {
        const drag = activeDragRef.current;
        if (!drag || drag.pointerId !== event.pointerId) return;
        if (disabled) {
          finishDrag(event, true);
          return;
        }
        event.preventDefault();
        const pointIndex = indexFromPoint(event.clientX, event.clientY);
        const index = pointIndex >= 0 ? pointIndex : indexFromTarget(event.target);
        applyDragRange(drag, index);
      }}
      onPointerUp={(event) => finishDrag(event, disabled)}
      onPointerCancel={(event) => finishDrag(event, true)}
    >
      {layout.map((item) => {
        const isSelected = displayedSelection.has(item.key);
        return (
          <div
            key={`${item.key}_${item.index}`}
            className={`hand__slot ${isSelected ? 'is-selected' : ''}`}
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
          >
            <Card
              ref={(node) => {
                buttonRefs.current[item.index] = node;
              }}
              card={item.card}
              selected={isSelected}
              disabled={disabled}
              tabIndex={disabled ? -1 : item.index === focusedIndex ? 0 : -1}
              onFocus={() => setFocusedIndex(item.index)}
              onKeyDown={(event) => handleCardKeyDown(event, item)}
              onClick={(event) => {
                if (disabled) return;
                if (event.detail > 0 && suppressPointerClickRef.current) {
                  suppressPointerClickRef.current = false;
                  if (suppressedClickTimerRef.current !== null) {
                    window.clearTimeout(suppressedClickTimerRef.current);
                    suppressedClickTimerRef.current = null;
                  }
                  event.preventDefault();
                  return;
                }
                if (event.detail > 1) return;
                if (event.detail === 1) doubleClickSelectionRef.current = new Set(selected);
                onToggle(item.key);
              }}
              onDoubleClick={() => {
                if (disabled) return;
                selectGroup(item.groupIndex, doubleClickSelectionRef.current ?? selected);
                doubleClickSelectionRef.current = null;
              }}
            />
          </div>
        );
      })}
    </div>
  );
}

export function buildHandLayout(cards: CardInfo[]): HandCardLayout[] {
  const groups = buildGroups(cards);
  const splitAt = Math.ceil(groups.length / 2);
  const rowGroups = [groups.slice(0, splitAt), groups.slice(splitAt)];
  const singlePositions = buildCenteredPositions(groups, 32, 8);
  const compactPositions = buildCenteredPositions(groups, 25, 5);
  const rowPositions = new Map<number, number>();

  rowGroups.forEach((row) => {
    for (const [index, x] of buildCenteredPositions(row, 30, 7)) rowPositions.set(index, x);
  });

  const items: HandCardLayout[] = [];

  rowGroups.forEach((row, rowIndex) => {
    const rowCards = row.flatMap((group) => group.cards);
    let cursor = 0;
    row.forEach((group, groupPosition) => {
      const groupIndex = rowIndex === 0 ? groupPosition : splitAt + groupPosition;
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
