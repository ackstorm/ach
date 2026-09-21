// Stats.tsx — the #/stats Usage & Spend page CONTAINER (Task 4.6).
//
// React + TanStack Query + Tailwind rebuild of the old Preact container
// src/ui/stats.js (StatsView). It owns the date-range state, drives the
// re-query against GET /api/session/stats via useStats(range), and composes the
// Phase-4 leaves (KpiRow, SpendChart, RequestsChart, UsageDonut, TopKeys,
// ModelTable, BudgetPanel, DateRange) with per-panel Skeletons while loading and
// a whole-page error card on failure.
//
// DELIBERATE DIVERGENCES from src/ui/stats.js (the new no-sidebar architecture):
//   • NO left StatsRail — the AppShell topbar already provides Dashboard/Stats
//     navigation, so the old page-local rail is redundant (not built here).
//   • NO "Insights" Beta panel and NO coming-soon toast — the mock filler is
//     dropped (consistent with the no-sidebar dashboard).
//   • The page renders FULL WIDTH (AppShell already gives the Outlet the full
//     content width); there is no page-owned right <aside> rail.
//
// The data-status machine collapses to TanStack Query's `isPending` / `isError`
// (useStats throws on any non-200), so the old loadSeq ref guard + the 422-inline
// rangeError distinction are subsumed by the query: a superseding range refetch
// is handled by the queryKey, and useStats throws generically, so rangeError is
// passed as null to DateRange for now (the 422-inline distinction is deferred).

import { useState } from 'react';

import { BudgetPanel } from '@/components/stats/BudgetPanel';
import { DateRange } from '@/components/stats/DateRange';
import { formatDate } from '@/lib/format';
import { ErrorsDonut } from '@/components/stats/ErrorsDonut';
import { ExportCsvButton } from '@/components/stats/ExportCsvButton';
import { KpiRow } from '@/components/stats/KpiRow';
import { LatencyPanel } from '@/components/stats/LatencyPanel';
import { ModelTable } from '@/components/stats/ModelTable';
import {
  UsageTypeFilter,
  applyUsageTypeFilter,
  type UsageType,
} from '@/components/stats/UsageTypeFilter';
import {
  RequestsChart,
  RequestsMetricToggle,
  type RequestsMetric,
} from '@/components/stats/RequestsChart';
import { SpendChart } from '@/components/stats/SpendChart';
import { TopKeys } from '@/components/stats/TopKeys';
import { UsageDonut } from '@/components/stats/UsageDonut';
import { Skeleton } from '@/components/ui/skeleton';
import { TableSearch, matchesSearch } from '@/components/ui/table-search';
import { useKeys } from '@/hooks/use-keys';
import { useLatency } from '@/hooks/use-latency';
import type { StatsModelRow } from '@/lib/api-types';
import type { CsvColumn } from '@/lib/csv';
import { useStats } from '@/hooks/use-stats';
import { selectKeyRows } from '@/lib/keys';
import { presetToRange } from '@/lib/stats-presets';
import { mergeTopKeys } from '@/lib/top-keys';

// Locked copy (mirrors src/ui/stats.js §Copywriting Contract).
const PAGE_TITLE = 'Usage & Spend';
const PAGE_SUB = 'Requests, tokens, models, and spend for the selected period.';
const SECTION_DAILY_SPEND = 'DAILY SPEND';
const SECTION_REQUESTS = 'REQUESTS BY DAY';
const SECTION_USAGE_BY_MODEL = 'USAGE BY MODEL';
const SECTION_MODEL_BREAKDOWN = 'USAGE BREAKDOWN';
const SECTION_LATENCY = 'LATENCY';
const SECTION_OUTCOMES = 'REQUEST OUTCOMES';
const ERR_HEADING = "Couldn't load usage";
const ERR_BODY =
  "We couldn't reach the usage service. Check your connection and retry.";

// The default preset (default window — 7d).
const DEFAULT_PRESET = '7d';

// CSV column definitions for the MODEL BREAKDOWN export (Task 8). The `key`s must
// be real fields on the row type so the serializer reads them directly.
const MODELS_CSV_COLS = [
  { key: 'model', header: 'model' },
  { key: 'requests', header: 'requests' },
  { key: 'input_tokens', header: 'input_tokens' },
  { key: 'output_tokens', header: 'output_tokens' },
  { key: 'total_tokens', header: 'total_tokens' },
  { key: 'spend', header: 'spend' },
  { key: 'spend_pct', header: 'spend_pct' },
  { key: 'last_used', header: 'last_used' },
] satisfies CsvColumn<StatsModelRow>[];

// Shared 11px mono-caption section label (the UI-SPEC §Typography caption role).
const SECTION_LABEL_CLASS =
  'font-mono text-[11px] font-semibold uppercase tracking-widest text-text-secondary';

