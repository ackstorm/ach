// LatencyPanel.tsx — request-latency figures for the #/stats page (new panel).
//
// Sourced from GET /api/session/latency (LiteLLM /spend/logs, fixed 7-day window).
// PURE presentational leaf — no fetch: the container passes the LatencyResponse
// slice + an isError flag and renders one of three states:
//   • unavailable — available === false (scoping unverified / fetch failed) OR isError
//   • empty       — available but no rows in the window
//   • ready       — headline (Throughput · Latency p50 · Error rate · TTFT) + the
//                   per-model split (with its own Lat / p95 columns)
//
// The headline leads with the figures that matter at a glance; the tail
// percentiles (p95) live only in the per-model table.
//
// Latency is SUPPLEMENTARY: it never throws the whole page. All figures are
// null-guarded (D-08 null-vs-0) and render an em-dash when absent.

import * as React from 'react';

import { SortIndicator } from '@/components/ui/data-table';
import type { LatencyByModel, LatencyResponse } from '@/lib/api-types';
import { formatInt } from '@/lib/format';
import { sortRows, type SortDir } from '@/lib/sort';
import { StatTile } from './chart-common';

const EM_DASH = '—';

const UNAVAILABLE_COPY = 'Latency data is not available for this deployment.';
const EMPTY_COPY = 'No requests in this range.';

// Format a millisecond figure: <1000 → "812 ms"; else "3.25 s" (2 sig decimals).
function formatMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return EM_DASH;
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

function formatTps(tps: number | null | undefined): string {
  if (tps === null || tps === undefined || !Number.isFinite(tps)) return EM_DASH;
  return `${Math.round(tps)} tok/s`;
}

// Failed / total across the outcome buckets (any non-"success" status = failed).
// Null when there are no outcome rows (→ em-dash).
function errorRate(outcomes: LatencyResponse['outcomes']): number | null {
  if (!Array.isArray(outcomes) || outcomes.length === 0) return null;
  let total = 0;
  let failed = 0;
  for (const o of outcomes) {
    const c = Number(o?.count) || 0;
    total += c;
    if (o?.status !== 'success') failed += c;
  }
  return total > 0 ? failed / total : null;
}

function formatPct(fraction: number | null): string {
  if (fraction === null || !Number.isFinite(fraction)) return EM_DASH;
  return `${(fraction * 100).toFixed(1)}%`;
}

// A calm centered state (unavailable / empty) sized to roughly match the chart body.
function StatePanel({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-40 flex-col items-center justify-center gap-2 text-center">
      <div className="font-sans text-sm text-text-secondary">{children}</div>
    </div>
  );
}

// Shared grid template so the header row and the data rows line up column-for-
// column (fixed numeric widths, right-aligned — model name takes the rest).
const MODEL_GRID =
  'grid grid-cols-[1fr_2.75rem_4rem_4rem_3.25rem] items-center gap-2';
const MODEL_HEAD_CELL =
  'font-mono text-[10px] font-semibold uppercase tracking-wider text-text-secondary';

// The sortable columns and how each maps to a comparable value (null sorts last).
// `err` is the failure ratio; requests===0 → null so those rows sort last.
type ModelSortKey = 'model' | 'requests' | 'latency' | 'p95' | 'err';
const MODEL_SORT_ACCESSORS: Record<
  ModelSortKey,
  (m: LatencyByModel) => number | string | null
> = {
  model: (m) => m.model,
  requests: (m) => m.requests,
  latency: (m) => m.p50_ms,
  p95: (m) => m.p95_ms,
  err: (m) => (m.requests > 0 ? m.failed / m.requests : null),
};

// A clickable column header reusing the DataTable sort triangle so the LATENCY
// table sorts identically to TOP API KEYS. Model left-aligned, figures right.
function SortHeader({
  label,
  sortKey,
  active,
  dir,
  onSort,
  align = 'left',
}: {
  label: string;
  sortKey: ModelSortKey;
  active: boolean;
  dir: SortDir;
  onSort: (key: ModelSortKey) => void;
  align?: 'left' | 'right';
}) {
  return (
    <button
      type="button"
      data-slot="latency-sort"
      onClick={() => onSort(sortKey)}
      className={`flex w-full cursor-pointer items-center gap-1 ${MODEL_HEAD_CELL} transition-colors hover:text-text-primary ${align === 'right' ? 'justify-end' : 'justify-start'} ${active ? 'text-text-primary' : ''}`}
    >
      <span>{label}</span>
      <SortIndicator active={active} dir={active ? dir : 'desc'} />
    </button>
  );
}

