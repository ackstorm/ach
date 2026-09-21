// UsageDonut.tsx — the Usage-by-Model SPEND donut for the #/stats Usage & Spend
// page (STATS-05). React + Tailwind + Recharts port of the old hand-rolled
// inline-SVG donut src/ui/stats-donut.js.
//
// The old leaf hand-rolled an inline-SVG ring because uPlot had no pie/donut
// (CONTEXT D-01). ui-next ships Recharts, so this renders a real <PieChart>/<Pie>
// donut. PARITY with stats-donut.js: the figure shows Usage-by-Model by SPEND —
// the Top-5 named slices by spend desc + one neutral `Other` aggregate, with
// `formatCurrency(totalSpend)` centered (24px heading) under a `TOTAL` caption,
// and a legend of model · spend · spend%. Colors step a green token opacity
// ramp; the `Other` slice is forced to a neutral token so it never competes.
//
// PURE presentational leaf — no fetch, no data ownership. The container (the
// old src/ui/stats.js) passes the Phase-12 `models` slice, the `totalSpend`, and
// the `capabilities` map. The render branch is decided by
// capabilityRenderMode(capabilities, 'per_model_spend', models.length > 0):
//   "coming-soon" -> the source's `Coming soon` copy + em-dash where the total sits.
//   "empty"       -> the source's `No usage in this range`.
//   "ready"       -> the donut + legend.
// (`per_model_spend` is not a declared StatsCapabilities key, so it is never
//  explicitly false today and resolves to ready/empty — but the coming-soon
//  branch is kept, faithful to the source.)
//
// SECURITY (T-13-05, XSS — MITIGATED): all labels render as React text children
// (auto-escaped). No dangerouslySetInnerHTML. Numbers pass through format.ts.
// SECURITY (T-13-07, DoS / divide-by-zero — MITIGATED): the donut renders only
// when there is real data; an all-zero models list falls to the "empty" branch.

import * as React from 'react';
import { Cell, Pie, PieChart, ResponsiveContainer } from 'recharts';

import type { StatsCapabilities, StatsModelRow } from '@/lib/api-types';
import { formatCurrency } from '@/lib/format';
import { capabilityRenderMode } from '@/lib/stats-presets';

// The em-dash placeholder (matches format.ts EM_DASH).
const EM_DASH = '—';

// Locked copy, lifted verbatim from stats-donut.js (13-UI-SPEC §Copywriting).
const OTHER_LABEL = 'Other';
const TOTAL_CAPTION = 'TOTAL';
const COMING_SOON_COPY = 'Coming soon';
const EMPTY_COPY = 'No usage in this range';

// Donut geometry — a fixed-height figure mirroring the old 160px ring.
const DONUT_HEIGHT = 200;
const INNER_RADIUS = 56;
const OUTER_RADIUS = 80;

// Slice colors: the Top-5 models read as DISTINCT hues instead of a single-green
// opacity ramp (4 near-identical greens were indistinguishable). Slice 1 is
// var(--primary) so the dominant model tracks the active skin (the categorical
// --cat-1 is a fixed green and clashed on the non-green skins, e.g. the red one);
// the remaining slices step the categorical palette. NEVER a raw hex — the tokens
// live in index.css. The trailing `Other` bucket stays neutral var(--text-tertiary)
// so it never competes.
const SLICE_FILLS = [
  'var(--primary)',
  'var(--cat-2)',
  'var(--cat-3)',
  'var(--cat-4)',
  'var(--cat-5)',
];
const OTHER_FILL = 'var(--text-tertiary)';

/** One ranked donut slice. */
interface Slice {
  model: string;
  spend: number;
  spend_pct: number | null;
  isOther: boolean;
}

// Format a contract spend_pct fraction (e.g. 0.182) as a one-decimal `%`; a
// null/non-finite fraction -> em-dash (never `0%` / `NaN`). Ported verbatim from
// stats-donut.js formatPct.
function formatPct(pct: number | null | undefined): string {
  if (pct === null || pct === undefined || !Number.isFinite(pct)) return EM_DASH;
  return `${(pct * 100).toFixed(1)}%`;
}

// rankSlices(models) -> [{model, spend, spend_pct, isOther}] of at most 6
// entries: the Top-5 named slices by spend desc + a single summed `Other`.
// Pure: no DOM, no mutation of the input array. Ported from stats-donut.js.
export function rankSlices(
  models: StatsModelRow[] | null | undefined,
): Slice[] {
  const list = Array.isArray(models) ? models.slice() : [];
  list.sort((a, b) => (b && b.spend ? b.spend : 0) - (a && a.spend ? a.spend : 0));

  const top: Slice[] = list.slice(0, 5).map((m) => ({
    model: m && m.model ? m.model : EM_DASH,
    spend: m && typeof m.spend === 'number' ? m.spend : 0,
    spend_pct: m && typeof m.spend_pct === 'number' ? m.spend_pct : null,
    isOther: false,
  }));

  const rest = list.slice(5);
  if (rest.length > 0) {
    let otherSpend = 0;
    let otherPct = 0;
    let anyPct = false;
    for (const m of rest) {
      if (m && typeof m.spend === 'number') otherSpend += m.spend;
      if (m && typeof m.spend_pct === 'number') {
        otherPct += m.spend_pct;
        anyPct = true;
      }
    }
    top.push({
      model: OTHER_LABEL,
      spend: otherSpend,
      spend_pct: anyPct ? otherPct : null,
      isOther: true,
    });
  }

  return top;
}

