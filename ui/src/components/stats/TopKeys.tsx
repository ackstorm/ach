// TopKeys.tsx — the Top API Keys panel for the #/stats Usage & Spend page
// (STATS-07). React + Tailwind port of the old Preact leaf src/ui/stats-top-keys.js.
//
// Ranks the user's keys by spend with HAND-ROLLED horizontal bars (no chart dep).
// Each bar REUSES the dashboard budget-bar shape — an 8px track with a primary
// fill whose width is ∝ `spend_pct` (clamped [0,100], divide-by-zero guarded).
//
// PRESENTATIONAL LEAF: it receives the Phase-12 `keys[]` slice (already spend
// desc from the contract) + the `capabilities` map. No fetch, no data ownership.
//
// SECURITY (T-13-08, XSS): the key label (key_alias || maskKey(id)) and every
// figure render as React text children (auto-escaped). No dangerouslySetInnerHTML.
// SECURITY (T-13-10, info disclosure — ACCEPTED): maskKey shows only a public
// `key-…last4` form; the full `sk-` is never client-side on this read-only page.

import * as React from 'react';

import { SortIndicator } from '@/components/ui/data-table';
import type { StatsCapabilities, StatsKeyRow } from '@/lib/api-types';
import { formatCurrency, formatInt, maskKey } from '@/lib/format';
import { sortRows, type SortDir } from '@/lib/sort';
import { capabilityRenderMode } from '@/lib/stats-presets';
import { StatTile } from './chart-common';

// The em-dash placeholder (matches format.ts EM_DASH).
const EM_DASH = '—';

// Locked copy (13-UI-SPEC §Copywriting Contract / §5).
const SECTION_LABEL = 'TOP API KEYS';
const COL_KEY = 'KEY';
const COL_REQUESTS = 'REQUESTS';
const COL_SPEND = 'SPEND';
const COL_PCT = '% OF TOTAL';
const COMING_SOON_COPY = 'Coming soon';
const EMPTY_COPY = 'No usage in this range';

// The max number of ranked rows shown (UI-SPEC §5 "top N per the mockup").
const TOP_N = 8;

// Shared 5-track grid (KEY | bar | REQUESTS | SPEND | % OF TOTAL).
const GRID_COLS =
  'grid grid-cols-[minmax(80px,1.4fr)_minmax(0,2fr)_72px_88px_80px] items-center gap-4';

// Render the contract's `spend_pct` fraction as a one-decimal `%`; a null /
// non-finite fraction -> em-dash.
function formatPct(fraction: number | null | undefined): string {
  if (typeof fraction !== 'number' || !Number.isFinite(fraction)) return EM_DASH;
  return `${(fraction * 100).toFixed(1)}%`;
}

// The bar fill width as a clamped 0..100 percentage of the contract `spend_pct`
// fraction. Divide-by-zero / null / non-finite -> 0 (an empty track), never NaN.
function barWidthPct(fraction: number | null | undefined): number {
  if (typeof fraction !== 'number' || !Number.isFinite(fraction)) return 0;
  return Math.max(0, Math.min(100, fraction * 100));
}

// The sortable columns and how each maps to a comparable value (null sorts last).
type KeySortKey = 'key' | 'requests' | 'spend' | 'pct';
const KEY_SORT_ACCESSORS: Record<
  KeySortKey,
  (k: StatsKeyRow) => number | string | null | undefined
> = {
  key: (k) => k.key_alias || maskKey(k.id),
  requests: (k) => k.requests,
  spend: (k) => k.spend,
  pct: (k) => k.spend_pct,
};

