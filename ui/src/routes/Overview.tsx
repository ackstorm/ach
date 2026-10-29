// Overview.tsx — the core landing page, from LiteLLM alone: spend, tokens,
// gateway operations and latency, the allowance cycle and tokens per day beside
// request outcome, responsiveness and spend by key, requests per day and a
// model table with its donut. With Enterprise on, ee/routes/Overview.tsx
// replaces it (App.tsx Landing).
import type { GatewayLatency, StatsDeltas, StatsKeyRow, StatsModelRow, StatsSeriesPoint } from '@/lib/api-types';
import { abbreviate, formatCurrency, formatInt, formatPercent, formatSeconds, formatDecimal, formatShortDay } from '@/lib/format';
import { useConsoleDateRange, useConsoleStats } from '@/stores/console-range';
import { Delta, previousRange } from '@/components/observability/KpiDelta';
import { useGatewayLatency } from '@/hooks/use-gateway-latency';
import { ChartFrame } from '@/components/charts/ChartFrame';
import { usePersistedChoice } from '@/hooks/use-persisted-choice';
import { ErrorCard } from '@/components/layout/ErrorCard';
import { AllowancePanel } from '@/components/observability/AllowancePanel';
import { TokensChart } from '@/components/observability/TokensChart';
import { Bars, Panel, Segmented, SkelKpis, SkelPanel, SkelRows, Swatch, TableDonut, Unavailable, rankClass } from '@/components/analytics/ReportParts';
import { RangePicker } from '@/components/analytics/RangePicker';
import { DataTable, type DataTableColumn } from '@/components/ui/data-table';

function Spark({ values, color = '' }: { values: number[]; color?: string }) {
  const maximum = Math.max(1, ...values);
  return <span className={`spark ${color}`} aria-hidden="true">{values.slice(-14).map((value, i) => <i key={i} style={{ height: `${Math.max(2, value / maximum * 100)}%` }} />)}</span>;
}

// The latency endpoint reads LiteLLM's latest request rows in the window.
function sampleNote(l: GatewayLatency) {
  return l.sampled ? `latest ${formatInt(l.row_count)} calls` : `${formatInt(l.row_count)} calls`;
}

function Outcome({ latency }: { latency?: GatewayLatency }) {
  if (!latency) return <Panel title="Request outcome"><SkelRows n={3} /></Panel>;
  const ok = latency.outcomes.find((o) => o.status === 'success')?.count ?? 0;
  const failed = latency.outcomes.filter((o) => o.status !== 'success').reduce((sum, o) => sum + o.count, 0);
  const slices = [{ id: 'success', name: 'Success', value: ok, cls: 'wc-ok' }, { id: 'failure', name: 'Failure', value: failed, cls: 'wc-s-5' }];
  return <Panel title="Request outcome" subtitle={latency.available ? sampleNote(latency) : undefined}>
    {!latency.available ? <Unavailable>The gateway request log could not be read.</Unavailable>
      : <TableDonut label="Request outcome" slices={slices} format={formatInt} noun="calls">{(hover) => <table className="outcome-legend"><tbody>{slices.map((s) => <tr key={s.id} data-row-id={s.id} className={hover === s.id ? 'on' : undefined}><td><Swatch cls={s.cls} />{s.name}</td><td>{formatInt(s.value)} · {formatPercent(ok + failed ? s.value / (ok + failed) : null)}</td></tr>)}</tbody></table>}</TableDonut>}
  </Panel>;
}

