// ModelTable.tsx — the Model Breakdown DATA-TABLE for the #/stats Usage & Spend
// page (STATS-06). React + Tailwind, rendered through the generic DataTable<T>.
// Port of the old Preact leaf src/ui/stats-model-table.js.
//
// PRESENTATIONAL LEAF: it receives the Phase-12 `models[]` slice + the
// `capabilities` map. No fetch, no data ownership.
//
// `null` ≠ `0` (Phase-12 D-08): an unavailable figure renders em-dash, never 0.
// Capability-driven cells (D-15): token_split === false -> INPUT/OUTPUT cells
// em-dash; per_model_last_used === false -> the whole LAST USED column em-dash.
// Rows are sorted SPEND descending (UI-SPEC §6 — matches the donut's narrative).
//
// SECURITY (T-13-08, XSS): every cell value renders as a React text child
// (auto-escaped). No dangerouslySetInnerHTML.

import * as React from 'react';

import {
  DataTable,
  type DataTableColumn,
} from '@/components/ui/data-table';
import type { StatsCapabilities, StatsModelRow } from '@/lib/api-types';
import { abbreviate, formatCurrency, formatDate, formatInt } from '@/lib/format';
import { isMcpModelRow, mcpToolLabel } from '@/lib/model-classify';

// The em-dash placeholder (matches format.ts EM_DASH) — every null / unavailable
// / capability-disabled cell renders THIS, never a `0`.
const EM_DASH = '—';

// The locked empty copy (UI-SPEC §Copywriting Contract / §6).
const EMPTY_COPY = 'No usage in this range';

// Render the contract's `spend_pct` fraction as a one-decimal `%` (UI-SPEC
// §Typography: 0.182 -> "18.2%"); a null / non-finite fraction -> em-dash.
function formatPct(fraction: number | null | undefined): string {
  if (typeof fraction !== 'number' || !Number.isFinite(fraction)) return EM_DASH;
  return `${(fraction * 100).toFixed(1)}%`;
}

export interface ModelTableProps {
  /** The Phase-12 `models[]` slice. */
  models: StatsModelRow[] | null | undefined;
  /** The contract `capabilities` map (token_split, per_model_last_used, …). A
   *  missing entry is treated as AVAILABLE — only an explicit `false` collapses
   *  a column. */
  capabilities: StatsCapabilities | null | undefined;
}

export function ModelTable({
  models,
  capabilities,
}: ModelTableProps): React.ReactElement {
  const caps = capabilities ?? null;
  const tokenSplit = caps?.token_split;
  const perModelLastUsed = caps?.per_model_last_used;

  const rows = Array.isArray(models) ? models : [];

  // Column order (UI-SPEC §6). Numeric columns right-aligned; MODEL
  // and LAST USED left-aligned (mirrors the old smt-cell-model/lastused idiom).
  // Every column declares a `sortAccessor` so DataTable can sort on a header
  // click; the default sort below keeps the original SPEND-descending view.
  const columns: DataTableColumn<StatsModelRow>[] = [
    {
      key: 'type',
      header: 'TYPE',
      className: 'whitespace-nowrap',
      cell: (m) => (
        <span className="font-mono text-[10px] font-semibold uppercase tracking-wide text-text-tertiary">
          {isMcpModelRow(m.model) ? 'MCP Tool' : 'Model'}
        </span>
      ),
      sortAccessor: (m) => (isMcpModelRow(m.model) ? 1 : 0),
    },
    {
      key: 'model',
      header: 'NAME',
      className: 'font-mono text-xs text-text-primary',
      cell: (m) => (m.model == null ? EM_DASH : mcpToolLabel(m.model)),
      sortAccessor: (m) => m.model,
    },
    {
      key: 'requests',
      header: 'REQUESTS',
      headerClassName: 'text-right',
      className: 'font-mono text-xs text-right whitespace-nowrap',
      cell: (m) => formatInt(m.requests),
      sortAccessor: (m) => m.requests,
    },
    {
      key: 'input',
      header: 'INPUT',
      headerClassName: 'text-right',
      className: 'font-mono text-xs text-right whitespace-nowrap',
      cell: (m) =>
        isMcpModelRow(m.model)
          ? EM_DASH
          : tokenSplit === false
            ? EM_DASH
            : abbreviate(m.input_tokens),
      sortAccessor: (m) => m.input_tokens,
    },
    {
      key: 'output',
      header: 'OUTPUT',
      headerClassName: 'text-right',
      className: 'font-mono text-xs text-right whitespace-nowrap',
      cell: (m) =>
        isMcpModelRow(m.model)
          ? EM_DASH
          : tokenSplit === false
            ? EM_DASH
            : abbreviate(m.output_tokens),
      sortAccessor: (m) => m.output_tokens,
    },
    {
      key: 'total',
      header: 'TOTAL',
      headerClassName: 'text-right',
      className: 'font-mono text-xs text-right whitespace-nowrap',
      cell: (m) => (isMcpModelRow(m.model) ? EM_DASH : abbreviate(m.total_tokens)),
      sortAccessor: (m) => m.total_tokens,
    },
    {
      key: 'cached',
      header: 'CACHED',
      headerClassName: 'text-right',
      className: 'font-mono text-xs text-right whitespace-nowrap',
      cell: (m) => (isMcpModelRow(m.model) ? EM_DASH : abbreviate(m.cache_read_tokens)),
      sortAccessor: (m) => m.cache_read_tokens,
    },
    {
      key: 'spend',
      header: 'SPEND',
      headerClassName: 'text-right',
      className: 'font-mono text-xs text-right whitespace-nowrap',
      cell: (m) =>
        isMcpModelRow(m.model) ? (
          <span data-testid="usage-spend-mcp" className="text-muted-foreground">
            {EM_DASH}
          </span>
        ) : (
          formatCurrency(m.spend)
        ),
      sortAccessor: (m) => m.spend,
    },
    {
      key: '%spend',
      header: '% SPEND',
      headerClassName: 'text-right',
      className: 'font-mono text-xs text-right whitespace-nowrap',
      cell: (m) => (isMcpModelRow(m.model) ? EM_DASH : formatPct(m.spend_pct)),
      sortAccessor: (m) => m.spend_pct,
    },
    {
      key: 'lastused',
      header: 'LAST USED',
      className: 'font-mono text-xs whitespace-nowrap',
      cell: (m) =>
        perModelLastUsed === false ? EM_DASH : formatDate(m.last_used),
      sortAccessor: (m) => m.last_used,
    },
  ];

  return (
    <DataTable
      data-slot="model-table"
      columns={columns}
      rows={rows}
      defaultSort={{ key: 'spend', dir: 'desc' }}
      getRowId={(m) => m.model ?? ''}
      empty={
        <div data-slot="model-table-empty">
          <p className="text-text-secondary text-sm">{EMPTY_COPY}</p>
        </div>
      }
    />
  );
}
