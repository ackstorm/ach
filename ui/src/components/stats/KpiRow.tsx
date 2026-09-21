// KpiRow.tsx — the 4-card KPI row for the #/stats Usage & Spend page (STATS-02).
//
// PURE presentational leaf — no fetch, no data ownership. The container passes
// the `totals` slice; this renders the four headline metrics plus a
// period-over-period delta chip per card (from totals.deltas; a null pct —
// degraded prior window, D-08 — renders no chip). Direction coloring is
// semantic: requests/tokens up = good (primary), spend/avg-cost up = bad
// (destructive) via the `invert` flag.

import { BarChart3, Coins, DollarSign, Gauge } from 'lucide-react';
import * as React from 'react';

import type { StatsSeriesPoint, StatsTotals } from '@/lib/api-types';
import { abbreviate, formatCurrency, formatInt } from '@/lib/format';

// ── Sparkline ────────────────────────────────────────────────────────────────
// A tiny inline-SVG area+line trend rendered faintly behind a KPI card's figure
// (the "transparent graphic" on the tiles). Deliberately NOT Recharts — a 96×32
// area chart is ~15 lines of SVG and mounting a chart lib per card is wasteful.
// Theme-token colored (stroke/fill primary), so it follows dark/light/pastel/red.
// Returns null for <2 points or an all-flat series (nothing to show). Purely
// decorative → aria-hidden.
function Sparkline({ data, className }: { data: number[]; className?: string }) {
  if (data.length < 2) return null;
  const max = Math.max(...data);
  const min = Math.min(...data);
  const range = max - min;
  if (range === 0) return null; // flat line carries no signal
  const W = 96;
  const H = 32;
  const pts = data.map((v, i) => {
    const x = (i / (data.length - 1)) * W;
    // 2px top/bottom inset so the peak/trough strokes aren't clipped.
    const y = H - 2 - ((v - min) / range) * (H - 4);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const line = pts.join(' ');
  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      className={className}
      aria-hidden="true"
    >
      <polygon points={`0,${H} ${line} ${W},${H}`} className="fill-primary/10" />
      <polyline
        points={line}
        className="fill-none stroke-primary"
        strokeWidth={1.5}
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}

// Signed one-decimal percent: 0.182 -> "+18.2%", -0.05 -> "-5.0%".
function formatSignedPct(fraction: number): string {
  const pct = (fraction * 100).toFixed(1);
  return fraction >= 0 ? `+${pct}%` : `${pct}%`;
}

// The per-card delta chip. Two states:
//   • finite pct -> signed percent + direction arrow/color;
//   • otherwise  -> nothing. When there is no prior baseline to compare against
//                   (prior window was 0/absent) there is no real trend, so we
//                   render NO chip rather than a confusing "▲ 100% (no info)".
function DeltaChip({
  pct,
  invert,
}: {
  pct: number | null | undefined;
  invert?: boolean;
}): React.ReactElement | null {
  const hasPct = typeof pct === 'number' && Number.isFinite(pct);
  if (!hasPct) return null;

  const value = pct as number;
  const up = value > 0;
  const flat = value === 0;
  const good = flat ? null : invert ? !up : up;
  const color = flat
    ? 'text-text-secondary'
    : good
      ? 'text-primary'
      : 'text-destructive';
  const arrow = flat ? '' : up ? '▲ ' : '▼ ';
  return (
    <div data-slot="kpi-delta" className={`relative font-mono text-[11px] ${color}`}>
      {arrow}
      {formatSignedPct(value)}{' '}
      <span className="text-text-tertiary">vs prev</span>
    </div>
  );
}

// One of the four cards, all with the SAME three-part layout: an 11px caption
// label, the 24px value with its optional detail inline beside it (in muted
// parens, e.g. "5 failed · 6.5%"), and the period-over-period delta chip on its
// own line below. Uniform across the four cards so the detail rows and the chip
// rows line up column-for-column. `sub` is inline content (a <span>) nested in
// the parens; keep it short enough to sit beside the value without wrapping.
function KpiCard({
  label,
  labelTitle,
  value,
  deltaPct,
  invert,
  sub,
  spark,
  icon: Icon,
}: {
  label: string;
  labelTitle?: string;
  value: string;
  deltaPct: number | null | undefined;
  invert?: boolean;
  /** Short inline note next to the value, in muted parens (e.g. "5 failed · 6.5%"). */
  sub?: React.ReactNode;
  /** Per-day series for the faint background sparkline (requests/tokens/spend). */
  spark?: number[];
  icon: typeof BarChart3;
}): React.ReactElement {
  // DeltaChip holds no hooks, so call it directly to render the chip below.
  const chip = DeltaChip({ pct: deltaPct, invert });
  const hasSub = sub != null && sub !== false;

  return (
    <div className="relative flex flex-col gap-2 overflow-hidden rounded-xl border border-border bg-surface p-5">
      {spark && spark.length > 1 ? (
        // Full-bleed trend anchored to the card's bottom, sitting BEHIND the
        // content (each content row carries `relative` to stack above it).
        // Softened so the figures stay legible; overflow-hidden clips it round.
        <div
          data-slot="kpi-spark"
          className="pointer-events-none absolute inset-x-0 bottom-0 h-1/2 opacity-25"
          aria-hidden="true"
        >
          <Sparkline data={spark} className="h-full w-full" />
        </div>
      ) : null}
      {/* Icon chip + label row — mirrors the Dashboard MetricTile so the two
          pages' KPI tiles read as the same component. */}
      <div className="relative flex items-center gap-2">
        <span className="inline-flex size-[26px] shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Icon className="size-[15px]" aria-hidden="true" />
        </span>
        <div
          data-testid={label === 'SPEND' ? 'kpi-spend-label' : undefined}
          title={labelTitle}
          className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary"
        >
          {label}
        </div>
      </div>
      <div className="relative flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <span className="break-words font-sans text-2xl font-semibold leading-tight text-text-primary">
          {value}
        </span>
        {hasSub ? (
          <span className="font-mono text-[10px] text-text-tertiary">({sub})</span>
        ) : null}
      </div>
      {chip}
    </div>
  );
}

export interface KpiRowProps {
  /** The `totals` slice (requests/tokens/spend/avg + deltas). */
  totals: StatsTotals | null | undefined;
  /**
   * Per-day `series` slice. When given, the flow cards (requests/tokens/spend)
   * render a faint background sparkline from it. Omitted → no sparklines.
   */
  series?: StatsSeriesPoint[] | null;
  /**
   * Optional node rendered in the 4th grid cell INSTEAD of the AVG COST card.
   * The KEYS tab (Dashboard) reuses this row for its richer header but swaps the
   * avg-cost card for a combined keys+teams tile. Omitted on the Stats page, so
   * the 4th card stays AVG COST there.
   */
  fourthCard?: React.ReactNode;
}

// Renders four cards (TOTAL REQUESTS / TOTAL TOKENS / SPEND / AVG COST / 1M
// TOKENS) in a 4->2->1 responsive grid. When `fourthCard` is given, cards 1-3
// come from `totals` and the 4th cell renders that node instead of AVG COST.
export function KpiRow({
  totals,
  series,
  fourthCard,
}: KpiRowProps): React.ReactElement {
  const t = totals ?? null;
  const d = t?.deltas ?? null;
  // Per-day spark arrays for the flow cards (empty → Sparkline no-ops).
  const pts = series ?? [];
  const reqSpark = pts.map((p) => p.requests);
  const tokSpark = pts.map((p) => p.tokens);
  const spendSpark = pts.map((p) => p.spend);

  const cards = [
    {
      label: 'TOTAL REQUESTS',
      icon: BarChart3,
      value: abbreviate(t?.requests),
      deltaPct: d?.requests_pct,
      invert: false,
      spark: reqSpark,
      sub:
        typeof t?.failed_requests === 'number' && t.failed_requests > 0 ? (
          <span data-slot="kpi-failed" className="text-destructive">
            {formatInt(t.failed_requests)} failed
            {t.requests > 0
              ? ` · ${((t.failed_requests / t.requests) * 100).toFixed(1)}%`
              : ''}
          </span>
        ) : null,
    },
    {
      label: 'TOTAL TOKENS',
      icon: Coins,
      value: abbreviate(t?.tokens),
      deltaPct: d?.tokens_pct,
      invert: false,
      spark: tokSpark,
      // The headline total already shows the full token count, so the inline sub
      // adds the OUTPUT split and its share of the total: "6.8M out · 22%". Short
      // enough to sit beside the value without wrapping; the title spells it out.
      sub: ((): React.ReactNode => {
        const out =
          typeof t?.output_tokens === 'number' ? t.output_tokens : null;
        if (out === null || out <= 0) return null;
        const total = typeof t?.tokens === 'number' ? t.tokens : null;
        const pct =
          total && total > 0 ? ` · ${((out / total) * 100).toFixed(1)}%` : '';
        return (
          <span data-slot="kpi-tokens" title="Output tokens · share of total tokens">
            {abbreviate(out)} out{pct}
          </span>
        );
      })(),
    },
    {
      label: 'SPEND',
      icon: DollarSign,
      value: formatCurrency(t?.spend),
      labelTitle: 'Gateway-computed spend — includes cache & provider pricing',
      deltaPct: d?.spend_pct,
      invert: true,
      spark: spendSpark,
    },
    {
      label: 'AVG COST / 1M TOKENS',
      icon: Gauge,
      value: formatCurrency(t?.avg_cost_per_1m_tokens),
      deltaPct: d?.avg_cost_per_1m_tokens_pct,
      invert: true,
    },
  ];

  // With a custom 4th tile, drop the AVG COST card and render the node instead.
  const shown = fourthCard ? cards.slice(0, 3) : cards;

  return (
    <div data-slot="kpi-row" className="flex flex-col gap-2">
      <div className="grid grid-cols-4 gap-3 max-[880px]:grid-cols-2 max-[520px]:grid-cols-1">
        {shown.map((c) => (
          <KpiCard
            key={c.label}
            label={c.label}
            labelTitle={'labelTitle' in c ? c.labelTitle : undefined}
            icon={c.icon}
            value={c.value}
            deltaPct={c.deltaPct}
            invert={c.invert}
            sub={'sub' in c ? c.sub : undefined}
            spark={'spark' in c ? c.spark : undefined}
          />
        ))}
        {fourthCard}
      </div>
    </div>
  );
}