function Responsiveness({ latency }: { latency?: GatewayLatency }) {
  const l = latency?.available ? latency.latency : null;
  return <Panel title="Responsiveness" subtitle={latency?.available ? sampleNote(latency) : undefined}>
    {!latency ? <SkelRows n={2} /> : !l ? <Unavailable>The gateway request log could not be read.</Unavailable> : <>
      <div className="scoregrid"><div><b>{formatSeconds(l.p50_ms, 2)}</b><span>P50 latency</span></div><div><b>{formatSeconds(l.p95_ms, 2)}</b><span>P95 latency</span></div><div><b>{formatDecimal(l.tokens_per_sec_p50, 0)}</b><span>Output tok/s P50</span></div></div>
      <p className="report-source-note">From LiteLLM's request log{latency.sampled ? ': the latest calls in the period, not the whole period' : ''}.</p></>}
  </Panel>;
}

function SpendByKey({ keys }: { keys: StatsKeyRow[] }) {
  const rows = [...keys].sort((a, b) => b.spend - a.spend || b.requests - a.requests);
  return <Panel title="Spend by key" subtitle="Your keys">{rows.length ? <Bars color="purple" share={false} rows={rows.slice(0, 6).map((k) => ({ name: k.key_alias || 'Unnamed key', value: k.spend, display: `${formatCurrency(k.spend)} · ${formatInt(k.requests)} calls` }))} /> : <Unavailable>No key activity in this period.</Unavailable>}</Panel>;
}

// Daily requests with the failed part in red at the base.
function RequestsPerDay({ series }: { series: StatsSeriesPoint[] }) {
  const n = series.length;
  const max = Math.max(1, ...series.map((r) => r.requests));
  const failed = series.reduce((sum, r) => sum + r.failed, 0);
  return <Panel title="Requests per day" subtitle={`${formatInt(failed)} failed, in red`} className="report-gap">
    {!n ? <Unavailable>No activity in this period.</Unavailable> : <div className="runway-chart" role="img" aria-label={`Requests per day, ${n} days, ${formatInt(failed)} failed`}><ChartFrame wide={1240} max={max} format={formatInt} xLabels={[formatShortDay(series[0].date), formatShortDay(series[n - 1].date)]} hover={{ n, at: (i) => (i + 0.5) / n, tip: (i) => ({ title: formatShortDay(series[i].date), rows: [{ label: 'Requests', value: formatInt(series[i].requests), color: 'var(--green)' }, { label: 'Failed', value: formatInt(series[i].failed), color: 'var(--red)' }] }) }}>{(f) => series.map((r, i) => { const x = f.x((i + 0.15) / n); const w = f.x(0.7 / n) - f.left; return <g key={r.date}><rect x={x} width={w} y={f.y(r.requests)} height={f.bottom - f.y(r.requests)} fill="var(--green)" opacity=".8" /><rect x={x} width={w} y={f.y(r.failed)} height={f.bottom - f.y(r.failed)} fill="var(--red)" /></g>; })}</ChartFrame></div>}
  </Panel>;
}

// The donut's metric; the table shows all three.
const METRICS = ['Cost', 'Tokens', 'Calls'] as const;
const METRIC: Record<typeof METRICS[number], [(row: StatsModelRow) => number, (n: number) => string, string]> = {
  Cost: [(row) => row.spend, formatCurrency, 'spend'],
  Tokens: [(row) => row.total_tokens, abbreviate, 'tokens'],
  Calls: [(row) => row.requests, formatInt, 'calls'],
};

function ModelUsage({ models }: { models: StatsModelRow[] }) {
  const [metric, setMetric] = usePersistedChoice('usage-by-model', METRICS, 'Cost');
  const [value, format, noun] = METRIC[metric];
  // LiteLLM logs MCP tool calls as 'MCP: <tool>' model groups: tool calls, not models.
  const used = models.filter((row) => row.requests > 0 && !row.model?.startsWith('MCP: '));
  const ranked = [...used].sort((a, b) => value(b) - value(a));
  const total = ranked.reduce((sum, row) => sum + value(row), 0);
  const cls = (row: StatsModelRow) => rankClass(ranked.indexOf(row));
  const id = (row: StatsModelRow) => row.model ?? 'unknown';
  const columns: DataTableColumn<StatsModelRow>[] = [
    { key: 'model', header: 'Model', className: 'entity', cell: (row) => <><Swatch cls={cls(row)} />{row.model || 'Unknown model'}</>, sortAccessor: (row) => row.model },
    { key: 'spend', header: 'Spend', className: 'mono', cell: (row) => formatCurrency(row.spend), sortAccessor: (row) => row.spend },
    { key: 'tokens', header: 'Tokens', className: 'mono', cell: (row) => abbreviate(row.total_tokens), sortAccessor: (row) => row.total_tokens },
    { key: 'calls', header: 'Calls', className: 'mono', cell: (row) => formatInt(row.requests), sortAccessor: (row) => row.requests },
    { key: 'share', header: `Share of ${noun}`, className: 'mono', cell: (row) => formatPercent(total ? value(row) / total : null), sortAccessor: value },
  ];
  return <Panel title="Usage by model" subtitle={`Public model alias · donut by ${noun}`} className="report-gap" action={<Segmented label="Usage by model metric" options={METRICS} value={metric} onChange={setMetric} />}>
    <TableDonut label={`Usage by model, ${noun}`} slices={ranked.map((row) => ({ id: id(row), name: row.model || 'Unknown model', value: value(row), cls: cls(row) }))} format={format} noun={noun}>
      {(hover) => <DataTable key={metric} columns={columns} rows={ranked} getRowId={id} highlight={hover} defaultSort={{ key: metric === 'Cost' ? 'spend' : metric === 'Tokens' ? 'tokens' : 'calls', dir: 'desc' }} pageSize={10} empty="No model calls in this period." />}
    </TableDonut>
  </Panel>;
}

export function Overview() {
  const query = useConsoleStats();
  const range = useConsoleDateRange();
  const latencyQuery = useGatewayLatency(range);
  const prevLatency = useGatewayLatency(previousRange(range)).data;
  const latency: GatewayLatency | undefined = latencyQuery.isError ? { available: false, sampled: false, row_count: 0, latency: null, outcomes: [] } : latencyQuery.data;
  const head = <div className="head"><div><h1>Overview</h1><p>Your personal AI usage — spend, tokens and the models behind them.</p></div><div className="actions"><RangePicker /></div></div>;
  if (query.isPending) return <>{head}<SkelKpis n={4} className="kpis" card="kpi" /><div className="overview-grid"><div className="overview-main"><SkelPanel title="Allowance" chart className="" /><SkelPanel title="Tokens per day" chart className="" /></div><SkelPanel title="Request outcome" className="" /></div></>;
  if (query.isError || !query.data) return <ErrorCard onRetry={() => void query.refetch()} retrying={query.isFetching} />;
  const { totals, budget, series, models, keys } = query.data;
  const deltas: Partial<StatsDeltas> = totals.deltas ?? {};
  const l = latency?.available ? latency.latency : null;
  // Both medians come from each period's latest calls; no comparison without both.
  const prevP50 = prevLatency?.available ? prevLatency.latency?.p50_ms : null;
  const latencyPct = l?.p50_ms != null && prevP50 ? l.p50_ms / prevP50 - 1 : null;
  // Spend KPI = this cycle's allowance spend (the enforced tag, same figure as
  // the allowance panel), not the page range.
  const cycle = budget.source !== 'unknown' && budget.current != null;
  return <>
    {head}
    <div className="kpis">
      <div className="panel kpi"><div className="khead"><span className="badge">$</span>Spend</div><div className="value">{formatCurrency(cycle ? budget.current : totals.spend)}{cycle && budget.max_budget != null && <span className="of"> / {formatCurrency(budget.max_budget)}</span>}</div><div className="delta">{!cycle ? 'Recorded spend' : budget.max_budget ? `${formatPercent(budget.current! / budget.max_budget, 0)} of this cycle’s allowance` : 'Spent this cycle'}</div><Delta pct={deltas.spend_pct} bad /><Spark values={series.map((row) => row.spend)} /></div>
      <div className="panel kpi"><div className="khead"><span className="badge">⬡</span>Tokens</div><div className="value">{abbreviate(totals.tokens)}</div><div className="delta">{abbreviate(totals.input_tokens)} input · {abbreviate(totals.output_tokens)} output</div><Delta pct={deltas.tokens_pct} /><Spark color="blue" values={series.map((row) => row.tokens)} /></div>
      <div className="panel kpi"><div className="khead"><span className="badge">↗</span>Gateway operations</div><div className="value">{formatInt(totals.requests)}</div><div className="delta">{formatInt(totals.failed_requests)} failed</div><Delta pct={deltas.requests_pct} /><Spark values={series.map((row) => row.requests)} /></div>
      <div className="panel kpi"><div className="khead"><span className="badge">◷</span>Median latency</div><div className="value">{formatSeconds(l?.p50_ms, 2)}</div><div className="delta">{l && latency ? `P95 ${formatSeconds(l.p95_ms, 2)} · ${sampleNote(latency)}` : latency ? 'Latency unavailable' : 'Loading…'}</div><Delta pct={latencyPct} bad /></div>
    </div>
    <div className="overview-grid"><div className="overview-main"><AllowancePanel budget={budget} />
    <section className="panel forecast"><div className="forecast-head"><div><h2>Tokens per day</h2><p>{query.data.range.start} – {query.data.range.end} · UTC · input and output</p></div><div className="forecast-meta"><div><span>Input</span><b>{abbreviate(totals.input_tokens)}</b></div><div><span>Output</span><b>{abbreviate(totals.output_tokens)}</b></div></div></div><TokensChart series={series} /><div className="legend"><span><i style={{ background: 'var(--green)' }} />Input tokens</span><span><i style={{ background: 'var(--blue)' }} />Output tokens</span></div></section></div>
      <div className="overview-side"><Outcome latency={latency} /><Responsiveness latency={latency} /><SpendByKey keys={keys} /></div></div>
    <RequestsPerDay series={series} />
    <ModelUsage models={models} />
  </>;
}
