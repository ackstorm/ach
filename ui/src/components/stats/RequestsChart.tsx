// RequestsChart.tsx — STATS-04 Requests by Day, a bar chart (Recharts port of
// the old uPlot RequestsChart in src/ui/charts.js).
//
// Mirrors the old intent: accent (green) bars with a slightly rounded top,
// --border gridlines, muted ticks, a dark themed hover tooltip formatted with
// formatInt, ~240px tall. Themed entirely via var(--*) tokens — NEVER a raw hex.
//
// The REQUESTS/TOKENS metric toggle (RequestsMetricToggle) is a SEPARATE
// controlled control so the container can hoist it into the panel header row
// (next to the section label) instead of stealing a row above the chart. When no
// `metric` prop is supplied the chart falls back to its own internal state and
// renders the toggle inline (self-contained mode).
//
// Render order (shared with SpendChart):
//   1. loading        -> <Skeleton variant="chart" />
//   2. no rows         -> <ChartEmpty /> (the locked empty state, not a grid)
//   3. otherwise       -> the BarChart in a ResponsiveContainer.

import * as React from 'react';
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';

import type { StatsSeriesPoint } from '@/lib/api-types';
import { abbreviate, formatDate, formatInt } from '@/lib/format';
import { seriesHasActivity, seriesToRecharts } from '@/lib/stats-series';
import { Skeleton } from '@/components/ui/skeleton';

import {
  AXIS_LINE_STROKE,
  AXIS_TICK,
  CHART_HEIGHT,
  ChartEmpty,
  SERIES_COLOR,
  TOOLTIP_CONTENT_STYLE,
  TOOLTIP_ITEM_STYLE,
  TOOLTIP_LABEL_STYLE,
  makeTooltipFormatter,
  tooltipLabelFormatter,
} from './chart-common';

export type RequestsMetric = 'requests' | 'tokens' | 'failed';

// The REQUESTS/TOKENS segmented toggle. Controlled — the owner holds the metric
// state so it can place this control anywhere (e.g. the panel header row). A
// SINGLE bordered pill with both segments inside it (the active one tinted) so
// the two read as one aligned control, not a box-next-to-plain-text.
export function RequestsMetricToggle({
  metric,
  onChange,
}: {
  metric: RequestsMetric;
  onChange: (m: RequestsMetric) => void;
}): React.ReactElement {
  return (
    <div className="flex items-center gap-0.5 rounded-md border border-border p-0.5">
      {(['requests', 'tokens', 'failed'] as const).map((m) => (
        <button
          key={m}
          type="button"
          data-slot={`requests-metric-${m}`}
          aria-pressed={metric === m}
          onClick={() => onChange(m)}
          className={`cursor-pointer rounded px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wider transition-colors ${
            metric === m
              ? 'bg-primary/15 text-primary'
              : 'text-text-secondary hover:text-text-primary'
          }`}
        >
          {m}
        </button>
      ))}
    </div>
  );
}

export interface RequestsChartProps {
  series: StatsSeriesPoint[] | null | undefined;
  loading?: boolean;
  /** Controlled metric. When omitted the chart owns its own state and renders an
   *  inline toggle above the chart (self-contained mode). */
  metric?: RequestsMetric;
}

export function RequestsChart({
  series,
  loading,
  metric: controlledMetric,
}: RequestsChartProps): React.ReactElement {
  const [internalMetric, setInternalMetric] =
    React.useState<RequestsMetric>('requests');
  const isControlled = controlledMetric !== undefined;
  const metric = isControlled ? controlledMetric : internalMetric;

  if (loading) {
    return <Skeleton variant="chart" />;
  }

  const rows = seriesToRecharts(series);
  // Empty when there are no rows OR the window is all-zero (server zero-fills an
  // inactive range) — otherwise Recharts paints a blank axis grid.
  if (rows.length === 0 || !seriesHasActivity(series)) {
    return <ChartEmpty />;
  }

  const fmt = metric === 'tokens' ? abbreviate : formatInt;

  const chart = (
    <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
      <BarChart data={rows}>
        <CartesianGrid stroke={AXIS_LINE_STROKE} strokeDasharray="3 3" vertical={false} />
        <XAxis
          dataKey="date"
          tickFormatter={formatDate}
          tick={AXIS_TICK}
          axisLine={{ stroke: AXIS_LINE_STROKE }}
          tickLine={{ stroke: AXIS_LINE_STROKE }}
          minTickGap={24}
          interval="preserveStartEnd"
        />
        <YAxis
          width={64}
          tickFormatter={fmt}
          tick={AXIS_TICK}
          axisLine={{ stroke: AXIS_LINE_STROKE }}
          tickLine={{ stroke: AXIS_LINE_STROKE }}
        />
        <Tooltip
          contentStyle={TOOLTIP_CONTENT_STYLE}
          labelStyle={TOOLTIP_LABEL_STYLE}
          itemStyle={TOOLTIP_ITEM_STYLE}
          labelFormatter={tooltipLabelFormatter}
          formatter={makeTooltipFormatter(fmt)}
          cursor={{ fill: 'var(--border)', opacity: 0.3 }}
        />
        {/* Flat children — Recharts scans its DIRECT children for <Bar> and does
            NOT flatten a React Fragment, so a <>…</> wrapper makes the stacked
            bars vanish. Keep each Bar as its own conditional child. */}
        {metric === 'requests' && (
          <Bar key="success" dataKey="success" stackId="req" fill={SERIES_COLOR} />
        )}
        {metric === 'requests' && (
          <Bar
            key="failed"
            dataKey="failed"
            stackId="req"
            fill="var(--destructive)"
            radius={[3, 3, 0, 0]}
          />
        )}
        {metric === 'tokens' && (
          <Bar key="in" name="input" dataKey="inputTokens" stackId="tok" fill={SERIES_COLOR} />
        )}
        {metric === 'tokens' && (
          <Bar
            key="out"
            name="output"
            dataKey="outputTokens"
            stackId="tok"
            fill="var(--cat-2)"
            radius={[3, 3, 0, 0]}
          />
        )}
        {metric === 'failed' && (
          <Bar key="failed-only" dataKey="failed" fill="var(--destructive)" radius={[3, 3, 0, 0]} />
        )}
      </BarChart>
    </ResponsiveContainer>
  );

  // Self-contained mode (no controlled metric): render the inline toggle above
  // the chart, exactly as before.
  if (!isControlled) {
    return (
      <div className="flex flex-col">
        <div className="mb-2 flex justify-end">
          <RequestsMetricToggle metric={metric} onChange={setInternalMetric} />
        </div>
        {chart}
      </div>
    );
  }

  return chart;
}