// A titled surface-card panel: the section label above its body (chart / table).
// An optional `action` is rendered on the label row, right-aligned — used to host
// per-panel controls (e.g. the REQUESTS/TOKENS toggle) in the otherwise-empty
// header space instead of stealing a row above the body.
function Panel({
  label,
  action,
  children,
}: {
  label: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="flex h-full min-w-0 flex-col rounded-xl border border-border bg-surface p-5">
      <div className="mb-3 flex min-h-6 items-center justify-between gap-3">
        <div className={SECTION_LABEL_CLASS}>{label}</div>
        {action}
      </div>
      {children}
    </div>
  );
}

export function Stats() {
  // Date-range state (mirrors src/ui/stats.js). `preset` drives the range via
  // presetToRange; `range` is the resolved {start,end} submitted to the server.
  const [preset, setPreset] = useState<string>(DEFAULT_PRESET);
  const [range, setRange] = useState<{ start: string; end: string }>(() =>
    presetToRange(DEFAULT_PRESET, new Date()),
  );
  // REQUESTS BY DAY metric, hoisted so the toggle lives in the panel header row.
  const [requestsMetric, setRequestsMetric] =
    useState<RequestsMetric>('requests');
  // USAGE BREAKDOWN client-side name filter.
  const [modelSearch, setModelSearch] = useState('');
  // USAGE BREAKDOWN row-type filter (ALL / MODEL / MCP TOOL).
  const [usageType, setUsageType] = useState<UsageType>('all');

  const query = useStats(range);
  // The user's full key list — merged into the TOP API KEYS panel so idle keys
  // (no activity in-window, hence absent from the stats contract) still appear.
  const keysQuery = useKeys();
  // Latency + request-outcomes — a SEPARATE endpoint (LiteLLM /spend/logs) over the
  // SAME date range as the rest of the page. Supplementary: it degrades to a calm
  // "not available" panel and never throws the whole page.
  const latencyQuery = useLatency(range);

  // A preset click: recompute the range (presetToRange clamps to <=366 days so
  // the client never submits a 422-triggering span); the queryKey refetches.
  const onPreset = (id: string): void => {
    setPreset(id);
    setRange(presetToRange(id, new Date()));
  };
  // The calendar Apply emit: set the explicit range directly.
  const onCustomRange = (r: { start: string; end: string }): void => {
    setPreset('Custom');
    setRange(r);
  };

  const loading = query.isPending;
  const isError = query.isError;
  const data = query.data;

  // Distribute the contract slices. While loading these are null/[]/0.
  const totals = data?.totals ?? null;
  const series = data?.series ?? [];
  const models = data?.models ?? [];
  const visibleModels = applyUsageTypeFilter(models, usageType).filter((m) =>
    matchesSearch(modelSearch, m.model),
  );
  // Activity rows unioned with the user's keys (idle keys padded with zeros),
  // ranked by spend — so TOP API KEYS lists all keys, not only the active ones.
  const keys = mergeTopKeys(data?.keys ?? [], selectKeyRows(keysQuery.data));
  const budget = data?.budget ?? null;
  const capabilities = data?.capabilities ?? null;

  // The header band — title + sub on the LEFT, the date controls on the RIGHT.
  // rangeError is null for now (the 422-inline distinction is deferred; useStats
  // throws generically into isError, surfaced as the whole-page error card).
  const header = (
    <div className="flex flex-col gap-1">
      {/* Row 1: title (left) + date controls (right). */}
      <div className="flex flex-wrap items-start justify-between gap-x-5 gap-y-2">
        <h1 className="font-sans text-2xl font-semibold leading-snug text-text-primary">
          {PAGE_TITLE}
        </h1>
        <DateRange
          preset={preset}
          onPreset={onPreset}
          onCustomRange={onCustomRange}
          rangeError={null}
        />
      </div>
      {/* Row 2: sub-line (left) + the RESOLVED window (right, baseline-aligned to
          the sub-line). Only shown for a Custom range — the presets already name
          their window (7d / This month / …). formatDate turns the "YYYY-MM-DD"
          bounds into "Jul 11, 2026". */}
      <div className="flex flex-wrap items-baseline justify-between gap-x-5 gap-y-1">
        <p className="max-w-2xl font-sans text-sm text-text-secondary">
          {PAGE_SUB}
        </p>
        {preset === 'Custom' ? (
          <p
            data-slot="stats-range-label"
            className="font-mono text-xs text-text-tertiary"
          >
            {formatDate(range.start)} – {formatDate(range.end)}
          </p>
        ) : null}
      </div>
    </div>
  );

  // ── Whole-page error (502 / network) ────────────────────────────────────────
  // The header band still renders above the locked error card; the body is the
  // error card with a bordered destructive retry that re-fires the query.
  if (isError) {
    return (
      <div className="flex flex-col gap-8">
        {header}
        <div className="flex flex-col items-center gap-3 rounded-xl border border-border bg-surface p-12 text-center">
          <div className="font-sans text-2xl font-semibold text-text-primary">
            {ERR_HEADING}
          </div>
          <div className="max-w-md font-sans text-sm text-text-secondary">
            {ERR_BODY}
          </div>
          <button
            type="button"
            onClick={() => void query.refetch()}
            className="inline-flex cursor-pointer items-center justify-center rounded-lg border border-destructive px-4 py-2 font-mono text-xs font-semibold uppercase tracking-wide text-destructive transition-colors hover:bg-destructive/10"
          >
            retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-5">
      {header}

      {/* §2 KPI row */}
      {loading ? (
        <div className="grid grid-cols-4 gap-3 max-[880px]:grid-cols-2 max-[520px]:grid-cols-1">
          <Skeleton variant="card" />
          <Skeleton variant="card" />
          <Skeleton variant="card" />
          <Skeleton variant="card" />
        </div>
      ) : (
        <KpiRow totals={totals} series={series} />
      )}

      {/* §7 Account budget — full-width band */}
      {loading ? <Skeleton variant="card" /> : <BudgetPanel budget={budget} />}

      {/* §3 the two time-series charts, side-by-side (stack on narrow) */}
      <div className="grid grid-cols-2 gap-3 max-[880px]:grid-cols-1">
        <Panel label={SECTION_DAILY_SPEND}>
          {loading ? <Skeleton variant="chart" /> : <SpendChart series={series} />}
        </Panel>
        <Panel
          label={SECTION_REQUESTS}
          action={
            loading ? undefined : (
              <RequestsMetricToggle
                metric={requestsMetric}
                onChange={setRequestsMetric}
              />
            )
          }
        >
          {loading ? (
            <Skeleton variant="chart" />
          ) : (
            <RequestsChart series={series} metric={requestsMetric} />
          )}
        </Panel>
      </div>

      {/* §5 usage-by-model donut + §6 top keys. No items-start: the cells
          stretch to equal height, and both panels are h-full so the shorter
          one (top keys) matches its taller sibling (empty space below is fine). */}
      <div className="grid grid-cols-2 gap-3 max-[880px]:grid-cols-1">
        <Panel label={SECTION_USAGE_BY_MODEL}>
          {loading ? (
            <Skeleton variant="chart" />
          ) : (
            <UsageDonut
              models={models}
              totalSpend={totals?.spend ?? null}
              capabilities={capabilities}
            />
          )}
        </Panel>
        {loading ? (
          <div className="h-full rounded-xl border border-border bg-surface p-5">
            <Skeleton variant="table-rows" rows={5} />
          </div>
        ) : (
          <TopKeys keys={keys} capabilities={capabilities} />
        )}
      </div>

      {/* Latency + request outcomes — sourced from /api/session/latency over the
          same date range as the page. Own (independent) loading state. */}
      <div className="grid grid-cols-2 gap-3 max-[880px]:grid-cols-1">
        <Panel label={SECTION_LATENCY}>
          {latencyQuery.isPending ? (
            <Skeleton variant="chart" />
          ) : (
            <LatencyPanel
              data={latencyQuery.data}
              isError={latencyQuery.isError}
            />
          )}
        </Panel>
        <Panel label={SECTION_OUTCOMES}>
          {latencyQuery.isPending ? (
            <Skeleton variant="chart" />
          ) : (
            <ErrorsDonut
              data={latencyQuery.data}
              isError={latencyQuery.isError}
            />
          )}
        </Panel>
      </div>

      {/* §4 Model Breakdown */}
      <div className="min-w-0">
        <div className="mb-3 flex min-h-6 flex-wrap items-center justify-between gap-3">
          <div className={SECTION_LABEL_CLASS}>{SECTION_MODEL_BREAKDOWN}</div>
          {/* One line: chips + search + export. No flex-wrap on the inner row —
              TableSearch is w-full and would push itself onto a second row. The
              chips are shrink-0, so the search input absorbs any width squeeze;
              the outer row still wraps, dropping this block under the label. */}
          {loading ? null : (
            <div className="flex items-center gap-2">
              <UsageTypeFilter value={usageType} onChange={setUsageType} />
              {/* Decorative divider between the type chips and the search box. */}
              <div className="h-4 w-px shrink-0 bg-border" aria-hidden="true" />
              <TableSearch
                value={modelSearch}
                onChange={setModelSearch}
                placeholder="Search models…"
              />
              <ExportCsvButton
                rows={models}
                columns={MODELS_CSV_COLS}
                filename={`models-${range.start}-${range.end}.csv`}
              />
            </div>
          )}
        </div>
        {loading ? (
          <Skeleton variant="table-rows" rows={6} />
        ) : (
          <ModelTable models={visibleModels} capabilities={capabilities} />
        )}
      </div>
    </div>
  );
}