// displayNames(slices) -> the legend display string per slice. When EVERY named
// (non-Other) slice shares the same `provider/` prefix (e.g. "gemini/..."), the
// prefix is stripped — the legend column is narrow and a constant prefix is pure
// noise. The FULL name is preserved in the legend item's `title` tooltip. Pure.
export function displayNames(slices: Slice[]): string[] {
  const named = slices.filter((s) => !s.isOther);
  let prefix: string | null = null;
  for (const s of named) {
    const i = s.model.indexOf('/');
    const p = i > 0 ? s.model.slice(0, i + 1) : null;
    if (p === null) {
      prefix = null;
      break;
    }
    if (prefix === null) prefix = p;
    else if (prefix !== p) {
      prefix = null;
      break;
    }
  }
  return slices.map((s) =>
    !s.isOther && prefix ? s.model.slice(prefix.length) : s.model
  );
}

// The fill token for a slice: the neutral token for `Other`, else the categorical
// hue at this rank (clamped to the last past index 4).
function sliceFill(slice: Slice, index: number): string {
  if (slice.isOther) return OTHER_FILL;
  return SLICE_FILLS[index] ?? SLICE_FILLS[SLICE_FILLS.length - 1];
}

export interface UsageDonutProps {
  /** The Phase-12 per-model rows — the source of the spend split. */
  models: StatsModelRow[] | null | undefined;
  /** The window total spend, centered under the TOTAL caption. */
  totalSpend: number | null;
  /** The contract `capabilities` map (gate: `per_model_spend`). */
  capabilities: StatsCapabilities | null | undefined;
}

// A small state panel (coming-soon / empty) matching the old usage-donut--state
// chrome: a centered, min-height surface card.
function DonutStatePanel({
  children,
  showTotalCap,
}: {
  children: React.ReactNode;
  showTotalCap?: boolean;
}): React.ReactElement {
  return (
    <div
      data-slot="usage-donut-state"
      className="flex min-h-40 flex-col items-center justify-center gap-2 rounded-xl border border-border bg-surface p-5 text-center"
    >
      {showTotalCap ? (
        <>
          <div className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
            {TOTAL_CAPTION}
          </div>
          <div className="font-sans text-2xl font-semibold leading-tight text-primary">
            {EM_DASH}
          </div>
        </>
      ) : null}
      <div className="font-sans text-sm text-text-secondary">{children}</div>
    </div>
  );
}

export function UsageDonut({
  models,
  totalSpend,
  capabilities,
}: UsageDonutProps): React.ReactElement {
  const hasData = (models?.length ?? 0) > 0;
  // StatsCapabilities is a fixed boolean record; capabilityRenderMode wants the
  // open Record<string, boolean> shape, so we read it through that view.
  const mode = capabilityRenderMode(
    capabilities as Record<string, boolean> | null | undefined,
    'per_model_spend',
    hasData
  );

  if (mode === 'coming-soon') {
    return (
      <DonutStatePanel showTotalCap>{COMING_SOON_COPY}</DonutStatePanel>
    );
  }

  if (mode === 'empty') {
    return <DonutStatePanel>{EMPTY_COPY}</DonutStatePanel>;
  }

  const slices = rankSlices(models);
  const names = displayNames(slices);
  // total = totalSpend when a number, else the sum of slice spends.
  const total =
    typeof totalSpend === 'number'
      ? totalSpend
      : slices.reduce((acc, s) => acc + s.spend, 0);

  return (
    <div
      data-slot="usage-donut"
      className="flex flex-wrap items-center gap-8 rounded-xl border border-border bg-surface p-5"
    >
      <div className="relative h-50 w-50 shrink-0">
        <ResponsiveContainer width="100%" height={DONUT_HEIGHT}>
          <PieChart>
            <Pie
              data={slices}
              dataKey="spend"
              nameKey="model"
              cx="50%"
              cy="50%"
              innerRadius={INNER_RADIUS}
              outerRadius={OUTER_RADIUS}
              startAngle={90}
              endAngle={-270}
              stroke="none"
              isAnimationActive={false}
            >
              {slices.map((s, i) => (
                <Cell key={`${s.model}-${i}`} fill={sliceFill(s, i)} />
              ))}
            </Pie>
          </PieChart>
        </ResponsiveContainer>
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-1">
          <div className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
            {TOTAL_CAPTION}
          </div>
          <div className="font-sans text-2xl font-semibold leading-tight text-primary">
            {formatCurrency(total)}
          </div>
        </div>
      </div>
      <ul
        data-slot="usage-donut-legend"
        className="flex flex-1 basis-50 flex-col gap-2"
      >
        {slices.map((s, i) => (
          <li
            key={`${s.model}-${i}`}
            title={s.model}
            className="grid grid-cols-[auto_1fr_auto_auto] items-center gap-2 rounded-lg px-2 py-1"
          >
            <span
              className="size-2.5 shrink-0 rounded-full"
              style={{ background: sliceFill(s, i) }}
              aria-hidden="true"
            />
            <span className="truncate font-mono text-xs text-text-primary">
              {names[i]}
            </span>
            <span className="font-mono text-xs text-text-primary">
              {formatCurrency(s.spend)}
            </span>
            <span className="font-mono text-xs text-text-secondary">
              {formatPct(s.spend_pct)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
