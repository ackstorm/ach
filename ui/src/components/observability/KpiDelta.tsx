// KpiDelta.tsx — a KPI card's change against the previous period.
import { formatPercent } from '@/lib/format';
import type { DayRange } from '@/stores/console-range';

// ▲ up / ▼ down against the previous period of the same length, on its own
// line; with `bad` (spend, latency) rising is red and falling green.
export function Delta({ pct, bad = false }: { pct: number | null | undefined; bad?: boolean }) {
  if (pct == null) return null;
  const up = pct > 0;
  return <div className="delta"><span className={bad ? (up ? 'chg up' : 'chg down') : undefined}>{up ? '▲' : '▼'} {formatPercent(Math.abs(pct))}</span> vs previous period</div>;
}

// The period of the same length that ends the day before `range` starts.
const DAY = 86_400_000;
export function previousRange(range: DayRange): DayRange {
  const start = Date.parse(`${range.start}T00:00:00Z`);
  const days = Math.round((Date.parse(`${range.end}T00:00:00Z`) - start) / DAY) + 1;
  const iso = (ms: number) => new Date(ms).toISOString().slice(0, 10);
  return { start: iso(start - days * DAY), end: iso(start - DAY) };
}
