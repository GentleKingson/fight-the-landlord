import { useState } from 'react';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import axe from 'axe-core';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GameTable } from '../src/features/table/GameTable';
import { Hand } from '../src/shared/cards/Hand';
import { cardKey } from '../src/shared/cards/cardModel';
import { useAppStore } from '../src/stores/appStore';
import { seedDemoState } from '../src/stores/demoState';
import type { CardInfo } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  });
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function StatefulHand({ cards, disabled = false }: { cards: CardInfo[]; disabled?: boolean }) {
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  return (
    <Hand
      cards={cards}
      selected={selected}
      disabled={disabled}
      onToggle={(key) => {
        setSelected((prev) => {
          const next = new Set(prev);
          if (next.has(key)) next.delete(key);
          else next.add(key);
          return next;
        });
      }}
      onRangeSelect={(keys) => setSelected(new Set(keys))}
    />
  );
}

describe('Hand accessibility and spatial keyboard interaction', () => {
  const sampleHand: CardInfo[] = [
    { rank: 14, suit: 0, color: 0 }, // A♠
    { rank: 13, suit: 1, color: 1 }, // K♥
    { rank: 12, suit: 2, color: 0 }, // Q♣
    { rank: 11, suit: 3, color: 1 }, // J♦
    { rank: 10, suit: 0, color: 0 }, // 10♠
    { rank: 9, suit: 1, color: 1 }   // 9♥
  ];

  it('passes axe nested-interactive and ARIA checks on Hand and GameTable with open drawer', async () => {
    seedDemoState('table');
    useAppStore.setState({ drawer: 'counter' });
    const { container } = render(<GameTable socket={{ send: vi.fn() } as unknown as GameSocket} />);

    // Confirm no nested button inside listbox/option
    const handListbox = screen.getByRole('listbox', { name: '手牌' });
    expect(within(handListbox).queryAllByRole('button')).toHaveLength(0);
    expect(within(handListbox).getAllByRole('option').length).toBeGreaterThan(0);

    const results = await axe.run(container, {
      rules: {
        'color-contrast': { enabled: false }
      }
    });
    const nestedViolations = results.violations.filter((violation) => violation.id === 'nested-interactive');
    expect(nestedViolations).toEqual([]);
    expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([]);
  });

  it('maintains roving tabindex, moves horizontally with ArrowRight/ArrowLeft, toggles with Enter/Space, and preserves boundary scrolling', () => {
    render(<StatefulHand cards={sampleHand} />);
    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(6);

    // Exactly one option exposes tabIndex=0 initially; all others are -1
    expect(options.map((el) => el.tabIndex)).toEqual([0, -1, -1, -1, -1, -1]);

    options[0].focus();
    expect(options[0]).toHaveFocus();

    // Pressing ArrowLeft on the leftmost card must NOT call preventDefault (preserves native scroll)
    const leftBoundaryEvent = new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true, cancelable: true });
    act(() => {
      options[0].dispatchEvent(leftBoundaryEvent);
    });
    expect(leftBoundaryEvent.defaultPrevented).toBe(false);
    expect(options[0]).toHaveFocus();

    // ArrowRight moves focus to the next card and shifts roving tabIndex
    const moveRightEvent = new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true });
    act(() => {
      options[0].dispatchEvent(moveRightEvent);
    });
    expect(moveRightEvent.defaultPrevented).toBe(true);
    expect(options[1]).toHaveFocus();
    expect(options.map((el) => el.tabIndex)).toEqual([-1, 0, -1, -1, -1, -1]);

    // Enter toggles selection on keydown
    fireEvent.keyDown(options[1], { key: 'Enter' });
    expect(options[1]).toHaveAttribute('aria-selected', 'true');

    // Space prevents scrolling on keydown and toggles selection on keyup
    const spaceDown = new KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true });
    act(() => {
      options[1].dispatchEvent(spaceDown);
    });
    expect(spaceDown.defaultPrevented).toBe(true);
    expect(options[1]).toHaveAttribute('aria-selected', 'true');

    fireEvent.keyUp(options[1], { key: ' ' });
    expect(options[1]).toHaveAttribute('aria-selected', 'false');

    // Single-row ArrowUp/ArrowDown should not intercept page scroll even when a neighbor is selected
    fireEvent.keyDown(options[1], { key: 'Enter' });
    expect(options[1]).toHaveAttribute('aria-selected', 'true');
    const upSingleRow = new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true });
    act(() => {
      options[0].focus();
      options[0].dispatchEvent(upSingleRow);
    });
    expect(upSingleRow.defaultPrevented).toBe(false);
  });

  it('navigates 2-row portrait geometry with ArrowDown and ArrowUp using live getBoundingClientRect()', () => {
    render(<StatefulHand cards={sampleHand} />);
    const options = screen.getAllByRole('option');

    // Mock 2-row portrait layout: row 0 (cards 0..2 at top=14..92), row 1 (cards 3..5 at top=94..172)
    options.forEach((option, index) => {
      const row = index < 3 ? 0 : 1;
      const col = index % 3;
      const left = 20 + col * 30;
      const isSelected = option.getAttribute('aria-selected') === 'true';
      const baseTop = row === 0 ? 14 : 94;
      vi.spyOn(option, 'getBoundingClientRect').mockImplementation(() => {
        const liftedTop = option.getAttribute('aria-selected') === 'true' ? baseTop - 14 : baseTop;
        return {
          x: left,
          y: liftedTop,
          left,
          top: liftedTop,
          right: left + 58,
          bottom: liftedTop + 78,
          width: 58,
          height: 78,
          toJSON: () => ({})
        } as DOMRect;
      });
      void isSelected;
    });

    // Focus middle card of top row (index 1: K♥) and press ArrowDown -> middle card of bottom row (index 4: 10♠)
    act(() => options[1].focus());
    const downEvent = new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true });
    act(() => {
      options[1].dispatchEvent(downEvent);
    });
    expect(downEvent.defaultPrevented).toBe(true);
    expect(options[4]).toHaveFocus();
    expect(options[4].tabIndex).toBe(0);

    // Select bottom-row card (lifts by 14px so top=80 slightly overlaps row 0 bottom=92) and press ArrowUp -> back to index 1
    fireEvent.keyDown(options[4], { key: 'Enter' });
    expect(options[4]).toHaveAttribute('aria-selected', 'true');

    const upEvent = new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true });
    act(() => {
      options[4].dispatchEvent(upEvent);
    });
    expect(upEvent.defaultPrevented).toBe(true);
    expect(options[1]).toHaveFocus();

    // Pressing ArrowUp again from top row does not preventDefault
    const topBoundaryEvent = new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true });
    act(() => {
      options[1].dispatchEvent(topBoundaryEvent);
    });
    expect(topBoundaryEvent.defaultPrevented).toBe(false);
    expect(options[1]).toHaveFocus();
  });
});

