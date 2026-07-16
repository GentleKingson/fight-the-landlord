import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { CardInfo } from '../src/protocol/types';
import { cardKey } from '../src/shared/cards/cardModel';
import { Hand } from '../src/shared/cards/Hand';

function c(suit: number, rank: number): CardInfo {
  return { suit, rank, color: suit === 1 || suit === 3 ? 1 : 0 };
}

const cards = [c(0, 10), c(1, 10), c(2, 9), c(3, 8)];
const keys = cards.map(cardKey);
const originalElementFromPoint = document.elementFromPoint;

interface ControlledHandProps {
  initial?: string[];
  disabled?: boolean;
  onToggleCall?: (key: string) => void;
  onRangeCall?: (keys: string[]) => void;
}

function ControlledHand({
  initial = [],
  disabled = false,
  onToggleCall,
  onRangeCall
}: ControlledHandProps) {
  const [selected, setSelected] = useState(() => new Set(initial));

  return (
    <>
      <Hand
        cards={cards}
        selected={selected}
        disabled={disabled}
        onToggle={(key) => {
          onToggleCall?.(key);
          setSelected((current) => {
            const next = new Set(current);
            if (next.has(key)) next.delete(key);
            else next.add(key);
            return next;
          });
        }}
        onRangeSelect={(nextKeys) => {
          onRangeCall?.(nextKeys);
          setSelected(new Set(nextKeys));
        }}
      />
      <output data-testid="selection">
        {keys.filter((key) => selected.has(key)).join(',')}
      </output>
    </>
  );
}

function selection(): string[] {
  const value = screen.getByTestId('selection').textContent ?? '';
  return value ? value.split(',') : [];
}

function setPointTarget(target: Element) {
  Object.defineProperty(document, 'elementFromPoint', {
    configurable: true,
    value: vi.fn(() => target)
  });
}

afterEach(() => {
  cleanup();
  Object.defineProperty(document, 'elementFromPoint', {
    configurable: true,
    value: originalElementFromPoint
  });
});

