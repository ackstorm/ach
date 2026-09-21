// DateRange.tsx — the date-range control cluster for the #/stats Usage & Spend
// page (STATS-09 / UI-SPEC §1). React + Tailwind port of the old Preact + htm
// control src/ui/date-range.js.
//
// Two controls, top-right of the page header band (per the mockup):
//   • Preset buttons (PRESETS from @/lib/stats-presets): 7d / 30d / 90d /
//     This month / Last month / This year / Custom. The active preset is
//     accent-tinted; a click on a non-Custom preset calls onPreset(presetId) and
//     the container computes {start,end} via presetToRange and re-queries /stats.
//   • Custom — opens a HAND-BUILT dark-terminal calendar popover (a centered
//     scrim overlay). The day-grid + month-nav are hand-rolled over the token set
//     (D-11 — NO native date inputs, no heavy dep) with Apply/Cancel; Apply calls
//     onCustomRange({start,end}). Clicking Custom ALSO calls onPreset('Custom').
//
// SECURITY (threat register 13-04):
//   • T-13-09 (Tampering): presetToRange (the container's pure helper) clamps to
//     the ~366d cap so the client never submits an over-cap range; the server
//     re-validates (422), surfaced as the inline rangeError.

import * as React from 'react';

import { PRESETS } from '@/lib/stats-presets';
import { PILL_ACTIVE, PILL_BASE, PILL_IDLE } from '@/lib/ui';
import { cn } from '@/lib/utils';

// Locked copy, lifted verbatim from date-range.js (13-UI-SPEC §Copywriting).
const APPLY_LABEL = 'Apply';
const CANCEL_LABEL = 'Cancel';
const CUSTOM_PRESET = 'Custom';

const MONTH_NAMES = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];
const WEEKDAY_LABELS = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];