function ModelHeader({
  sort,
  onSort,
}: {
  sort: { key: ModelSortKey; dir: SortDir };
  onSort: (key: ModelSortKey) => void;
}) {
  return (
    <div className={`${MODEL_GRID} border-b border-border/60 pb-1`}>
      <SortHeader label="Model" sortKey="model" active={sort.key === 'model'} dir={sort.dir} onSort={onSort} />
      <SortHeader label="Req" sortKey="requests" active={sort.key === 'requests'} dir={sort.dir} onSort={onSort} align="right" />
      <SortHeader label="Lat" sortKey="latency" active={sort.key === 'latency'} dir={sort.dir} onSort={onSort} align="right" />
      <SortHeader label="p95" sortKey="p95" active={sort.key === 'p95'} dir={sort.dir} onSort={onSort} align="right" />
      <SortHeader label="Err" sortKey="err" active={sort.key === 'err'} dir={sort.dir} onSort={onSort} align="right" />
    </div>
  );
}

function ModelRow({ m }: { m: LatencyByModel }) {
  const errPct =
    m.requests > 0 ? ((m.failed / m.requests) * 100).toFixed(1) : null;
  const stripped = m.model.includes('/')
    ? m.model.slice(m.model.indexOf('/') + 1)
    : m.model;
  return (
    <li title={m.model} className={`${MODEL_GRID} py-1`}>
      <span className="max-w-full truncate justify-self-start font-mono text-xs text-text-primary">
        {stripped}
      </span>
      <span className="text-right font-mono text-xs text-text-secondary tabular-nums">
        {formatInt(m.requests)}
      </span>
      <span className="text-right font-mono text-xs text-text-secondary tabular-nums">
        {formatMs(m.p50_ms)}
      </span>
      <span className="text-right font-mono text-xs text-text-secondary tabular-nums">
        {formatMs(m.p95_ms)}
      </span>
      <span
        className={`text-right font-mono text-xs tabular-nums ${
          m.failed > 0 ? 'text-destructive' : 'text-text-tertiary'
        }`}
      >
        {errPct === null ? EM_DASH : `${errPct}%`}
      </span>
    </li>
  );
}

export interface LatencyPanelProps {
  data: LatencyResponse | undefined;
  isError: boolean;
}

export function LatencyPanel({
  data,
  isError,
}: LatencyPanelProps): React.ReactElement {
  // Interactive column sort; default REQUESTS desc (the contract's incoming order).
  const [sort, setSort] = React.useState<{ key: ModelSortKey; dir: SortDir }>({
    key: 'requests',
    dir: 'desc',
  });
  const onSort = (key: ModelSortKey) =>
    setSort((cur) =>
      cur.key === key
        ? { key, dir: cur.dir === 'asc' ? 'desc' : 'asc' }
        : { key, dir: 'desc' }
    );

  if (isError || !data || !data.available) {
    return <StatePanel>{UNAVAILABLE_COPY}</StatePanel>;
  }
  if (data.row_count === 0 || !data.latency) {
    return <StatePanel>{EMPTY_COPY}</StatePanel>;
  }

  const l = data.latency;
  const err = errorRate(data.outcomes);
  const models = sortRows(data.by_model, MODEL_SORT_ACCESSORS[sort.key], sort.dir);
  return (
    <div className="flex flex-col gap-4">
      {/* Headline: the at-a-glance figures — throughput, typical latency, error
          rate, TTFT. (Request volume lives in the Request Outcomes panel; the tail
          percentiles are demoted to the per-model table's p95 column.) */}
      <div className="flex flex-wrap justify-between gap-3">
        <StatTile
          label="THROUGHPUT"
          title="Output tokens per second (p50)"
          value={formatTps(l.tokens_per_sec_p50)}
        />
        <StatTile
          label="LATENCY"
          title="Median request latency (p50)"
          value={formatMs(l.p50_ms)}
        />
        <StatTile
          label="ERROR RATE"
          value={formatPct(err)}
          tone={err !== null && err > 0 ? 'bad' : 'default'}
        />
        <StatTile
          label="TTFT"
          title="Time to first token (p50)"
          value={formatMs(l.ttft_p50_ms)}
        />
      </div>

      {/* Per-model latency + error split (headed table, aligned columns). Extra
          top padding sets it apart from the headline summary (no divider line). */}
      {models.length > 0 ? (
        <div className="flex flex-col pt-2">
          <ModelHeader sort={sort} onSort={onSort} />
          <ul className="flex flex-col divide-y divide-border/60">
            {models.map((m) => (
              <ModelRow key={m.model} m={m} />
            ))}
          </ul>
        </div>
      ) : null}

      {/* REQUESTS is now a headline stat, so the footer only carries the
          sample-truncation caveat when the row cap kicked in. */}
      {data.sampled ? (
        <div className="font-mono text-[10px] text-text-tertiary">
          {formatInt(data.row_count)} requests · sampled
        </div>
      ) : null}
    </div>
  );
}