describe('Hand interaction', () => {
  it('uses pressed card buttons and toggles selection by click', () => {
    render(<ControlledHand initial={[keys[0]]} />);

    expect(screen.getByRole('group', { name: '手牌' })).toBeInTheDocument();
    const buttons = screen.getAllByRole('button');
    expect(buttons.map((button) => button.tabIndex)).toEqual([0, -1, -1, -1]);
    expect(buttons[0]).toHaveAttribute('aria-pressed', 'true');
    expect(buttons[1]).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(buttons[1]);
    expect(selection()).toEqual([keys[0], keys[1]]);
    expect(buttons[1]).toHaveAttribute('aria-pressed', 'true');
  });

  it('provides wrapping roving focus with arrows, Home, and End', () => {
    render(<ControlledHand />);
    const buttons = screen.getAllByRole('button');

    buttons[0].focus();
    fireEvent.keyDown(buttons[0], { key: 'ArrowRight' });
    expect(buttons[1]).toHaveFocus();
    expect(buttons.map((button) => button.tabIndex)).toEqual([-1, 0, -1, -1]);

    fireEvent.keyDown(buttons[1], { key: 'ArrowDown' });
    expect(buttons[2]).toHaveFocus();
    fireEvent.keyDown(buttons[2], { key: 'End' });
    expect(buttons[3]).toHaveFocus();
    fireEvent.keyDown(buttons[3], { key: 'Home' });
    expect(buttons[0]).toHaveFocus();
    fireEvent.keyDown(buttons[0], { key: 'ArrowLeft' });
    expect(buttons[3]).toHaveFocus();
    fireEvent.keyDown(buttons[3], { key: 'ArrowRight' });
    expect(buttons[0]).toHaveFocus();
  });

  it('activates cards with Enter and Space', () => {
    const onToggleCall = vi.fn();
    render(<ControlledHand onToggleCall={onToggleCall} />);
    const first = screen.getAllByRole('button')[0];

    fireEvent.keyDown(first, { key: 'Enter' });
    expect(selection()).toEqual([keys[0]]);
    fireEvent.keyDown(first, { key: ' ' });
    expect(selection()).toEqual([]);
    expect(onToggleCall).toHaveBeenCalledTimes(2);
  });

  it('toggles an equal-rank group deterministically by keyboard or double click', () => {
    render(<ControlledHand />);
    const first = screen.getAllByRole('button')[0];

    fireEvent.keyDown(first, { key: 'Enter', shiftKey: true });
    expect(selection()).toEqual([keys[0], keys[1]]);
    fireEvent.keyDown(first, { key: ' ', shiftKey: true });
    expect(selection()).toEqual([]);

    fireEvent.click(first, { detail: 1 });
    fireEvent.click(first, { detail: 2 });
    fireEvent.doubleClick(first, { detail: 2 });
    expect(selection()).toEqual([keys[0], keys[1]]);

    fireEvent.click(first, { detail: 1 });
    fireEvent.click(first, { detail: 2 });
    fireEvent.doubleClick(first, { detail: 2 });
    expect(selection()).toEqual([]);
  });

  it('makes every interaction inert when disabled', () => {
    const onToggleCall = vi.fn();
    const onRangeCall = vi.fn();
    render(<ControlledHand disabled onToggleCall={onToggleCall} onRangeCall={onRangeCall} />);
    const group = screen.getByRole('group', { name: '手牌' });
    const buttons = screen.getAllByRole('button');

    expect(group).toHaveAttribute('aria-disabled', 'true');
    for (const button of buttons) {
      expect(button).toBeDisabled();
      expect(button).toHaveAttribute('tabindex', '-1');
    }

    fireEvent.click(buttons[0]);
    fireEvent.keyDown(buttons[0], { key: 'Enter' });
    fireEvent.pointerDown(buttons[0], { pointerId: 1, pointerType: 'touch' });
    fireEvent.pointerMove(group, { pointerId: 1, pointerType: 'touch' });
    fireEvent.pointerUp(group, { pointerId: 1, pointerType: 'touch' });
    expect(onToggleCall).not.toHaveBeenCalled();
    expect(onRangeCall).not.toHaveBeenCalled();
    expect(selection()).toEqual([]);
  });

  it('adds a touch-drag range from the original selection without accumulating old previews', () => {
    const { container } = render(<ControlledHand initial={[keys[3]]} />);
    const group = screen.getByRole('group', { name: '手牌' });
    const buttons = screen.getAllByRole('button');
    const slots = container.querySelectorAll('[data-card-index]');

    setPointTarget(slots[2]);
    fireEvent.pointerDown(buttons[0], { pointerId: 7, pointerType: 'touch', button: 0 });
    fireEvent.pointerMove(group, { pointerId: 7, pointerType: 'touch', clientX: 20, clientY: 20 });
    expect(selection()).toEqual(keys);

    setPointTarget(slots[1]);
    fireEvent.pointerMove(group, { pointerId: 7, pointerType: 'touch', clientX: 10, clientY: 20 });
    expect(selection()).toEqual([keys[0], keys[1], keys[3]]);
    fireEvent.pointerUp(group, { pointerId: 7, pointerType: 'touch', clientX: 10, clientY: 20 });

    fireEvent.click(buttons[0], { detail: 1 });
    expect(selection()).toEqual([keys[0], keys[1], keys[3]]);
  });

  it('removes a drag range when the starting card was originally selected', () => {
    const { container } = render(<ControlledHand initial={keys} />);
    const group = screen.getByRole('group', { name: '手牌' });
    const buttons = screen.getAllByRole('button');
    const slots = container.querySelectorAll('[data-card-index]');

    setPointTarget(slots[2]);
    fireEvent.pointerDown(buttons[0], { pointerId: 8, pointerType: 'mouse', button: 0 });
    fireEvent.pointerMove(group, { pointerId: 8, pointerType: 'mouse', clientX: 20, clientY: 20 });
    expect(selection()).toEqual([keys[3]]);
    fireEvent.pointerUp(group, { pointerId: 8, pointerType: 'mouse', clientX: 20, clientY: 20 });
    expect(selection()).toEqual([keys[3]]);
  });

  it('rolls selection back to the pointer-down snapshot on pointercancel', () => {
    const onRangeCall = vi.fn();
    const { container } = render(<ControlledHand initial={[keys[2]]} onRangeCall={onRangeCall} />);
    const group = screen.getByRole('group', { name: '手牌' });
    const buttons = screen.getAllByRole('button');
    const slots = container.querySelectorAll('[data-card-index]');

    setPointTarget(slots[1]);
    fireEvent.pointerDown(buttons[0], { pointerId: 9, pointerType: 'touch', button: 0 });
    fireEvent.pointerMove(group, { pointerId: 9, pointerType: 'touch', clientX: 10, clientY: 20 });
    expect(selection()).toEqual([keys[0], keys[1], keys[2]]);

    fireEvent.pointerCancel(group, { pointerId: 9, pointerType: 'touch' });
    expect(selection()).toEqual([keys[2]]);
    expect(onRangeCall).toHaveBeenLastCalledWith([keys[2]]);

    fireEvent.click(buttons[0], { detail: 1 });
    expect(selection()).toEqual([keys[2]]);
  });
});
