// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// Cost.tsx — Analytics → Cost (approved Cost v3 content). Daily spend is the
// primary chart. Period spend, model and key breakdowns come from the gateway's
// daily activity (/platform/console/stats); per-request, per-session and cache
// figures from the caller's own generations (/platform/observability/cost);
// the projection is the budget cycle's, shared with Overview. A figure without
// a source is unavailable, never 0.
import { useConsoleStats } from '@/stores/console-range';
import { EVIDENCE_LIMIT_NOTICE, sourceUnavailable, useEvidenceReport, useEvidenceWindow, type CostReport } from '@/hooks/use-evidence-reports';
import { formatCurrency, formatInt, formatLocalDate, formatUsdSmall, formatPercent } from '@/lib/format';
import type { StatsKeyRow, StatsModelRow } from '@/lib/api-types';
import { DataTable, type DataTableColumn } from '@/components/ui/data-table';
import { ErrorCard } from '@/components/layout/ErrorCard';
import { NO_PROJECTION, projectionReason } from '@/components/observability/AllowancePanel';
import { Bars, DailySpend, Metric, Paged, Panel, ReportHead, SkelKpis, SkelPanel, Swatch, TableDonut, Unavailable, rankClass } from '@/components/analytics/ReportParts';
import { UsageByModel, hiddenNote, sharePct as pct, withSpend } from '@/components/analytics/UsageByModel';
const unit = formatUsdSmall;
type DetailRow = Pick<StatsModelRow, 'model' | 'requests' | 'spend' | 'spend_pct' | 'prev_spend'> & { total: boolean };

// Previous comparable period from the change; null when the backend had no
// baseline (a zero previous period yields a null %, never infinity).
function previous(current: number, change: number | null): number | null {
  return change == null || change <= -1 ? null : current / (1 + change);
}

// Two keys can share a name (rotated personal keys, re-created ek_): the
// repeated names get the id's last 4 characters so the rows stay tellable.
export function keyLabel(row: StatsKeyRow, rows: StatsKeyRow[]): string {
  const name = row.key_alias || 'Unknown key';
  const repeated = rows.filter((r) => (r.key_alias || 'Unknown key') === name).length > 1;
  return repeated && row.id ? `${name} · ${row.id.slice(-4)}` : name;
}

