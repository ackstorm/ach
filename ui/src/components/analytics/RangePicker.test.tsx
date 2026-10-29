import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { presetToRange } from '@/lib/stats-presets';
import { useConsoleRange } from '@/stores/console-range';
import { RangePicker, buildMonthGrid, customRangeError } from './RangePicker';

beforeEach(() => { window.sessionStorage.clear(); useConsoleRange.setState({ preset: 'This month', custom: null }); });
afterEach(cleanup);

describe('RangePicker', () => {
  it('a preset click sets the shared store and persists it for the session', () => {
    render(<RangePicker />);
    fireEvent.click(screen.getByRole('button', { name: /^This month · / }));
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Activity period' })).getByRole('button', { name: 'Last month' }));
    expect(useConsoleRange.getState().preset).toBe('Last month');
    expect(JSON.parse(window.sessionStorage.getItem('console.range')!)).toEqual({ preset: 'Last month', custom: null });
    expect(screen.queryByRole('dialog')).toBeNull();
    const { start } = presetToRange('Last month', new Date());
    expect(screen.getByRole('button', { name: /^Last month · / })).toHaveAttribute('title', expect.stringContaining(start));
  });

  it('Custom opens the calendar without changing the range; one pick + Apply is a one-day range', () => {
    render(<RangePicker />);
    fireEvent.click(screen.getByRole('button', { name: /This month/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Custom' }));
    expect(useConsoleRange.getState().preset).toBe('This month');
    const dialog = within(screen.getByRole('dialog'));
    expect(dialog.getByRole('button', { name: 'Apply' })).toBeDisabled();
    fireEvent.click(dialog.getByRole('button', { name: '1' }));
    fireEvent.click(dialog.getByRole('button', { name: 'Apply' }));
    const { preset, custom } = useConsoleRange.getState();
    expect(preset).toBe('Custom');
    expect(custom!.start).toBe(custom!.end);
    expect(custom!.start.endsWith('-01')).toBe(true);
  });
});

describe('customRangeError', () => {
  const today = '2026-10-28';
  it('accepts 10 and 40 days (evidence panels show their own 31-day notice)', () => {
    expect(customRangeError({ start: '2026-10-10', end: '2026-10-19' }, today)).toBeNull();
    expect(customRangeError({ start: '2026-09-10', end: '2026-10-19' }, today)).toBeNull();
  });
  it('rejects a future end and more than 366 days', () => {
    expect(customRangeError({ start: '2026-10-20', end: '2026-10-29' }, today)).toMatch(/future/);
    expect(customRangeError({ start: '2025-01-01', end: '2026-10-01' }, today)).toMatch(/366/);
  });
});

it('buildMonthGrid leads with the weekday offset', () => {
  expect(buildMonthGrid(2026, 2).filter((d) => d != null)).toHaveLength(31);
  expect(buildMonthGrid(2026, 9).slice(0, 5)).toEqual([null, null, null, null, 1]); // Oct 1 2026 is a Thursday
});
