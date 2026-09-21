// stats-series.ts — pure mapper from the Phase-12 stats series contract to
// Recharts-ready ROW OBJECTS.
//
// The old uPlot wrapper (src/ui/charts.js::seriesToUplot) mapped the series to
// uPlot's COLUMNAR shape `[xs[], ys[]]` (with xs as UTC unix seconds). Recharts
// consumes ROW OBJECTS instead — one `{ date, spend, requests }` per in-window
// day — and reads the `date` STRING directly off the category XAxis (a
// tickFormatter formats it later), so this transform does NOT convert dates to
// unix seconds.
//
// PURE: no DOM, no chart-lib reference, no input mutation, deterministic — so it
// runs in the node env (stats-series.test.ts), mirroring format.ts / charts.js.
import type { StatsSeriesPoint } from './api-types';

/**
 * One Recharts row. `date` is the YYYY-MM-DD string used as the XAxis dataKey
 * (Recharts' category axis can't take null); `spend`/`requests` are the numeric
 * values (a real 0 is preserved).
 */
export interface SpendRequestsPoint {
  date: string;
  spend: number;
  requests: number;
  tokens: number;
  inputTokens: number;
  outputTokens: number;
  failed: number;
  success: number;
}

// Coerce to a finite number, or 0. Mirrors seriesToUplot's defensive guard
// (`typeof v === "number" ? v : ...`): the backend type guarantees a number, but
// a non-number coerces to 0 rather than emitting NaN into the chart. A REAL zero
// stays 0 (it is a finite number, never dropped).
function asNumber(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

/**
 * Map the Phase-12 series contract to Recharts row objects, in input order.
 *
 * An empty / null / non-array series returns the no-data shape `[]` so the chart
 * renders its empty state instead of an empty grid (mirrors seriesToUplot's
 * `[[],[]]` guard). Each point maps to `{ date, spend, requests }`: `date` is the
 * point's date string or `''` when null/undefined.
 */
/**
 * True when the window has ANY request activity. The server ZERO-FILLS the
 * series (an inactive range comes back as full-length all-zero points, not an
 * empty array), so `seriesToRecharts(...).length > 0` is NOT enough to decide a
 * chart has something to draw — an all-zero series still renders a blank axis
 * grid. Charts gate their empty state on this instead. `requests` is the master
 * activity signal: zero requests ⇒ zero spend / tokens / failed too.
 */
export function seriesHasActivity(
  series: StatsSeriesPoint[] | null | undefined,
): boolean {
  return Array.isArray(series) && series.some((p) => asNumber(p?.requests) > 0);
}

export function seriesToRecharts(
  series: StatsSeriesPoint[] | null | undefined,
): SpendRequestsPoint[] {
  if (!Array.isArray(series) || series.length === 0) {
    return [];
  }
  const rows = new Array<SpendRequestsPoint>(series.length);
  for (let i = 0; i < series.length; i++) {
    const point = series[i] ?? ({} as StatsSeriesPoint);
    const requests = asNumber(point.requests);
    const failed = Math.min(requests, asNumber(point.failed));
    rows[i] = {
      date: point.date ?? '',
      spend: asNumber(point.spend),
      requests,
      tokens: asNumber(point.tokens),
      inputTokens: asNumber(point.input_tokens),
      outputTokens: asNumber(point.output_tokens),
      failed,
      // Stacked-bar split: success + failed always re-sums to requests.
      success: Math.max(0, requests - failed),
    };
  }
  return rows;
}
