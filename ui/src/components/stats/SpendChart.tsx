// SpendChart.tsx — STATS-03 Daily Spend, an area chart (Recharts port of the
// old uPlot SpendChart in src/ui/charts.js).
//
// Mirrors the old intent: an accent (green) line with a --primary->transparent
// glow fill, --border gridlines, muted ticks, a dark themed hover tooltip
// formatted with formatCurrency, ~240px tall. Themed entirely via var(--*)
// tokens — NEVER a raw hex.
//
// Render order (shared with RequestsChart):
//   1. loading        -> <Skeleton variant="chart" />
//   2. no rows         -> <ChartEmpty /> (the locked empty state, not a grid)
//   3. otherwise       -> the AreaChart in a ResponsiveContainer.

import * as React from 'react';
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';

import type { StatsSeriesPoint } from '@/lib/api-types';
import { formatCurrency, formatDate } from '@/lib/format';
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

export interface SpendChartProps {
  series: StatsSeriesPoint[] | null | undefined;
  loading?: boolean;
}

export function SpendChart({ series, loading }: SpendChartProps): React.ReactElement {
  if (loading) {
    return <Skeleton variant="chart" />;
  }

  const rows = seriesToRecharts(series);
  // Empty when there are no rows OR the window is all-zero (server zero-fills an
  // inactive range) — otherwise Recharts paints a blank axis grid.
  if (rows.length === 0 || !seriesHasActivity(series)) {
    return <ChartEmpty />;
  }

  return (
    <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
      <AreaChart data={rows}>
        <defs>
          <linearGradient id="spendFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={SERIES_COLOR} stopOpacity={0.35} />
            <stop offset="100%" stopColor={SERIES_COLOR} stopOpacity={0} />
          </linearGradient>
        </defs>
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
          tickFormatter={formatCurrency}
          tick={AXIS_TICK}
          axisLine={{ stroke: AXIS_LINE_STROKE }}
          tickLine={{ stroke: AXIS_LINE_STROKE }}
        />
        <Tooltip
          contentStyle={TOOLTIP_CONTENT_STYLE}
          labelStyle={TOOLTIP_LABEL_STYLE}
          itemStyle={TOOLTIP_ITEM_STYLE}
          labelFormatter={tooltipLabelFormatter}
          formatter={makeTooltipFormatter(formatCurrency)}
        />
        <Area
          type="monotone"
          dataKey="spend"
          stroke={SERIES_COLOR}
          strokeWidth={2}
          fill="url(#spendFill)"
          dot={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}
