// UsageTypeFilter.tsx — the TYPE chip row above the #/stats USAGE BREAKDOWN
// table (ALL / MODEL / MCP TOOL). Mirrors the models catalog MODE row visually
// (shared `chipClass`), but is SINGLE-select: the breakdown has exactly two row
// types, so a multi-select toggle would make "both on" a synonym for ALL.
//
// PRESENTATIONAL LEAF: the active value + setter are owned by Stats.tsx.
//
// Classification delegates to `isMcpModelRow` — the same predicate the table's
// TYPE column renders from, so the chip and the column can never disagree.

import * as React from 'react';

import type { StatsModelRow } from '@/lib/api-types';
import { chipClass } from '@/lib/chip';
import { isMcpModelRow } from '@/lib/model-classify';

export type UsageType = 'all' | 'model' | 'mcp';

const OPTIONS: { key: UsageType; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'model', label: 'Model' },
  { key: 'mcp', label: 'MCP Tool' },
];

/** Narrow the breakdown rows to one type; `all` passes them through untouched. */
export function applyUsageTypeFilter(
  rows: StatsModelRow[],
  type: UsageType,
): StatsModelRow[] {
  if (type === 'all') return rows;
  return rows.filter((r) => isMcpModelRow(r.model) === (type === 'mcp'));
}

export function UsageTypeFilter({
  value,
  onChange,
}: {
  value: UsageType;
  onChange: (t: UsageType) => void;
}): React.ReactElement {
  return (
    <div data-slot="usage-type-filter" className="flex shrink-0 items-center gap-1.5">
      <span className="mr-1 font-mono text-[10px] font-semibold uppercase tracking-wider text-text-tertiary">
        Type
      </span>
      {OPTIONS.map((o) => (
        <button
          key={o.key}
          type="button"
          aria-pressed={value === o.key}
          onClick={() => onChange(o.key)}
          className={chipClass(value === o.key)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