// Format a UTC Date as "YYYY-MM-DD" (tz-stable, mirrors stats-presets.ts).
// Ported verbatim from date-range.js toUTCDateString. Pure.
export function toUTCDateString(d: Date): string {
  const year = d.getUTCFullYear();
  const month = String(d.getUTCMonth() + 1).padStart(2, '0');
  const day = String(d.getUTCDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

// Build the day grid for a given UTC month: a leading run of nulls for the
// weekday offset, then 1..daysInMonth. Pure, deterministic on {year, month}.
// Ported verbatim from date-range.js buildMonthGrid.
export function buildMonthGrid(year: number, month: number): (number | null)[] {
  const firstWeekday = new Date(Date.UTC(year, month, 1)).getUTCDay();
  const daysInMonth = new Date(Date.UTC(year, month + 1, 0)).getUTCDate();
  const cells: (number | null)[] = [];
  for (let i = 0; i < firstWeekday; i++) cells.push(null);
  for (let d = 1; d <= daysInMonth; d++) cells.push(d);
  return cells;
}

// The current UTC month/year as {year, month} for the calendar's initial view.
// Reads new Date() — only used for the INITIAL component view (not a pure
// helper), matching date-range.js currentMonth.
function currentMonth(): { year: number; month: number } {
  const now = new Date();
  return { year: now.getUTCFullYear(), month: now.getUTCMonth() };
}

interface MonthView {
  year: number;
  month: number;
}

// ── CalendarPopover ──────────────────────────────────────────────────────────
// A hand-built two-click range picker (D-11 — no native date input). The user
// clicks a start day then an end day; Apply emits {start, end} as YYYY-MM-DD
// strings (start <= end normalized). Cancel/scrim-click dismisses with no emit.
function CalendarPopover({
  onApply,
  onCancel,
}: {
  onApply: (range: { start: string; end: string }) => void;
  onCancel: () => void;
}): React.ReactElement {
  const [view, setView] = React.useState<MonthView>(() => currentMonth());
  const [startDate, setStartDate] = React.useState<Date | null>(null);
  const [endDate, setEndDate] = React.useState<Date | null>(null);

  const cells = buildMonthGrid(view.year, view.month);

  const prevMonth = (): void => {
    const m = view.month - 1;
    setView(m < 0 ? { year: view.year - 1, month: 11 } : { year: view.year, month: m });
  };
  const nextMonth = (): void => {
    const m = view.month + 1;
    setView(m > 11 ? { year: view.year + 1, month: 0 } : { year: view.year, month: m });
  };

  const pickDay = (day: number | null): void => {
    if (day == null) return;
    const picked = new Date(Date.UTC(view.year, view.month, day));
    // First pick (or restarting after a complete range) sets the start.
    if (startDate === null || endDate !== null) {
      setStartDate(picked);
      setEndDate(null);
      return;
    }
    // Second pick sets the end; normalize so start <= end.
    if (picked.getTime() < startDate.getTime()) {
      setEndDate(startDate);
      setStartDate(picked);
    } else {
      setEndDate(picked);
    }
  };

  // A day is "in range" (start..end inclusive) or one of the two endpoints.
  const dayState = (day: number | null): 'is-start' | 'is-end' | 'is-inrange' | '' => {
    if (day == null) return '';
    const t = Date.UTC(view.year, view.month, day);
    const s = startDate ? startDate.getTime() : null;
    const e = endDate ? endDate.getTime() : null;
    if (s !== null && t === s) return 'is-start';
    if (e !== null && t === e) return 'is-end';
    if (s !== null && e !== null && t > s && t < e) return 'is-inrange';
    return '';
  };

  const canApply = startDate !== null;
  const apply = (): void => {
    if (!canApply || startDate === null) return;
    // A single-day pick (no end) collapses to a one-day range start==end.
    const end = endDate || startDate;
    onApply({ start: toUTCDateString(startDate), end: toUTCDateString(end) });
  };

  // Day-cell classes by selection state — accent fill for endpoints, accent tint
  // for the in-range span, neutral otherwise. var(--*) tokens only.
  const dayClass = (day: number | null): string => {
    const state = dayState(day);
    return cn(
      'flex aspect-square min-h-[40px] items-center justify-center rounded-md border border-transparent font-mono text-xs transition-colors',
      day == null
        ? 'cursor-default'
        : 'cursor-pointer text-text-primary hover:border-primary hover:text-text-primary',
      state === 'is-inrange' && 'bg-primary/10 text-primary',
      (state === 'is-start' || state === 'is-end') &&
        'border-primary bg-primary text-primary-foreground'
    );
  };

  return (
    <div
      data-slot="date-range-overlay"
      onClick={onCancel}
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-6"
    >
      <div
        data-slot="date-range-modal"
        role="dialog"
        aria-label="Select custom date range"
        onClick={(e) => e.stopPropagation()}
        className="flex w-full max-w-[320px] flex-col gap-4 rounded-2xl border border-border bg-surface p-5"
      >
        <div className="flex items-center justify-between">
          <button
            type="button"
            onClick={prevMonth}
            aria-label="Previous month"
            className="flex size-8 cursor-pointer items-center justify-center rounded-md border border-border font-mono text-text-secondary transition-colors hover:border-primary hover:text-primary"
          >
            ‹
          </button>
          <span className="font-mono text-xs font-semibold text-text-primary">
            {MONTH_NAMES[view.month]} {view.year}
          </span>
          <button
            type="button"
            onClick={nextMonth}
            aria-label="Next month"
            className="flex size-8 cursor-pointer items-center justify-center rounded-md border border-border font-mono text-text-secondary transition-colors hover:border-primary hover:text-primary"
          >
            ›
          </button>
        </div>
        <div className="grid grid-cols-7 gap-1">
          {WEEKDAY_LABELS.map((w, i) => (
            <span
              key={`wd${i}`}
              className="py-1 text-center font-mono text-[11px] font-semibold text-text-secondary"
            >
              {w}
            </span>
          ))}
          {cells.map((day, i) => (
            <button
              key={`d${i}`}
              type="button"
              disabled={day == null}
              onClick={() => pickDay(day)}
              className={dayClass(day)}
            >
              {day == null ? '' : day}
            </button>
          ))}
        </div>
        <div className="mt-1 flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="inline-flex min-h-[44px] cursor-pointer items-center justify-center rounded-lg border border-border bg-transparent px-3 font-mono text-[11px] font-semibold uppercase tracking-wide text-text-secondary transition-colors hover:border-text-tertiary hover:text-text-primary sm:min-h-8"
          >
            {CANCEL_LABEL}
          </button>
          <button
            type="button"
            disabled={!canApply}
            onClick={apply}
            className="inline-flex min-h-[44px] cursor-pointer items-center justify-center rounded-lg border border-primary bg-primary px-3 font-mono text-[11px] font-semibold uppercase tracking-wide text-primary-foreground transition-colors hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50 sm:min-h-8"
          >
            {APPLY_LABEL}
          </button>
        </div>
      </div>
    </div>
  );
}

// ── DateRange ────────────────────────────────────────────────────────────────
export interface DateRangeProps {
  /** The active preset id (one of PRESETS) — accent-tinted. */
  preset: string;
  /** A preset button click. The container computes the range via presetToRange. */
  onPreset: (id: string) => void;
  /** The calendar Apply emit. */
  onCustomRange: (range: { start: string; end: string }) => void;
  /** Optional inline error (server 422 over-cap / bad range) — surfaced ON the
   *  control, NOT a whole-page error. */
  rangeError?: string | null;
}

export function DateRange({
  preset,
  onPreset,
  onCustomRange,
  rangeError,
}: DateRangeProps): React.ReactElement {
  const [calOpen, setCalOpen] = React.useState(false);

  const clickPreset = (p: string): void => {
    if (p === CUSTOM_PRESET) {
      // Only OPEN the calendar — do NOT touch the range/preset yet. The range
      // (and the active-preset switch to Custom) commits on Apply via
      // onCustomRange; opening the picker must not refetch the charts.
      setCalOpen(true);
      return;
    }
    setCalOpen(false);
    onPreset(p);
  };

  const applyCustom = (range: { start: string; end: string }): void => {
    setCalOpen(false);
    onCustomRange(range);
  };

  // Preset buttons share the canonical segment-pill chrome (@/lib/ui) and add
  // the data-filter typography: mono + UPPERCASE + a 44px tap target.
  const pillBase = cn(
    PILL_BASE,
    'min-h-[44px] px-3 font-mono text-[11px] font-semibold uppercase tracking-wide sm:min-h-8'
  );
  const pillIdle = PILL_IDLE;
  const pillActive = PILL_ACTIVE;

  return (
    <div
      data-slot="date-range"
      className="relative flex flex-col items-end gap-2"
    >
      <div className="flex flex-wrap items-center gap-2">
        {PRESETS.map((p) => {
          const isActive = preset === p;
          return (
            <button
              key={p}
              type="button"
              data-slot="date-range-preset"
              data-active={isActive ? '' : undefined}
              aria-pressed={isActive}
              onClick={() => clickPreset(p)}
              className={cn(pillBase, isActive ? pillActive : pillIdle)}
            >
              {p}
            </button>
          );
        })}
      </div>
      {rangeError ? (
        <div
          data-slot="date-range-error"
          className="text-right font-sans text-xs text-destructive"
        >
          {rangeError}
        </div>
      ) : null}
      {calOpen ? (
        <CalendarPopover onApply={applyCustom} onCancel={() => setCalOpen(false)} />
      ) : null}
    </div>
  );
}