describe('UtilityDrawer popover, Escape dismissal, and focus management', () => {
  beforeEach(() => {
    seedDemoState('table');
    useAppStore.setState({
      drawer: 'none',
      selectedCards: new Set([cardKey(useAppStore.getState().hand[0])])
    });
  });

  it('uses popover="manual" non-modally, focuses close button on open, and restores focus to trigger on Escape or close click', () => {
    render(<GameTable socket={{ send: vi.fn() } as unknown as GameSocket} />);

    expect(screen.queryByRole('button', { name: '聊天' })).not.toBeInTheDocument();

    const counterTrigger = screen.getByRole('button', { name: '记牌器' });
    const historyTrigger = screen.getByRole('button', { name: '历史' });

    counterTrigger.focus();
    fireEvent.click(counterTrigger);

    const drawer = screen.getByLabelText('记牌器', { selector: 'aside' });
    expect(drawer).toHaveAttribute('popover', 'manual');
    expect(drawer).not.toHaveAttribute('aria-modal');
    expect(document.querySelector('main')?.hasAttribute('inert')).toBe(false);
    expect(counterTrigger).toHaveAttribute('aria-expanded', 'true');
    expect(counterTrigger).toHaveAttribute('aria-controls', 'table-utility-drawer');

    // Opening focuses the close button inside UtilityDrawer
    const closeButton = within(drawer).getByRole('button', { name: '关闭' });
    expect(closeButton).toHaveFocus();

    // Pressing Escape closes the drawer and returns focus to counterTrigger
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByLabelText('记牌器', { selector: 'aside' })).not.toBeInTheDocument();
    expect(counterTrigger).toHaveAttribute('aria-expanded', 'false');
    expect(counterTrigger).toHaveFocus();

    // Reopen via counter, switch directly to history, then click Close -> returns focus to historyTrigger
    fireEvent.click(counterTrigger);
    fireEvent.click(historyTrigger);
    const historyDrawer = screen.getByLabelText('动作历史', { selector: 'aside' });
    expect(within(historyDrawer).getByRole('button', { name: '关闭' })).toHaveFocus();
    fireEvent.click(within(historyDrawer).getByRole('button', { name: '关闭' }));
    expect(screen.queryByLabelText('动作历史', { selector: 'aside' })).not.toBeInTheDocument();
    expect(historyTrigger).toHaveFocus();
  });
});
