// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// overview.tsx — the generation-evidence parts of the Enterprise Overview:
// average input, signals, input/output hero, top apps, tool uses, work
// classification, the Traces insight and the recent-sessions feed.
import type { CSSProperties } from 'react';
import { Link } from 'react-router';
import { ArrowRight } from 'lucide-react';
import { EVIDENCE_LIMIT_NOTICE, sourceUnavailable, useEvidenceReport, useEvidenceWindow, type ClassificationReport, type ContextReport, type WorkloadReport } from '@/hooks/use-evidence-reports';
import type { ClassificationCategory, WorkloadRuntime } from '@/lib/api-types';
import { abbreviate, formatCurrency, formatInt, formatLocalDateTime, formatTokens, formatPercent, formatDecimal, formatUsdSmall, formatDuration } from '@/lib/format';
import { targetStatus, traceOutcome } from '@/lib/status';
import { cacheStatus, ratioStatus, runtimeLabel, type TargetStatus } from '@/lib/observability-targets';
import { StatusPill } from '@/components/ui/StatusPill';
import { usePersistedChoice } from '@/hooks/use-persisted-choice';
import { Bars, Panel, Segmented, SkelRows, Swatch, TableDonut } from '@/components/analytics/ReportParts';
import { DataTable, type DataTableColumn } from '@/components/ui/data-table';
import { useTraces } from './hooks/use-traces';
import { useSessions } from './hooks/use-sessions';
import { workClass, workDomain, workLabel } from './lib/work-labels';

// Work classification: the classifier's taxonomy v3 per session.
const WORK_METRICS = ['Sessions', 'Tokens', 'Cost'] as const;
const WORK_METRIC: Record<typeof WORK_METRICS[number], [(row: ClassificationCategory) => number, (n: number) => string]> = {
  Sessions: [(row) => row.sessions, formatInt],
  Tokens: [(row) => row.input_tokens + row.output_tokens, abbreviate],
  Cost: [(row) => row.cost, formatCurrency],
};

// Donut colour = category (lib/work-labels workClass); trivial is muted and
// last. The table is the legend with every metric (TableDonut links hover).
type WorkRow = ClassificationCategory & { name: string; domain: string; value: number; share: number | null; cls: string };

const WORK_COLUMNS: DataTableColumn<WorkRow>[] = [
  { key: 'category', header: 'Category', className: 'entity', cell: (r) => <><Swatch cls={r.cls} />{r.name}</>, sortAccessor: (r) => r.name },
  { key: 'share', header: 'Share', className: 'mono', cell: (r) => formatPercent(r.share), sortAccessor: (r) => r.share },
  { key: 'sessions', header: 'Sessions', className: 'mono', cell: (r) => formatInt(r.sessions), sortAccessor: (r) => r.sessions },
  { key: 'generations', header: 'Generations', className: 'mono', cell: (r) => formatInt(r.generations), sortAccessor: (r) => r.generations },
  { key: 'tokens', header: 'Tokens', className: 'mono', cell: (r) => abbreviate(r.input_tokens + r.output_tokens), sortAccessor: (r) => r.input_tokens + r.output_tokens },
  { key: 'cost', header: 'Cost', className: 'mono', cell: (r) => formatCurrency(r.cost), sortAccessor: (r) => r.cost },
  { key: 'per', header: '$/session', className: 'mono', cell: (r) => r.sessions ? formatUsdSmall(r.cost / r.sessions) : '—', sortAccessor: (r) => r.sessions ? r.cost / r.sessions : null },
];