// A clickable column header for the hand-rolled grid — reuses the DataTable sort
// triangle so the two tables read identically. KEY left-aligned, figures right.
function SortHeader({
  label,
  sortKey,
  active,
  dir,
  onSort,
  align = 'left',
}: {
  label: string;
  sortKey: KeySortKey;
  active: boolean;
  dir: SortDir;
  onSort: (key: KeySortKey) => void;
  align?: 'left' | 'right';
}): React.ReactElement {
  return (
    <button
      type="button"
      data-slot="top-keys-sort"
      onClick={() => onSort(sortKey)}
      className={`flex w-full cursor-pointer items-center gap-1 uppercase tracking-wider transition-colors hover:text-text-primary ${align === 'right' ? 'justify-end' : 'justify-start'} ${active ? 'text-text-primary' : ''}`}
    >
      <span>{label}</span>
      <SortIndicator active={active} dir={active ? dir : 'desc'} />
    </button>
  );
}

// One ranked key row: the label, the hand-rolled horizontal spend-share bar, the
// requests, the spend figure, and the `% OF TOTAL`.
function KeyRowItem({ item: k }: { item: StatsKeyRow }): React.ReactElement {
  // Public key id is shown masked when there is no alias (maskKey preserves the
  // real `key-` prefix; the full sk- is never client-side here — T-13-10).
  const label = k.key_alias || maskKey(k.id);
  const width = barWidthPct(k.spend_pct);

  return (
    <div data-slot="top-keys-row" className={`${GRID_COLS} py-1`}>
      <div
        className="truncate font-mono text-xs text-text-primary"
        title={label}
      >
        {label == null ? EM_DASH : label}
      </div>
      <div className="w-full">
        <div className="h-2.5 w-full overflow-hidden rounded-full bg-primary/10">
          <div
            data-slot="top-keys-fill"
            className="h-full rounded-full bg-gradient-to-r from-primary/60 to-primary transition-[width] duration-300"
            style={{ width: `${width}%` }}
          />
        </div>
      </div>
      <div className="text-right font-mono text-xs text-text-primary whitespace-nowrap">
        {formatInt(k.requests)}
      </div>
      <div className="text-right font-mono text-xs text-text-primary whitespace-nowrap">
        {formatCurrency(k.spend)}
      </div>
      <div className="text-right font-mono text-xs text-text-primary whitespace-nowrap">
        {formatPct(k.spend_pct)}
      </div>
    </div>
  );
}

// A panel wrapper sharing the section-label head + surface card chrome.
function Panel({ children }: { children: React.ReactNode }): React.ReactElement {
  return (
    <div
      data-slot="top-keys"
      className="flex h-full flex-col gap-4 rounded-xl border border-border bg-surface p-5"
    >
      <div className="flex items-baseline justify-between gap-3">
        <span className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
          {SECTION_LABEL}
        </span>
      </div>
      {children}
    </div>
  );
}

export interface TopKeysProps {
  /** The Phase-12 `keys[]` slice — already spend desc from the contract. */
  keys: StatsKeyRow[] | null | undefined;
  /** The contract `capabilities` map (gate: `per_key_spend`). */
  capabilities: StatsCapabilities | null | undefined;
}

