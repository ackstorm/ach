// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// Performance.tsx — generation timing from the caller's own traces
// (/platform/observability/latency) plus gateway metrics from Thanos
// (/platform/observability/performance). Unmeasured generations are excluded
// from the percentiles and shown as coverage.
import { EVIDENCE_LIMIT_NOTICE, useEvidenceReport, useEvidenceWindow, type GenerationLatency, type PerformanceReport } from '@/hooks/use-evidence-reports';
import { DataTable, type DataTableColumn } from '@/components/ui/data-table';
import { Metric, Panel, ReportHead, SkelChart, SkelKpis, SkelRows, Swatch, TableDonut, Unavailable, rankClass } from '@/components/analytics/ReportParts';
import { formatSeconds, formatDecimal, formatInt, formatPercent } from '@/lib/format';
const duration = (value: number | null | undefined) => formatSeconds(value);
const rate = (value: number | null | undefined) => formatDecimal(value, 1);
type ModelTiming = GenerationLatency['by_model'][number];
const modelColumns = (cls: (row: ModelTiming) => string): DataTableColumn<ModelTiming>[] => [
  { key: 'model', header: 'Model', className: 'entity', cell: (row) => <b><Swatch cls={cls(row)} />{row.model}</b>, sortAccessor: (row) => row.model },
  { key: 'requests', header: 'Generations', className: 'mono', cell: (row) => formatInt(row.requests), sortAccessor: (row) => row.requests },
  { key: 'p50', header: 'P50', className: 'mono', cell: (row) => duration(row.p50_ms), sortAccessor: (row) => row.p50_ms },
  { key: 'p95', header: 'P95', className: 'mono', cell: (row) => duration(row.p95_ms), sortAccessor: (row) => row.p95_ms },
  { key: 'ttft', header: 'TTFT P50', className: 'mono', cell: (row) => duration(row.ttft_p50_ms), sortAccessor: (row) => row.ttft_p50_ms },
  { key: 'tps', header: 'Output tok/s', className: 'mono', cell: (row) => rate(row.tokens_per_sec_p50), sortAccessor: (row) => row.tokens_per_sec_p50 },
  { key: 'errors', header: 'Error observed', className: 'mono', cell: (row) => row.requests ? formatPercent(row.failed / row.requests) : '—', sortAccessor: (row) => row.requests ? row.failed / row.requests : null },
];
export function Performance() {
  const trends = useEvidenceReport<PerformanceReport>('performance');
  const { range, tooLong } = useEvidenceWindow();
  const query = useEvidenceReport<GenerationLatency>('latency');
  const data = query.data?.available ? query.data : undefined;
  const histogram = data?.source_coverage?.timing === 0 ? undefined : data?.histogram;
  const coverage = data?.source_coverage;
  const histogramMax = Math.max(1, ...(histogram?.map((bin) => bin.count) ?? []));
  const rows = data?.by_model ?? [];
  const queue = trends.data?.queue_p95;
  const byCount = [...rows].sort((a, b) => b.requests - a.requests);
  const cls = (row: ModelTiming) => rankClass(byCount.indexOf(row));
  // TTFT only exists for streamed calls; say how many that is.
  const ttftNote = !coverage ? 'Time from the request to the first streamed chunk.' : coverage.ttft ? `First streamed chunk · ${formatInt(coverage.ttft)} of ${formatInt(coverage.generation_count)} generations streamed.` : 'No streamed generations in this period, so no first-token time.';
  return <><ReportHead title="Performance">Understand responsiveness, throughput, and which changes can make your own executions faster.</ReportHead>
    {tooLong ? <p className="report-source-note">{EVIDENCE_LIMIT_NOTICE}</p> : query.isPending ? null : query.isError ? <div role="alert" className="report-source-note">Request timing could not be loaded. <button type="button" className="btn sm" disabled={query.isFetching} onClick={() => void query.refetch()}>Retry timing</button></div> : !data ? <p className="report-source-note">Latency data is unavailable. {'The timing source could not be read.'}</p> : null}
    {query.isPending ? <><SkelKpis /><SkelKpis /></> : <><div className="metric-grid"><Metric label="Median latency" value={duration(data?.latency?.p50_ms)}>Observed generation start to completion.</Metric><Metric label="P95 latency" value={duration(data?.latency?.p95_ms)}>95% of timed generations finished within this time.</Metric><Metric label="Median output tok/s" value={rate(data?.latency?.tokens_per_sec_p50)}>End-to-end: output tokens / total duration. Not streaming speed.</Metric></div>
    <div className="metric-grid"><Metric label="Median time to first token" value={duration(data?.latency?.ttft_p50_ms)}>{ttftNote}</Metric><Metric label="P95 time to first token" value={duration(data?.latency?.ttft_p95_ms)}>95% of streamed generations started answering within this time.</Metric><Metric label="Gateway queue P95" value={queue?.value == null ? undefined : formatSeconds(queue.value * 1000, 2)}>{queue?.value == null ? 'No verified queue measurement for this period.' : 'Time waiting in the AI gateway before the provider call. Histogram estimate.'}</Metric></div></>}
    <Panel className="latency-distribution report-gap" title="Your latency distribution" subtitle={histogram ? 'Observed generations by duration' : 'Observed generation duration'}>{query.isPending ? <SkelChart /> : histogram ? <><div className="dist" role="img" aria-label="Observed generation duration distribution">{histogram.map((bin) => <i key={bin.label} title={`${bin.label}: ${bin.count} generations`} style={{height:`${bin.count / histogramMax * 100}%`,background:`var(--${bin.lower_ms >= 5000 ? 'amber' : 'blue'})`}} />)}</div><div className="latency-buckets">{histogram.map((bin) => <span key={bin.label}>{bin.label}<b>{formatInt(bin.count)}</b></span>)}</div></> : <Unavailable>No generation duration measurements are available for this period.</Unavailable>}{data && <p className="report-coverage">{coverage ? `${formatInt(coverage.timing)} of ${formatInt(coverage.generation_count)} generations measured` : `${formatInt(data!.row_count ?? 0)} observed generations`}{' · '}{`${range.start} – ${range.end} UTC${data.sampled ? ' · capped sample' : ' · full-window aggregate'}`} · Timing excludes upstream queueing outside the trace. Unmeasured generations are excluded from the distribution and percentiles.</p>}</Panel>
    <Panel title="Performance by model" subtitle="Observed generations per model in the period." className="report-gap">{query.isPending ? <SkelRows /> : rows.length > 0 && <TableDonut label="Generations by model" slices={byCount.map((row) => ({ id: row.model, name: row.model, value: row.requests, cls: cls(row) }))} format={formatInt} noun="generations">{(hover) => <DataTable columns={modelColumns(cls)} rows={rows} getRowId={(row) => row.model} defaultSort={{ key: 'requests', dir: 'desc' }} pageSize={10} highlight={hover} />}</TableDonut>}{!rows.length && !query.isPending && <Unavailable>{data?.by_model_reason ? 'Model timing breakdown is unavailable. Summary timing remains available.' : data ? 'No observed generations in this window.' : 'Model timing is unavailable.'}</Unavailable>}{data?.by_model_truncated && <p className="report-source-note">Showing the top 100 models by observed generation count. Summary timing includes all models.</p>}</Panel>
    <Panel title="Personal metrics for selected period" subtitle="Thanos estimates · independent source. Increases may be fractional and P95 comes from histogram buckets, so these are not accounting figures." className="report-gap"><div className="scoregrid">{([['latency_p95','Gateway P95 · selected period'],['failures','Failures · selected period'],['mcp_calls','MCP calls · selected period']] as const).map(([key,label]) => <div key={key}><b>{trends.data?.[key].value == null ? '—' : `${formatDecimal(trends.data[key].value, 2)}${key === 'latency_p95' ? ' s' : ''}`}</b><span>{label}</span><p>{trends.data?.[key].value == null ? 'No verified metric available' : trends.data?.[key].reason ? 'Partial source response — estimate may omit data' : key === 'latency_p95' ? 'Histogram estimate' : 'Extrapolated increase, not an exact count'}</p></div>)}</div></Panel>
  </>;
}
