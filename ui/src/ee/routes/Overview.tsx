// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// Overview.tsx — the Enterprise landing page (approved Overview v3).
// Card order: Spend → Tokens → Gateway operations → Average generation input.
// The LiteLLM figures sit beside the generation evidence from ee/overview.tsx
// (signals, input/output hero, top apps, tool uses, work classification,
// sessions). The allowance is the enforced user:<email> tag with the backend
// projection to reset_at.
import { ArrowRight } from 'lucide-react';
import type { StatsDeltas } from '@/lib/api-types';
import { useEvidenceWindow } from '@/hooks/use-evidence-reports';
import { abbreviate, formatCurrency, formatInt, formatPercent } from '@/lib/format';
import { useConsoleStats } from '@/stores/console-range';
import { EvidenceHero, EvidenceKpi, RuntimeInsights, TracesFeed, TracesInsight, WorkClassification } from '../overview';
import { Link } from 'react-router';
import { ErrorCard } from '@/components/layout/ErrorCard';
import { AllowancePanel } from '@/components/observability/AllowancePanel';
import { Delta } from '@/components/observability/KpiDelta';
import { TokensChart } from '@/components/observability/TokensChart';
import { SkelKpis, SkelPanel } from '@/components/analytics/ReportParts';
import { RangePicker } from '@/components/analytics/RangePicker';
import { UsageByModel } from '@/components/analytics/UsageByModel';

function Spark({ values, color = '' }: { values: number[]; color?: string }) {
  const maximum = Math.max(1, ...values);
  return <span className={`spark ${color}`} aria-hidden="true">{values.slice(-14).map((value, i) => <i key={i} style={{ height: `${Math.max(2, value / maximum * 100)}%` }} />)}</span>;
}

export function Overview() {
  const query = useConsoleStats();
  const { tooLong } = useEvidenceWindow();
  const head = <div className="head"><div><h1>Overview</h1><p>Your personal AI runtime — context shape, budget, health, and the work that needs attention.</p></div><div className="actions"><RangePicker /></div></div>;
  if (query.isPending) return <>{head}<SkelKpis n={4} className="kpis" card="kpi" /><div className="overview-grid"><div className="overview-main"><SkelPanel title="Allowance" chart className="" /><SkelPanel title="Tokens per day" chart className="" /></div><SkelPanel title="Runtime" className="" /></div></>;
  if (query.isError || !query.data) return <ErrorCard onRetry={() => void query.refetch()} retrying={query.isFetching} />;
  const { totals, budget, range, series, models } = query.data;
  const deltas: Partial<StatsDeltas> = totals.deltas ?? {};
  // Spend KPI = this cycle's allowance spend (the enforced tag, same figure as
  // the allowance panel), not the page range; the range spend lives on Cost.
  const cycle = budget.source !== 'unknown' && budget.current != null;
  const spend = <><div className="value">{formatCurrency(cycle ? budget.current : totals.spend)}{cycle && budget.max_budget != null && <span className="of"> / {formatCurrency(budget.max_budget)}</span>}</div><div className="delta">{!cycle ? 'Recorded spend' : budget.max_budget ? `${formatPercent(budget.current! / budget.max_budget, 0)} of this cycle’s allowance` : 'Spent this cycle'}</div><Delta pct={deltas.spend_pct} bad /></>;
  return <>
    {head}
    <div className="kpis">
      <div className="panel kpi"><div className="khead"><span className="badge">$</span>Spend</div>{spend}<Spark values={series.map((row) => row.spend)} /></div>
      <div className="panel kpi"><div className="khead"><span className="badge">⬡</span>Tokens</div><div className="value">{abbreviate(totals.tokens)}</div><div className="delta">{abbreviate(totals.input_tokens)} input · {abbreviate(totals.output_tokens)} output</div><Delta pct={deltas.tokens_pct} /><Spark color="blue" values={series.map((row) => row.tokens)} /></div>
      <div className="panel kpi"><div className="khead"><span className="badge">↗</span>Gateway operations</div><div className="value">{formatInt(totals.requests)}</div><div className="delta">{formatInt(totals.failed_requests)} failed operations</div><Delta pct={deltas.requests_pct} /><Spark values={series.map((row) => row.requests)} /></div>
      <EvidenceKpi />
    </div>
    <EvidenceHero range={range} />
    <div className="overview-grid"><div className="overview-main"><AllowancePanel budget={budget} />
        <section className="panel forecast"><div className="forecast-head"><div><h2>Tokens per day</h2><p>{range.start} – {range.end} · UTC · input and output</p></div><div className="forecast-meta"><div><span>Input</span><b>{abbreviate(totals.input_tokens)}</b></div><div><span>Output</span><b>{abbreviate(totals.output_tokens)}</b></div></div></div><TokensChart series={series} /><div className="legend"><span><i style={{ background: 'var(--green)' }} />Input tokens</span><span><i style={{ background: 'var(--blue)' }} />Output tokens</span></div></section></div>
      <section className="panel insights"><div className="panel-title"><h2>Runtime</h2><Link className="go right" to="/analytics/runtime" aria-label="Open Runtime"><ArrowRight size={16} aria-hidden="true" /></Link></div>
        <RuntimeInsights />
        <TracesInsight />
        <Link className="insight" to="/analytics/cost"><span className="symbol">◎</span><span><b>Remaining allowance</b><p>{budget.source === 'unknown' ? 'Budget data is unavailable.' : budget.current == null ? `Spend unavailable${budget.max_budget == null ? '; no limit configured' : ` against a ${formatCurrency(budget.max_budget)} cap`}.` : budget.max_budget == null ? `${formatCurrency(budget.current)} used; no limit configured.` : `${formatCurrency(budget.max_budget - budget.current)} of ${formatCurrency(budget.max_budget)} left this cycle.`}</p></span><span className="go" aria-hidden="true"><ArrowRight size={16} /></span></Link>
      </section>
    </div>
    <div className="lower-grid"><UsageByModel models={models} />
      <TracesFeed /></div>
    <WorkClassification tooLong={tooLong} />
  </>;
}