export function WorkClassification({ tooLong }: { tooLong: boolean }) {
  const [metric, setMetric] = usePersistedChoice('work-classification', WORK_METRICS, 'Sessions');
  const query = useEvidenceReport<ClassificationReport>('classification');
  const report = query.data && !sourceUnavailable(query.data.provenance) ? query.data : undefined;
  const coverage = report?.coverage;
  const [value, format] = WORK_METRIC[metric];
  const raw = (report?.categories ?? []).map((row) => ({ ...row, domain: workDomain(row.category, row.domain), name: workLabel(row.category), value: value(row) }));
  const total = raw.reduce((sum, row) => sum + row.value, 0);
  const base = raw.map((row) => ({ ...row, share: total ? row.value / total : null }));
  const sorted = [...base].sort((a, b) => Number(a.category === 'trivial') - Number(b.category === 'trivial') || b.value - a.value);
  const rows: WorkRow[] = sorted.map((row) => ({ ...row, cls: workClass(row.category, row.domain) }));
  const body = tooLong ? EVIDENCE_LIMIT_NOTICE : query.isPending ? null : !coverage ? 'Work classification is unavailable right now.' : !coverage.labelled || !rows.length ? 'Not classified yet.' : null;
  return <Panel title="Work classification" subtitle={`Per session · share of ${metric.toLowerCase()}`} className="report-gap work-classification" action={<Segmented label="Work classification metric" options={WORK_METRICS} value={metric} onChange={setMetric} />}>
    {query.isPending && !tooLong ? <SkelRows /> : body ? <p className="report-notice">{body}</p> : <TableDonut label={`Work classification by ${metric.toLowerCase()}`} slices={rows.map((r) => ({ id: r.category, name: r.name, value: r.value, cls: r.cls }))} format={format} noun={metric.toLowerCase()}>{(hover) => <DataTable key={metric} className="wc-table" columns={WORK_COLUMNS} rows={rows.filter((r) => r.category !== 'trivial')} pinnedEnd={rows.filter((r) => r.category === 'trivial')} getRowId={(r) => r.category} highlight={hover} defaultSort={{ key: 'share', dir: 'desc' }} rowClassName={(r) => r.category === 'trivial' ? 'muted' : undefined} />}</TableDonut>}
    {coverage && !tooLong && <p className="report-source-note">{formatInt(coverage.labelled)} of {formatInt(coverage.sessions)} sessions classified · taxonomy {report?.taxonomy}{coverage.sessionless_generations > 0 && ` · ${formatInt(coverage.sessionless_generations)} generations without a session are not classified`}</p>}
  </Panel>;
}

function useTraceNotice() {
  const traces = useTraces();
  const { tooLong } = useEvidenceWindow();
  const notice = tooLong ? EVIDENCE_LIMIT_NOTICE : traces.isPending ? 'Loading trace evidence…' : traces.isError ? 'Trace data could not be loaded.' : traces.data && sourceUnavailable(traces.data.provenance) ? 'Trace data is not connected.' : 'No activity in the trace period.';
  return { traces, notice };
}

export function TracesInsight() {
  const { traces, notice } = useTraceNotice();
  return <Link className="insight" to="/analytics/traces"><span className="symbol">⌘</span><span><b>Traces</b><p>{traces.data?.total_count != null ? `${formatInt(traces.data.total_count)} traces across the selected period.` : notice}</p></span><span className="go" aria-hidden="true"><ArrowRight size={16} /></span></Link>
}

export function TracesFeed() {
  const sessions = useSessions(0, { key: 'last_activity_at', dir: 'desc' });
  const { tooLong } = useEvidenceWindow();
  const sessionNotice = tooLong ? EVIDENCE_LIMIT_NOTICE : sessions.isPending ? 'Loading session evidence…' : sessions.isError ? 'Session data could not be loaded.' : sessions.data && sourceUnavailable(sessions.data.provenance) ? 'Session data is not connected.' : 'No sessions in the trace period.';
  return <section className="panel"><div className="panel-title"><h2>Traces</h2><p>Recent sessions</p></div><div className="feed">{!sessions.isError && sessions.data?.sessions.length ? sessions.data.sessions.slice(0, 10).map((session) => { const { label, tone } = traceOutcome(session.status); return <div className="feedrow" key={`${session.project_id}:${session.session_id}`}><time className="time" dateTime={session.last_activity_at} title={session.last_activity_at}>{formatLocalDateTime(session.last_activity_at)}</time><Link className="mono trace-link" title={session.session_id} to={`/analytics/traces?session=${encodeURIComponent(session.session_id)}&project=${encodeURIComponent(session.project_id)}`}>{session.session_id.slice(0, 8)}</Link><span>{formatInt(session.turns)} turns</span><span className="mono" title={session.input_tokens == null || session.output_tokens == null ? undefined : `${formatInt(session.input_tokens)} in · ${formatInt(session.output_tokens)} out`}>{formatTokens(session.input_tokens, session.output_tokens, true)}</span><span className="mono">{formatDuration(session.duration_ms)}</span><StatusPill tone={tone}>{label}</StatusPill></div>; }) : <p className="report-notice">{sessionNotice}</p>}</div></section>;
}