export function TopKeys({
  keys,
  capabilities,
}: TopKeysProps): React.ReactElement {
  const rows = Array.isArray(keys) ? keys : [];
  // Idle = no in-window activity (padded by mergeTopKeys so the panel lists every
  // key). Hidden by default — they are noise — behind an explicit count toggle.
  const [showIdle, setShowIdle] = React.useState(false);
  // Interactive column sort; default SPEND desc (the contract's incoming order).
  const [sort, setSort] = React.useState<{ key: KeySortKey; dir: SortDir }>({
    key: 'spend',
    dir: 'desc',
  });
  const onSort = (key: KeySortKey) =>
    setSort((cur) =>
      cur.key === key
        ? { key, dir: cur.dir === 'asc' ? 'desc' : 'asc' }
        : { key, dir: 'desc' }
    );
  const active = rows.filter((k) => (k.requests ?? 0) > 0 || (k.spend ?? 0) > 0);
  const idleCount = rows.length - active.length;
  // Summary totals across every key (idle keys carry 0, so this equals the active
  // sum) — the at-a-glance headline mirroring the Latency panel's top summary.
  const totalRequests = rows.reduce((s, k) => s + (k.requests ?? 0), 0);
  const totalSpend = rows.reduce((s, k) => s + (k.spend ?? 0), 0);
  const visible = sortRows(
    showIdle ? rows : active,
    KEY_SORT_ACCESSORS[sort.key],
    sort.dir
  );
  // StatsCapabilities is a fixed boolean record; capabilityRenderMode wants the
  // open Record<string, boolean> shape, so we read it through that view.
  const mode = capabilityRenderMode(
    capabilities as Record<string, boolean> | null | undefined,
    'per_key_spend',
    rows.length > 0
  );

  // per_key_spend === false -> the panel-level coming-soon state (D-15): no bars,
  // em-dash where a figure would sit.
  if (mode === 'coming-soon') {
    return (
      <Panel>
        <div
          data-slot="top-keys-state"
          className="px-4 py-8 text-center"
        >
          <div className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
            {COMING_SOON_COPY}
          </div>
          <div className="mt-2 font-mono text-xs text-text-secondary">
            {EM_DASH}
          </div>
        </div>
      </Panel>
    );
  }

  // A real but empty keys: [] -> the no-usage state.
  if (mode === 'empty') {
    return (
      <Panel>
        <div data-slot="top-keys-state" className="px-4 py-8 text-center">
          <div className="font-sans text-sm text-text-secondary">{EMPTY_COPY}</div>
        </div>
      </Panel>
    );
  }

  // Cap at TOP_N in the default view; "show idle" reveals the full list.
  const ranked = showIdle ? visible : visible.slice(0, TOP_N);

  const toggle =
    idleCount > 0 ? (
      <button
        type="button"
        data-slot="top-keys-toggle"
        onClick={() => setShowIdle((v) => !v)}
        className="flex cursor-pointer items-center gap-1 self-start font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary transition-colors hover:text-text-primary"
      >
        <span
          aria-hidden="true"
          className={`inline-block size-0 border-y-[4px] border-l-[6px] border-y-transparent border-l-current transition-transform ${showIdle ? 'rotate-90' : ''}`}
        />
        <span>{showIdle ? 'Hide idle keys' : `Show idle keys (${idleCount})`}</span>
      </button>
    ) : null;

  return (
    <Panel>
      {/* At-a-glance summary (active-key count · total requests · total spend) —
          mirrors the Latency panel's top summary. */}
      <div data-slot="top-keys-summary" className="flex flex-wrap justify-between gap-3">
        <StatTile label="ACTIVE KEYS" value={formatInt(active.length)} />
        <StatTile label="REQUESTS" value={formatInt(totalRequests)} />
        <StatTile label="SPEND" value={formatCurrency(totalSpend)} />
      </div>
      <div data-slot="top-keys-table" className="flex flex-col gap-2">
        <div
          className={`${GRID_COLS} border-b border-border pb-1 font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary`}
        >
          <SortHeader
            label={COL_KEY}
            sortKey="key"
            active={sort.key === 'key'}
            dir={sort.dir}
            onSort={onSort}
          />
          <span aria-hidden="true" />
          <SortHeader
            label={COL_REQUESTS}
            sortKey="requests"
            active={sort.key === 'requests'}
            dir={sort.dir}
            onSort={onSort}
            align="right"
          />
          <SortHeader
            label={COL_SPEND}
            sortKey="spend"
            active={sort.key === 'spend'}
            dir={sort.dir}
            onSort={onSort}
            align="right"
          />
          <SortHeader
            label={COL_PCT}
            sortKey="pct"
            active={sort.key === 'pct'}
            dir={sort.dir}
            onSort={onSort}
            align="right"
          />
        </div>
        {ranked.length === 0 ? (
          <div data-slot="top-keys-state" className="px-4 py-8 text-center">
            <div className="font-sans text-sm text-text-secondary">
              {EMPTY_COPY}
            </div>
          </div>
        ) : (
          ranked.map((k, i) => <KeyRowItem key={k.id ?? i} item={k} />)
        )}
      </div>
      {toggle}
    </Panel>
  );
}
