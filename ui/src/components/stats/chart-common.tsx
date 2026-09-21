// chart-common.tsx — shared chrome for the two Recharts time-series (STATS-03
// Daily Spend area, STATS-04 Requests-by-Day bars).
//
// Ports the dark-terminal intent of the OLD uPlot wrapper (src/ui/charts.js): a
// ~240px chart, --border gridlines, muted ticks, a dark themed hover tooltip,
// and the LOCKED empty-state copy shown instead of an empty grid. Everything is
// themed via CSS custom properties (Recharts accepts `var(--x)` in stroke/fill
// strings) — NEVER a raw hex — so the charts track the same green tokens as the
// hand-rolled panels. DRY: SpendChart + RequestsChart both import from here.

import * as React from 'react';
import { CalendarRange } from 'lucide-react';
import type { TooltipProps } from 'recharts';

import { formatDate } from '@/lib/format';

// Chart height — matches charts.js CHART_HEIGHT (13-UI-SPEC §3, ~220-260px).
export const CHART_HEIGHT = 240;

// Locked empty-state copy, lifted verbatim from charts.js EMPTY_COPY.
export const CHART_EMPTY_COPY = 'No usage in this range';

// Shared axis tick styling — muted --text-tertiary, 11px (mirrors the 12px
// --dim mono ticks of the old uPlot axes). var(--*) only, no hex.
export const AXIS_TICK = { fill: 'var(--text-tertiary)', fontSize: 11 } as const;

// Shared axis/grid line color (--border gridlines + ticklines).
export const AXIS_LINE_STROKE = 'var(--border)';

// Series accent (green). --primary === --chart-1.
export const SERIES_COLOR = 'var(--primary)';

// Dark themed Tooltip surface (mirrors the old .chart-tooltip chrome).
export const TOOLTIP_CONTENT_STYLE = {
  background: 'var(--surface-elevated)',
  border: '1px solid var(--border)',
  borderRadius: 12,
  color: 'var(--text-primary)',
} as const;

export const TOOLTIP_LABEL_STYLE = { color: 'var(--text-secondary)' } as const;
export const TOOLTIP_ITEM_STYLE = { color: 'var(--text-primary)' } as const;

/**
 * ChartEmpty — the empty state shown when there is no usage in the range (NOT an
 * empty Recharts grid, and NOT the zero-filled blank axis grid — the charts gate
 * on seriesHasActivity). A muted icon + the locked copy + a "widen the range"
 * hint so it reads as intentional, not broken. Min-height matches CHART_HEIGHT.
 */
export function ChartEmpty(): React.ReactElement {
  return (
    <div
      className="flex flex-col items-center justify-center gap-2 text-center"
      style={{ minHeight: CHART_HEIGHT }}
    >
      <CalendarRange className="size-6 text-text-tertiary/60" aria-hidden="true" />
      <div className="text-sm text-text-secondary">{CHART_EMPTY_COPY}</div>
      <div className="text-xs text-text-tertiary">Try a wider date range.</div>
    </div>
  );
}

/**
 * StatTile — a headline metric tile (10px caption label above, 20px value below),
 * shared by the Latency + Top-Keys panel summaries so their top rows read
 * identically. `tone="bad"` reddens the value (e.g. a non-zero error rate);
 * `title` adds a hover hint on the label.
 */
export function StatTile({
  label,
  value,
  title,
  tone = 'default',
}: {
  label: string;
  value: string;
  title?: string;
  tone?: 'default' | 'bad';
}): React.ReactElement {
  return (
    <div className="flex flex-col gap-0.5">
      <div
        title={title}
        className="font-mono text-[10px] font-semibold uppercase tracking-wider text-text-secondary"
      >
        {label}
      </div>
      <div
        className={`font-sans text-xl font-semibold leading-tight tabular-nums ${tone === 'bad' ? 'text-destructive' : 'text-text-primary'}`}
      >
        {value}
      </div>
    </div>
  );
}

/**
 * Build a themed Recharts <Tooltip> formatter that renders one value through the
 * supplied formatter (formatCurrency / formatInt). Mirrors the old native hover
 * tooltip intent: `formatDate(date) · formatValue`.
 */
export function makeTooltipFormatter(
  formatValue: (n: number) => string,
): NonNullable<TooltipProps<number, string>['formatter']> {
  return (value) => formatValue(typeof value === 'number' ? value : Number(value));
}

// Tooltip label is the bucket date string -> "Mar 01, 2026".
export const tooltipLabelFormatter: NonNullable<
  TooltipProps<number, string>['labelFormatter']
> = (label) => formatDate(String(label));
