// UsageByModel.tsx — one "Usage by model" widget for Overview (top N) and Cost
// (paged), switchable Cost · Tokens · Calls. Each row shows one value and its
// share; rows that are zero for the chosen metric are hidden and counted.
import type { ReactNode } from 'react';
import type { StatsModelRow } from '@/lib/api-types';
import { abbreviate, formatCurrency, formatInt, formatPercent } from '@/lib/format';
import { usePersistedChoice } from '@/hooks/use-persisted-choice';
import { Bars, Paged, Panel, Segmented } from './ReportParts';

/** Rows with recorded spend (≥ $0.005, i.e. not shown as $0.00). */
export const withSpend = <T extends { spend: number }>(rows: T[]): T[] => rows.filter((row) => row.spend >= 0.005);
export const sharePct = (v: number | null | undefined) => formatPercent(v);
export const hiddenNote = (n: number, noun: string, what = 'spend') => n > 0 ? <p className="report-source-note">{formatInt(n)} {noun}{n === 1 ? '' : 's'} with no recorded {what} hidden.</p> : null;

const METRICS = ['Cost', 'Tokens', 'Calls'] as const;
const METRIC: Record<typeof METRICS[number], [(row: StatsModelRow) => number, (n: number) => string, string, (rows: StatsModelRow[]) => StatsModelRow[]]> = {
  Cost: [(row) => row.spend, formatCurrency, 'spend', withSpend],
  Tokens: [(row) => row.total_tokens, abbreviate, 'tokens', (rows) => rows.filter((row) => row.total_tokens > 0)],
  Calls: [(row) => row.requests, formatInt, 'calls', (rows) => rows.filter((row) => row.requests > 0)],
};

export function UsageByModel({ models, paged = false, limit = 10, children }: { models: StatsModelRow[]; paged?: boolean; limit?: number; children?: ReactNode }) {
  const [metric, setMetric] = usePersistedChoice('usage-by-model', METRICS, 'Cost');
  const [value, format, noun, shown] = METRIC[metric];
  const rows = shown(models).sort((a, b) => value(b) - value(a));
  const total = rows.reduce((sum, row) => sum + value(row), 0);
  const notes = hiddenNote(models.length - rows.length, 'model', noun);
  const bars = (page: StatsModelRow[]) => <Bars total={total} rows={page.map((row) => ({ name: row.model || 'Unknown model', value: value(row), display: format(value(row)) }))} />;
  return <Panel title="Usage by model" subtitle={`Public model alias · share of ${noun}`} action={<Segmented label="Usage by model metric" options={METRICS} value={metric} onChange={setMetric} />}>
    {paged ? <Paged rows={rows} footer={<div>{notes}{children}</div>}>{bars}</Paged> : <>{bars(rows.slice(0, limit))}{notes}{children}</>}
  </Panel>;
}
