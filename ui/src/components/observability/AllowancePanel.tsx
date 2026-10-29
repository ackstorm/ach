// AllowancePanel.tsx — the enforced allowance (the caller's user:<email> tag)
// over its own cycle: cumulative spend from cycle start, the cap, and the
// backend's linear projection to reset_at (budget.projection), all from the
// same tag read (budget.cycle_series sums to budget.current). The cycle is
// independent of the page's activity period; a missing cap is "no limit",
// never a zero allowance.
import type { BudgetProjection, StatsBudget } from '@/lib/api-types';
import { formatCurrency, formatCurrencyCompact, formatLocalDate, formatShortDay } from '@/lib/format';
import { ChartFrame, type Tip } from '@/components/charts/ChartFrame';

const DAY = 86_400_000;
const isoDay = (ms: number) => new Date(ms).toISOString().slice(0, 10);

// A backend without the projection field reads as unavailable, never as $0.
export const NO_PROJECTION: BudgetProjection = { basis: 'linear_cycle_rate', cycle_start: null, horizon_end: null, spend: null, pct: null, reason: null };

export function projectionReason(p: BudgetProjection): string {
  if (p.reason === 'too_early') return 'Available after the first day of the cycle';
  if (p.reason === 'no_cycle') return 'No resetting budget cycle';
  if (p.reason === 'spend_unknown') return 'Spend unavailable';
  return 'Projection unavailable';
}

export function AllowancePanel({ budget }: { budget: StatsBudget }) {
  const p = budget.projection ?? NO_PROJECTION;
  const start = p.cycle_start ? Date.parse(p.cycle_start) : NaN;
  const end = p.horizon_end ? Date.parse(p.horizon_end) : NaN;
  const hasCycle = Number.isFinite(start) && Number.isFinite(end) && end > start;
  // Daily buckets are whole UTC days; the cycle's first day is the bucket the
  // backend counts from (nearest UTC midnight).
  const first = hasCycle ? isoDay(Math.round(start / DAY) * DAY) : '';
  const today = isoDay(Date.now());
  const cap = budget.max_budget;
  // null = the cycle spend could not be read: shown as unknown, never $0.
  const spent = budget.current;
  const remaining = cap == null || spent == null ? null : cap - spent;
  if (budget.source === 'unknown') {
    return <section className="panel forecast forecast-compact"><div className="forecast-head"><div><h2>Allowance</h2><p>Budget data is unavailable.</p></div></div></section>;
  }
  const meta = <div className="forecast-meta">
    <div><span>Spent this cycle</span><b>{spent == null ? 'Unavailable' : formatCurrency(spent)}</b></div>
    <div><span>Remaining</span><b>{cap == null ? 'No limit' : remaining == null ? 'Unavailable' : formatCurrency(remaining)}</b></div>
    <div><span>{p.horizon_end ? `Projected by ${formatLocalDate(p.horizon_end)}` : 'Projection'}</span><b>{p.spend == null ? '—' : formatCurrency(p.spend)}</b></div>
  </div>;
  if (!hasCycle || spent == null) {
    return <section className="panel forecast forecast-compact"><div className="forecast-head"><div><h2>Allowance</h2><p>{cap == null ? 'No limit configured' : `${formatCurrency(cap)} cap`} · {projectionReason(p)}</p></div>{meta}</div></section>;
  }
  const days = Math.max(1, Math.round((end - start) / DAY));
  const bySpend = new Map((budget.cycle_series ?? []).map((pt) => [pt.date, pt.spend]));
  const elapsed = Math.max(0, Math.min(days - 1, Math.round((Date.parse(`${today}T00:00:00Z`) - Date.parse(`${first}T00:00:00Z`)) / DAY)));
  let running = 0;
  const cumulative = Array.from({ length: elapsed + 1 }, (_, i) => running += bySpend.get(isoDay(Date.parse(`${first}T00:00:00Z`) + i * DAY)) ?? 0);
  const ceiling = Math.max(1, running, spent, cap ?? 0, p.spend ?? 0);
  // Past days read the cumulative line; later days the drawn straight projection.
  const projected = (i: number) => p.spend == null || i <= elapsed ? null : spent + (Math.max(p.spend, spent) - spent) * (i - elapsed) / Math.max(1, days - 1 - elapsed);
  const tip = (i: number): Tip => {
    const est = projected(i);
    return { title: formatShortDay(isoDay(Date.parse(`${first}T00:00:00Z`) + i * DAY)), rows: [
      ...(i <= elapsed ? [{ label: 'Cumulative spend', value: formatCurrency(cumulative[i]), color: 'var(--green)', y: cumulative[i] }] : []),
      ...(est != null ? [{ label: 'Projected', value: formatCurrency(est), color: 'var(--green)', y: est }] : []),
      ...(cap != null ? [{ label: 'Allowance cap', value: formatCurrency(cap), color: 'var(--red)' }] : []),
    ] };
  };
  const label = `Allowance cycle spend ${formatCurrency(spent)}${p.spend != null ? `, projected ${formatCurrency(p.spend)} by ${formatLocalDate(p.horizon_end)}` : ''}${cap != null ? ` against a ${formatCurrency(cap)} cap` : ''}`;
  return <section className="panel forecast"><div className="forecast-head"><div><h2>Allowance cycle spend</h2><p>{formatLocalDate(p.cycle_start)} – {formatLocalDate(p.horizon_end)} · {p.spend == null ? projectionReason(p) : 'straight-line estimate from this cycle’s rate'}</p></div>{meta}</div>
    {!budget.cycle_series ? <p className="report-notice">Allowance cycle spend could not be loaded.</p> :
      <div className="runway-chart" role="img" aria-label={label}>
        <ChartFrame max={ceiling} format={formatCurrencyCompact} xLabels={[formatShortDay(first), formatShortDay(isoDay(end))]} hover={{ n: days, at: (i) => i / Math.max(1, days - 1), tip }}>{(f) => {
          const x = (i: number) => f.x(i / Math.max(1, days - 1));
          const line = cumulative.map((v, i) => `${i ? 'L' : 'M'}${x(i)} ${f.y(v)}`).join(' ');
          return <>
            {elapsed > 0 && <path className="actual-fill" d={`${line} L${x(elapsed)} ${f.bottom} L${f.left} ${f.bottom} Z`} />}<path className="actual-line" d={line} />{elapsed === 0 && <circle cx={x(0)} cy={f.y(cumulative[0])} r="4" fill="var(--green)" />}
            <line className="cycle-today" x1={x(elapsed)} x2={x(elapsed)} y1="25" y2={f.bottom} /><text className="chart-label" x={x(elapsed)} y="18" textAnchor={elapsed === 0 ? 'start' : 'middle'} dx={elapsed === 0 ? 4 : 0}>Today</text>
            {cap != null && <line className="cycle-cap" x1={f.left} x2={f.right} y1={f.y(cap)} y2={f.y(cap)} />}
            {p.spend != null && <line className="cycle-projection" x1={x(elapsed)} y1={f.y(spent)} x2={x(days - 1)} y2={f.y(Math.max(p.spend, spent))} />}
          </>;
        }}</ChartFrame>
      </div>}
    <div className="legend"><span><i style={{ background: 'var(--green)' }} />Cumulative spend</span>{cap != null && <span><i style={{ background: 'var(--red)' }} />Allowance cap</span>}{p.spend != null && <span><i style={{ background: 'var(--green)', opacity: 0.5 }} />Projection to reset</span>}</div>
  </section>;
}
