// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// Workloads.tsx — generations by client runtime.
import { EVIDENCE_LIMIT_NOTICE, useEvidenceReport, useEvidenceWindow, sourceUnavailable, type WorkloadReport } from '@/hooks/use-evidence-reports';
import { ErrorCard } from '@/components/layout/ErrorCard';
import { abbreviate, formatDecimal, formatInt, formatPercent, formatUsdSmall } from '@/lib/format';
import { Metric, Panel, ReportHead, SkelKpis, SkelPanel, Swatch, TableDonut, rankClass } from '@/components/analytics/ReportParts';
import { runtimeLabel } from '@/lib/observability-targets';
import { DataTable } from '@/components/ui/data-table';
import type { WorkloadRuntime } from '@/lib/api-types';
export function Workloads() {
  const query = useEvidenceReport<WorkloadReport>('workloads');
  const { tooLong } = useEvidenceWindow();
  const head = <ReportHead title="Workloads" preview>Compare your own clients and interaction patterns without comparing yourself with other people.</ReportHead>;
  if (tooLong) return <>{head}<p className="report-source-note">{EVIDENCE_LIMIT_NOTICE}</p></>;
  if (query.isPending) return <>{head}<SkelKpis /><SkelPanel title="Your client comparison" /></>;
  if (query.isError || !query.data) return <ErrorCard onRetry={() => void query.refetch()} retrying={query.isFetching} />;
  const data = query.data;
  const unavailable = sourceUnavailable(data.provenance);
  const rows = unavailable ? [] : [...data.runtimes].sort((a, b) => b.count - a.count);
  const name = (row: WorkloadRuntime) => row.name === 'unknown' ? 'Unattributed' : runtimeLabel(row.name);
  const cls = (row: WorkloadRuntime) => row.name === 'unknown' ? 'wc-c-other' : rankClass(rows.indexOf(row));
  const attributed = data.runtimes.filter((row) => row.name !== 'unknown');
  const attributedCount = attributed.reduce((sum, row) => sum + row.count, 0);
  const primary = [...attributed].sort((a,b) => b.count-a.count)[0];
  const top = (key: 'cache_read_share' | 'median_input') => attributed.filter((row) => row[key] != null).sort((a, b) => b[key]! - a[key]!)[0];
  const bestCache = unavailable ? undefined : top('cache_read_share');
  const largest = unavailable ? undefined : top('median_input');
  const io = (row: WorkloadRuntime) => row.input_tokens && row.output_tokens ? row.input_tokens / row.output_tokens : null;
  return <>{head}
    <div className="metric-grid"><Metric label="Primary client" value={!unavailable && primary ? runtimeLabel(primary.name) : '—'}>{!unavailable && primary && data.generation_count ? `${formatPercent(primary.count / data.generation_count, 0)} of all observed generations; ${formatInt(attributedCount)} of ${formatInt(data.generation_count)} have client attribution.` : `${formatInt(data.generation_count)} generations observed; client attribution is unavailable.`}</Metric><Metric label="Best cache-read share" value={bestCache ? formatPercent(bestCache.cache_read_share, 0) : undefined}>{bestCache ? `${name(bestCache)} generations in this period.` : 'No client reports cache reads in this period.'}</Metric><Metric label="Largest median context" value={largest ? abbreviate(largest.median_input) : undefined}>{largest ? `${name(largest)} generations.` : 'No client reports input tokens in this period.'}</Metric></div>
    <Panel className="report-gap" title="Your client comparison" subtitle="Ratios are descriptive within each interaction pattern. Counts are generations, not logical requests."><TableDonut label="Generations by client" slices={rows.map((row) => ({ id: row.name, name: name(row), value: row.count, cls: cls(row) }))} format={formatInt} noun="generations">{(hover) => <DataTable columns={[
      { key: 'client', header: 'Client', className: 'entity', cell: (row: WorkloadRuntime) => <><b><Swatch cls={cls(row)} />{name(row)}</b>{row.name === 'unknown' && <small>Client metadata missing</small>}</>, sortAccessor: (row: WorkloadRuntime) => runtimeLabel(row.name) },
      { key: 'count', header: 'Generations', className: 'mono', cell: (row: WorkloadRuntime) => formatInt(row.count), sortAccessor: (row: WorkloadRuntime) => row.count },
      { key: 'share', header: 'Share', className: 'mono', cell: (row: WorkloadRuntime) => data.generation_count ? formatPercent(row.count / data.generation_count, 0) : '—', sortAccessor: (row: WorkloadRuntime) => row.count },
      { key: 'median', header: 'Median context', className: 'mono', cell: (row: WorkloadRuntime) => abbreviate(row.median_input), sortAccessor: (row: WorkloadRuntime) => row.median_input },
      { key: 'cache', header: 'Cache-read share', className: 'mono', cell: (row: WorkloadRuntime) => formatPercent(row.cache_read_share, 0), sortAccessor: (row: WorkloadRuntime) => row.cache_read_share },
      { key: 'io', header: 'Input / output', className: 'mono', cell: (row: WorkloadRuntime) => { const r = io(row); return r == null ? '—' : `${formatDecimal(r, 0)} : 1`; }, sortAccessor: io },
      { key: 'unit', header: 'Cost / generation', className: 'mono', cell: (row: WorkloadRuntime) => row.cost_per_generation == null ? '—' : formatUsdSmall(row.cost_per_generation), sortAccessor: (row: WorkloadRuntime) => row.cost_per_generation },
    ]} rows={rows} getRowId={(row) => row.name} defaultSort={{ key: 'count', dir: 'desc' }} pageSize={10} highlight={hover} empty={unavailable ? 'Runtime evidence is not connected or could not be read.' : 'No generations in the selected period.'} />}</TableDonut></Panel>
    <section className="panel callout report-gap"><b>One useful correction</b><p>Runtime labels describe the client associated with a generation. They do not identify the user's business task. Task comparisons and recommendations are unavailable without additional instrumentation.</p></section>
    <p className="report-source-note">Median context, cache-read share and input/output count only the generations that report them; cost per generation counts priced generations only, so subscription traffic does not read as $0. {data.window.start.slice(0,10)} – {data.window.end.slice(0,10)} · UTC · end exclusive. Parser v{data.runtime_parser_version}{data.provenance.sampled ? ' · capped sample' : ''}. A generation takes its trace's client User-Agent; a trace without one is unknown. No employee ranking or productivity scoring.</p>
  </>;
}
