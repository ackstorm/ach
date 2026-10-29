// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// Reliability.tsx — what failed in the caller's own work and who can act on it.
// Gateway success comes from stats; failure owners and the queue from Thanos
// (/platform/observability/performance); truncation and guardrails from the
// caller's traces (/platform/observability/latency). Each source degrades on
// its own: a missing one reads "unavailable", never zero.
import { useConsoleStats } from '@/stores/console-range';
import { useEvidenceReport, type FailureCauses, type FailureOwner, type GenerationLatency, type PerformanceReport } from '@/hooks/use-evidence-reports';
import { formatInt, formatPercent } from '@/lib/format';
import { DataTable } from '@/components/ui/data-table';
import { ErrorCard } from '@/components/layout/ErrorCard';
import { Insights, Metric, Panel, ReportHead, SkelKpis, SkelPanel, Swatch, TableDonut, Unavailable } from '@/components/analytics/ReportParts';

// Fixed colour per owner (.wc-s-* rank tokens in reports.css).
const OWNERS: { owner: FailureOwner; label: string; body: string; cls: string }[] = [
  { owner: 'you', cls: 'wc-s-4', label: 'You', body: 'Requests you can change: a model outside your access, an invalid request or budget.' },
  { owner: 'provider', cls: 'wc-s-5', label: 'Model provider', body: 'Rate limits, timeouts and provider outages. Retry later or switch model.' },
  { owner: 'tool', cls: 'wc-s-2', label: 'Tools', body: 'An MCP tool returned an error.' },
  { owner: 'unattributed', cls: 'wc-s-7', label: 'Unattributed', body: 'Failures without a recognised cause.' },
];

// One plain-words name per known cause; anything else shows its class.
function causeName(c: FailureCauses['causes'][number]): string {
  if (c.class === 'ProxyException' && c.status === '403') return 'Model not in your access';
  if (c.class === 'BudgetExceededError') return 'Budget exceeded';
  if (c.class === 'ProxyModelNotFoundError') return 'Unknown model';
  if (c.class === 'MCPToolResultError') return 'MCP tool error';
  if (c.class.endsWith('RateLimitError')) return 'Provider rate limit';
  if (c.class.endsWith('.Timeout')) return 'Provider timeout';
  if (c.status.startsWith('5')) return 'Provider unavailable';
  if (c.status === '400') return 'Invalid request';
  return c.status ? `${c.class} (${c.status})` : c.class;
}

export function Reliability() {
  const query = useConsoleStats();
  const latency = useEvidenceReport<GenerationLatency>('latency');
  const performance = useEvidenceReport<PerformanceReport>('performance');
  const head = <ReportHead title="Reliability">Separate problems you can correct from model, tool, and platform failures outside your control.</ReportHead>;
  if (query.isPending) return <>{head}<SkelKpis /><div className="perf-grid"><SkelPanel title="What failed in your work" className="" /><SkelPanel title="Who can act" className="" /></div></>;
  if (query.isError || !query.data) return <ErrorCard onRetry={() => void query.refetch()} retrying={query.isFetching} />;
  const { totals } = query.data;
  const traces = latency.data?.available ? latency.data : undefined;
  const causes = performance.data?.failure_causes;
  const attributed = causes?.state !== 'not_instrumented' && causes?.causes.length ? causes.causes : undefined;
  const byOwner = OWNERS.map((o) => {
    const own = attributed?.filter((c) => c.owner === o.owner).sort((a, b) => b.value - a.value) ?? [];
    return { ...o, value: own.reduce((sum, c) => sum + c.value, 0), top: own.at(0) };
  }).filter((o) => o.value > 0);
  type Owner = typeof byOwner[number];
  const failed = byOwner.reduce((sum, o) => sum + o.value, 0);
  const guards = traces?.guardrails;
  const checked = guards?.reduce((sum, g) => sum + g.checked, 0) ?? 0;
  const intervened = guards?.reduce((sum, g) => sum + g.intervened, 0) ?? 0;
  const finish = traces?.finish;
  const you = byOwner.find((o) => o.owner === 'you');
  return <>{head}
    <div className="metric-grid"><Metric label="Gateway operation success" value={totals.requests ? formatPercent((totals.requests - totals.failed_requests) / totals.requests) : '—'}>{formatInt(totals.requests - totals.failed_requests)} of {formatInt(totals.requests)} gateway operations completed without a recorded failure.</Metric>
      <Metric label="Guardrail interventions" value={guards ? formatInt(intervened) : undefined}>{guards ? checked ? `Blocked or changed ${formatInt(intervened)} of ${formatInt(checked)} guardrail checks on your calls.` : 'No guardrail ran on your calls in this period.' : 'Guardrail activity could not be read.'}</Metric>
      <Metric label="Truncated responses" value={finish ? formatInt(finish.truncated) : undefined}>{finish ? `Answers cut at the output limit, of ${formatInt(finish.coverage)} generations that report why they ended. ${formatInt(finish.content_filtered)} stopped by a content filter.` : 'No generation reported why it ended in this period.'}</Metric></div>
    {!totals.requests && <p className="report-source-note">No requests in this period.</p>}
    {attributed ? <Panel title="What failed in your work" subtitle={`${formatInt(failed)} failures by who can act · estimated from gateway counters`} className="report-gap"><TableDonut label="Failures by who can act" slices={byOwner.map((o) => ({ id: o.owner, name: o.label, value: o.value, cls: o.cls }))} format={formatInt} noun="failures">{(hover) => <DataTable columns={[
      { key: 'owner', header: 'Who can act', className: 'entity', cell: (o: Owner) => <><b><Swatch cls={o.cls} />{o.label}</b><small>{o.body}</small></> },
      { key: 'failures', header: 'Failures', className: 'mono', cell: (o: Owner) => formatInt(o.value) },
      { key: 'share', header: 'Share', className: 'mono', cell: (o: Owner) => formatPercent(o.value / failed) },
      { key: 'top', header: 'Top cause', cell: (o: Owner) => o.top ? `${causeName(o.top)} (${formatInt(o.top.value)})` : '—' },
    ]} rows={byOwner} getRowId={(o) => o.owner} highlight={hover} />}</TableDonut></Panel>
      : <div className="perf-grid"><Panel title="What failed in your work" subtitle="Failures by who can act"><Unavailable>{performance.isPending ? 'Loading failure causes…' : causes?.reason === 'no_series' ? 'No attributed failures in this period.' : 'Failure causes could not be read.'}</Unavailable></Panel>
      <Insights title="Who can act" rows={OWNERS.slice(0, 3).map((o) => ({ title: `${o.label} · unavailable`, body: o.body }))}/></div>}
    {you && failed > 0 && you.value / failed > 0.5 && you.top && <section className="panel callout report-gap"><b>One useful correction</b><p>Most of your failures are requests you can change, mainly “{causeName(you.top)}”. {you.top.class === 'ProxyException' ? 'Use a model from your Models page, or ask for access to the one you need.' : 'Check the request your client sends.'}</p></section>}
  </>;
}