function Target({ status, ok, over }: { status: TargetStatus; ok: string; over: string }) {
  const { label, tone } = targetStatus(status, ok, over);
  return <StatusPill tone={tone}>{label}</StatusPill>;
}

// Top apps metric: generations (the evidence unit), tokens or recorded cost.
const APP_METRICS = ['Generations', 'Tokens', 'Cost'] as const;
const APP_METRIC: Record<typeof APP_METRICS[number], [(row: WorkloadRuntime) => number, (n: number) => string, string]> = {
  Generations: [(row) => row.count, formatInt, 'Generations by client'],
  Tokens: [(row) => (row.input_tokens ?? 0) + (row.output_tokens ?? 0), abbreviate, 'Tokens (input + output) by client'],
  Cost: [(row) => row.cost ?? 0, formatCurrency, 'Recorded cost by client; subscription traffic records $0'],
};

// Verified signals: each one is a count or share the caller's own generations
// carry in this period; a signal whose source is missing is not shown.
function Signals({ context }: { context: ContextReport }) {
  const large = context.context_bands.slice(3).reduce((sum, band) => sum + band.count, 0);
  const share = context.cache_read_share?.value ?? null;
  const items = [
    { mark: '▲', title: large ? 'Large requests observed' : 'No large requests', body: `${formatInt(large)} generations carried 200K+ input tokens`, tone: large ? 'red' : '' },
    share != null && { mark: '△', title: `Prompt cache-read share is ${cacheStatus(share) === 'over' ? 'low' : 'high'}`, body: `${formatPercent(share)} of input read from the provider cache`, tone: cacheStatus(share) === 'over' ? 'amber' : '' },
    context.skill_invocation_count != null && { mark: '◷', title: context.skill_invocation_count ? 'Skills are visible' : 'No skills observed', body: `${formatInt(context.skill_invocation_count)} skill loads in this period`, tone: '' },
    context.subagent_invocation_count != null && { mark: '⊘', title: context.subagent_invocation_count ? 'Delegation is visible' : 'No delegation observed', body: `${formatInt(context.subagent_invocation_count)} subagent starts in this period`, tone: '' },
  ].filter((item) => !!item);
  return <section className="panel status-band" aria-label="Verified signals" style={{ '--signals': items.length } as CSSProperties}>
    <div className="status-lead"><span className="ring" aria-hidden="true">!</span>Verified signals</div>
    {items.map((item) => <div className="status-item" key={item.title}><span className={`mark ${item.tone}`} aria-hidden="true">{item.mark}</span><span><b>{item.title}</b><small>{item.body}</small></span></div>)}
    <div className="status-action"><Link className="btn accent" to="/analytics/context">Review all <ArrowRight size={14} aria-hidden="true" /></Link></div>
  </section>;
}

// The evidence hooks, shared by the parts below (one request per report).
function useEvidence() {
  const contextQuery = useEvidenceReport<ContextReport>('context');
  const workloadsQuery = useEvidenceReport<WorkloadReport>('workloads');
  const { tooLong } = useEvidenceWindow();
  const context = contextQuery.data && !sourceUnavailable(contextQuery.data.provenance) ? contextQuery.data : undefined;
  const workloads = workloadsQuery.data && !sourceUnavailable(workloadsQuery.data.provenance) ? workloadsQuery.data : undefined;
  return { contextQuery, workloadsQuery, context, workloads, tooLong };
}

// The fourth KPI card: average generation input.
export function EvidenceKpi() {
  const { context } = useEvidence();
  const summary = context?.input_summary;
  return <div className="panel kpi"><div className="khead"><span className="badge">◎</span>Average generation input</div><div className="value">{abbreviate(summary?.mean)}</div><div className="delta">{context ? `${formatInt(context.input_coverage)} generations with input usage` : 'Generation measurements unavailable'}</div></div>;
}

