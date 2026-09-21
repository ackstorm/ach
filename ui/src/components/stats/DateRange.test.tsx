// DateRange.test.tsx — jsdom suite for the STATS-09 date-range control cluster.
//
// Pragmatic: asserts the preset wiring (callbacks + active state) and the
// custom-calendar open + two-click Apply emit. The pure UTC date helpers
// (toUTCDateString / buildMonthGrid) are exercised directly to lock their
// behavior independently of the rendered month.

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, within } from '@testing-library/react';

import { PRESETS } from '@/lib/stats-presets';
import { DateRange, buildMonthGrid, toUTCDateString } from './DateRange';

describe('toUTCDateString', () => {
  it('formats a UTC date as YYYY-MM-DD (zero-padded)', () => {
    expect(toUTCDateString(new Date(Date.UTC(2026, 2, 1)))).toBe('2026-03-01');
    expect(toUTCDateString(new Date(Date.UTC(2026, 11, 25)))).toBe('2026-12-25');
  });
});

describe('buildMonthGrid', () => {
  it('leads with nulls for the weekday offset, then 1..daysInMonth', () => {
    // March 2026: Mar 1 is a Sunday (getUTCDay 0) -> no leading nulls, 31 days.
    const march = buildMonthGrid(2026, 2);
    expect(march[0]).toBe(1);
    expect(march.filter((d) => d != null)).toHaveLength(31);
    // February 2026: Feb 1 is a Sunday -> 28 days, no leading nulls.
    const feb = buildMonthGrid(2026, 1);
    expect(feb.filter((d) => d != null)).toHaveLength(28);
  });
});

describe('DateRange', () => {
  function setup(overrides: Partial<React.ComponentProps<typeof DateRange>> = {}) {
    const onPreset = vi.fn();
    const onCustomRange = vi.fn();
    const utils = render(
      <DateRange
        preset="30d"
        onPreset={onPreset}
        onCustomRange={onCustomRange}
        {...overrides}
      />
    );
    return { onPreset, onCustomRange, ...utils };
  }

  it('renders all seven preset buttons', () => {
    const { getByRole } = setup();
    for (const p of PRESETS) {
      expect(getByRole('button', { name: p })).toBeInTheDocument();
    }
  });

  it('clicking 30d calls onPreset("30d")', () => {
    const { getByRole, onPreset } = setup();
    fireEvent.click(getByRole('button', { name: '30d' }));
    expect(onPreset).toHaveBeenCalledWith('30d');
  });

  it('the active preset is distinguishable (aria-pressed + accent class)', () => {
    const { getByRole } = setup({ preset: '7d' });
    const active = getByRole('button', { name: '7d' });
    const inactive = getByRole('button', { name: '30d' });
    expect(active).toHaveAttribute('aria-pressed', 'true');
    expect(active).toHaveAttribute('data-active', '');
    expect(active.className).toContain('bg-primary/10');
    expect(inactive).toHaveAttribute('aria-pressed', 'false');
    expect(inactive.className).not.toContain('bg-primary/10');
  });

  it('renders the inline rangeError text when provided', () => {
    const { getByText } = setup({ rangeError: 'Range exceeds 366 days' });
    expect(getByText('Range exceeds 366 days')).toBeInTheDocument();
  });

  it('clicking Custom only opens the calendar — it does NOT change the range yet', () => {
    const { getByRole, getByLabelText, onPreset, onCustomRange } = setup();
    fireEvent.click(getByRole('button', { name: 'Custom' }));
    // The calendar dialog is open: its month-nav buttons exist.
    expect(getByLabelText('Previous month')).toBeInTheDocument();
    expect(getByLabelText('Next month')).toBeInTheDocument();
    // But nothing committed — opening the picker must not refetch the charts.
    expect(onPreset).not.toHaveBeenCalled();
    expect(onCustomRange).not.toHaveBeenCalled();
  });

  it('picking two days then Apply calls onCustomRange with {start,end} (start<=end)', () => {
    const { getByRole, queryByLabelText, onCustomRange } = setup();
    fireEvent.click(getByRole('button', { name: 'Custom' }));

    const dialog = getByRole('dialog');
    const grid = within(dialog);
    // Pick two distinct day cells by their visible number. Both '5' and '15'
    // exist in every month, so this is month-agnostic. Apply is enabled after
    // the first pick.
    fireEvent.click(grid.getByRole('button', { name: '15' }));
    fireEvent.click(grid.getByRole('button', { name: '5' }));

    const applyBtn = getByRole('button', { name: 'Apply' });
    expect(applyBtn).not.toBeDisabled();
    fireEvent.click(applyBtn);

    expect(onCustomRange).toHaveBeenCalledTimes(1);
    const arg = onCustomRange.mock.calls[0][0] as { start: string; end: string };
    expect(arg.start).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    expect(arg.end).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    // start <= end after the two-click normalization (picked 15 then 5).
    expect(arg.start <= arg.end).toBe(true);
    expect(arg.start.endsWith('-05')).toBe(true);
    expect(arg.end.endsWith('-15')).toBe(true);
    // The popover closes on Apply.
    expect(queryByLabelText('Previous month')).toBeNull();
  });

  it('single-day pick collapses to start==end on Apply', () => {
    const { getByRole, onCustomRange } = setup();
    fireEvent.click(getByRole('button', { name: 'Custom' }));
    const dialog = getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: '10' }));
    fireEvent.click(getByRole('button', { name: 'Apply' }));
    const arg = onCustomRange.mock.calls[0][0] as { start: string; end: string };
    expect(arg.start).toBe(arg.end);
    expect(arg.start.endsWith('-10')).toBe(true);
  });

  it('Apply is disabled before any day is picked', () => {
    const { getByRole } = setup();
    fireEvent.click(getByRole('button', { name: 'Custom' }));
    expect(getByRole('button', { name: 'Apply' })).toBeDisabled();
  });
});