export function Cost() {
  const query = useConsoleStats();
  const costQuery = useEvidenceReport<CostReport>('cost');
  const cost = costQuery.data && !sourceUnavailable(costQuery.data.provenance) ? costQuery.data : undefined;
  const { tooLong } = useEvidenceWindow();
  const head = <ReportHead title="Cost">Understand your spend, what drives it, and which usage change would make a measurable difference.</ReportHead>;
  if (query.isPending) return <>{head}<SkelKpis /><SkelPanel title="Daily spend" chart /><div className="breakdown"><SkelPanel title="Usage by model" className="" /><SkelPanel title="Spend by key" className="" /></div></>;
  if (query.isError || !query.data) return <ErrorCard onRetry={() => void query.refetch()} retrying={query.isFetching} />;
  const { totals, budget, series, models, keys } = query.data;
  const change = totals.deltas.spend_pct;
  const prior = previous(totals.spend, change);
  const p = budget.projection ?? NO_PROJECTION;
  const byModel = withSpend(models).sort((a, b) => b.spend - a.spend);
  const byKey = withSpend(keys).sort((a, b) => b.spend - a.spend);
  const modelSum = models.reduce((sum, row) => sum + row.spend, 0);
  const residual = totals.spend - modelSum;
  const cache = cost?.cache;
  const before = (row: DetailRow) => row.total ? prior : row.prev_spend ?? null;
  const detailColumns: DataTableColumn<DetailRow>[] = [
    { key: 'item', header: 'Item', className: 'entity', cell: (row) => row.total ? <b>All spend</b> : <><Swatch cls={rankClass(byModel.findIndex((m) => m.model === row.model))} />{row.model || 'Unknown model'}</>, sortAccessor: (row) => row.model },
    { key: 'spend', header: 'Spend', className: 'mono', cell: (row) => formatCurrency(row.spend), sortAccessor: (row) => row.spend },
    { key: 'requests', header: 'Requests', className: 'mono', cell: (row) => formatInt(row.requests), sortAccessor: (row) => row.requests },
    { key: 'share', header: 'Share', className: 'mono', cell: (row) => pct(row.spend_pct), sortAccessor: (row) => row.spend_pct },
    { key: 'unit', header: 'Unit cost', className: 'mono', cell: (row) => row.total ? (cost ? unit(cost.cost_per_model_request) : '—') : row.requests ? unit(row.spend / row.requests) : '—', sortAccessor: (row) => row.requests ? row.spend / row.requests : null },
    { key: 'prior', header: 'Previous period', className: 'mono', cell: (row) => { const p = before(row); return p == null ? '—' : formatCurrency(p); }, sortAccessor: before },
    { key: 'change', header: 'Change', className: 'mono', cell: (row) => { const p = before(row); if (p == null) return '—'; const d = row.spend - p; return d ? <span className={`chg ${d > 0 ? 'up' : 'down'}`}>{d > 0 ? '▲' : '▼'} {formatCurrency(Math.abs(d))}</span> : formatCurrency(0); }, sortAccessor: (row) => { const p = before(row); return p == null ? null : row.spend - p; } },
  ];
  const evidence = (loading: string, missing: string) => tooLong ? EVIDENCE_LIMIT_NOTICE : costQuery.isPending ? loading : missing;
  return <>{head}
    <div className="metric-grid">
      <Metric label={p.horizon_end ? `Projected by ${formatLocalDate(p.horizon_end)}` : 'Projected spend'} value={p.spend == null ? undefined : formatCurrency(p.spend)}>{p.spend == null ? projectionReason(p) : `Estimate: this allowance cycle’s rate carried to its reset${p.pct == null ? '' : `, ${pct(p.pct)} of the cap`}. Independent of the selected period.`}</Metric>
      <Metric label="Cost per model request" value={cost ? unit(cost.cost_per_model_request) : undefined}>{cost ? `${formatCurrency(cost.spend)} over ${formatInt(cost.priced_generations)} priced chat generations. ${formatInt(cost.zero_cost_generations)} generations with no recorded cost (subscription traffic) are counted apart and excluded.` : evidence('Loading generation costs…', 'Generation cost evidence is unavailable.')}</Metric>
      <Metric label="Cost per session" value={cost ? unit(cost.cost_per_session) : undefined}>{cost ? `${formatInt(cost.sessions)} observed sessions · ${pct(cost.session_spend_coverage)} of spend carries a session id; the rest is unassigned.` : evidence('Loading sessions…', 'Session attribution is unavailable.')}</Metric>
    </div>
    <section className="panel hero-metric report-gap"><div className="hero-top"><div><h2>Daily spend</h2><p>Recorded spend per UTC day for the selected period</p></div><div className="hero-number"><b>{formatCurrency(totals.spend)}</b><small>{change == null ? 'No comparable previous period' : `${change >= 0 ? '+' : ''}${formatPercent(change)} vs previous period${prior == null ? '' : ` (${formatCurrency(prior)})`}`}</small></div></div><DailySpend series={series} /></section>
    <div className="breakdown">
      <UsageByModel models={models} paged>{Math.abs(residual) >= 0.005 && <p className="report-source-note">{formatCurrency(residual)} of period spend is not attributed to a model.</p>}</UsageByModel>
      <Panel title="Spend by key" subtitle="ACH key names · external keys grouped"><Paged rows={byKey} footer={hiddenNote(keys.length - byKey.length, 'key')}>{(page) => <Bars total={byKey.reduce((s, r) => s + r.spend, 0)} rows={page.map((row) => ({ name: keyLabel(row, byKey), value: row.spend, display: formatCurrency(row.spend) }))} />}</Paged></Panel>
    </div>
    <Panel title="Prompt cache: recorded vs without cache" subtitle="Input cost of priced generations" className="report-gap">{cache && cache.with_cache != null ? <><div className="scoregrid">
      <div><b>{formatCurrency(cache.with_cache)}</b><span>Recorded input cost</span><p>What was billed, with the prompt cache</p></div>
      <div><b>{formatCurrency(cache.without_cache)}</b><span>Hypothetical without cache</span><p>Same input at the full input price</p></div>
      <div><b>{formatCurrency(cache.saving)}</b><span>Estimated saving</span><p>{pct(cache.saving_share)} of the hypothetical cost</p></div>
    </div><p className="report-source-note">Estimate from the AI gateway’s per-model prices, scaled to each model’s recorded rate; covers {formatInt(cache.priced_models)} models.{cache.unpriced_models.length ? ` Not compared (no cache price): ${cache.unpriced_models.join(', ')}.` : ''}</p></> : <Unavailable>{tooLong ? EVIDENCE_LIMIT_NOTICE : costQuery.isPending ? 'Loading cache comparison…' : cache?.reason === 'no_priced_models' ? 'No model in this period has both an input and a cache-read price.' : 'The cache comparison is unavailable: prices or generation evidence could not be read.'}</Unavailable>}</Panel>
    {/* "All spend" is pinned on every page; the model rows sort and page by 10. */}
    <Panel title="Cost detail" subtitle="Monetary ledger for the selected period" className="report-gap"><TableDonut label="Spend by model" slices={byModel.map((row, i) => ({ id: row.model ?? 'unknown', name: row.model || 'Unknown model', value: row.spend, cls: rankClass(i) }))} format={formatCurrency} noun="spend">{(hover) => <DataTable<DetailRow> columns={detailColumns} highlight={hover} rows={byModel.map((row) => ({ ...row, total: false }))} pinned={[{ model: 'All spend', requests: totals.requests, spend: totals.spend, spend_pct: 1, total: true }]} getRowId={(row) => row.total ? '__all' : row.model ?? 'unknown'} defaultSort={{ key: 'spend', dir: 'desc' }} pageSize={10} footer={hiddenNote(models.length - byModel.length, 'model')} />}</TableDonut></Panel>
  </>;
}