// Verified signals and the input/output hero, under the KPI row.
export function EvidenceHero({ range }: { range: { start: string; end: string } }) {
  const { context, tooLong } = useEvidence();
  const ratio = context?.token_ratio?.uncached_value ?? null;
  const share = context?.cache_read_share?.value ?? null;
  const reasoning = context?.reasoning?.share ?? null;
  const summary = context?.input_summary;
  return <>
    {context && !tooLong && <Signals context={context} />}
    {tooLong ? <section className="panel report-gap"><p className="report-notice">{EVIDENCE_LIMIT_NOTICE}</p></section> : <section className="panel ratio-hero" aria-label="Input and output activity">
      <div className="ratio-main"><span className="label">Uncached input / output</span><strong className={ratioStatus(ratio) === 'over' ? 'red' : undefined}>{ratio == null ? '—' : `${formatDecimal(ratio, 1)} : 1`}</strong><p>{abbreviate(context?.token_ratio?.uncached_input_tokens)} uncached input / {abbreviate(context?.token_ratio?.output_tokens)} output · {formatInt(context?.token_ratio?.coverage ?? 0)} generations with both measured. With cache reads: {context?.token_ratio?.value == null ? '—' : `${formatDecimal(context.token_ratio.value, 1)} : 1`}.</p><p><Target status={ratioStatus(ratio)} ok="Within target · ≤ 10:1" over="Above target · > 10:1" /></p></div>
      <div className="ratio-main ratio-score"><span className="label">Prompt cache-read share</span><strong className={cacheStatus(share) === 'over' ? 'red' : undefined}>{formatPercent(share)}</strong><p>{share == null ? 'Cache-read coverage is unavailable for this generation population.' : `${formatInt(context?.cache_read_share.coverage)} generations with matched input and cache telemetry; cached input is counted once.`} Target ≥ 80%, a dashboard choice, not a universal benchmark.</p><p><Target status={cacheStatus(share)} ok="Within target · ≥ 80%" over="Below target · < 80%" /></p></div>
      <div className="ratio-main"><span className="label">Average generation input</span><strong>{abbreviate(summary?.mean)}</strong><p>{context ? `Over ${formatInt(context.input_coverage)} generations with input usage.` : 'Generation measurements unavailable.'}</p></div>
      <div className="ratio-meta"><div><span>Median input</span><b>{abbreviate(summary?.median)}</b></div><div><span>Maximum input</span><b>{abbreviate(summary?.max)}</b></div><div><span>Reasoning share of output</span><b>{formatPercent(reasoning)}</b></div><div><span>Reasoning measured on</span><b>{context ? `${formatInt(context.reasoning.coverage)} generations` : '—'}</b></div><div><span>Period</span><b>{range.start} – {range.end}</b></div></div>
    </section>}
  </>;
}

// Top apps and tool uses in the Runtime panel.
export function RuntimeInsights() {
  const [appMetric, setAppMetric] = usePersistedChoice('top-apps', APP_METRICS, 'Generations');
  const { contextQuery, workloadsQuery, context, workloads, tooLong } = useEvidence();
  const [appValue, appFormat, appCaption] = APP_METRIC[appMetric];
  const apps = [...workloads?.runtimes ?? []].sort((a, b) => appValue(b) - appValue(a));
  const tools = context?.invocations ?? [];
  return <>
        <div className="insight"><span className="symbol">▣</span><span><div className="insight-head"><b>Top apps</b>{apps.length > 0 && <Segmented label="Top apps metric" options={APP_METRICS} value={appMetric} onChange={setAppMetric} />}</div>{apps.length ? <Bars total={apps.reduce((sum, row) => sum + appValue(row), 0)} rows={apps.slice(0, 5).map((row) => ({ name: runtimeLabel(row.name), value: appValue(row), display: appFormat(appValue(row)) }))} /> : <p>{tooLong ? EVIDENCE_LIMIT_NOTICE : workloadsQuery.isPending ? 'Loading client attribution…' : 'Client attribution is unavailable.'}</p>}<p>{appCaption}. Clients come from the User-Agent; missing attribution stays Unknown.</p></span></div>
        <div className="insight"><span className="symbol">✦</span><span><b>Tool uses</b>{tools.length ? <><Bars color="purple" total={context?.invocation_count ?? tools.reduce((sum, row) => sum + row.count, 0)} rows={tools.slice(0, 5).map((row) => ({ name: row.name, value: row.count }))} /><p>Tool calls emitted by the model: {formatInt(context?.invocation_count)} across {formatInt(tools.length)}{context?.invocations_truncated ? '+' : ''} distinct tools. A call is not a successful result.</p></> : <p>{tooLong ? EVIDENCE_LIMIT_NOTICE : contextQuery.isPending ? 'Loading tool calls…' : context ? 'No tool calls in this period.' : 'Tool calls are unavailable.'}</p>}</span></div>
  </>;
}
