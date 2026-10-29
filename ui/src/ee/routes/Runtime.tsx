// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { useConsoleStats } from '@/stores/console-range';
import { ErrorCard } from '@/components/layout/ErrorCard';
import { formatCurrency, formatInt, formatPercent } from '@/lib/format';
import { isAgentModelRow, isMcpModelRow, isMcpProtocolRow, mcpToolLabel } from '@/lib/model-classify';
import { useEvidenceReport, type GenerationLatency } from '@/hooks/use-evidence-reports';
import { Bars, Metric, Panel, ReportHead, SkelPanel, Unavailable } from '@/components/analytics/ReportParts';
import '@/components/analytics/runtime.css';

const TOP = 10;
const byRequests = <T extends { model: string | null; requests: number }>(rows: T[]) =>
  [...rows].sort((a, b) => b.requests - a.requests || (a.model ?? '').localeCompare(b.model ?? ''));
const sum = (rows: { requests: number }[]) => rows.reduce((total, row) => total + row.requests, 0);

// Usage statistics for models, MCP tools and A2A agents. The catalog pages only
// list what is available; these figures come from the stats accounting rows.
// Guardrails come from the caller's traces (latency report); Reliability shows
// the same source as an intervention count.
export function Runtime() {
  const query = useConsoleStats();
  const latency = useEvidenceReport<GenerationLatency>('latency');
  const head = <ReportHead title="Runtime">Usage of the models, MCP tools, and A2A agents available to you.</ReportHead>;
  if (query.isPending) return <>{head}<SkelPanel title="Gateway operations" className="" /><div className="breakdown runtime-gap"><SkelPanel title="Models by requests" className="" /><SkelPanel title="MCP tools by operations" className="" /></div></>;
  if (query.isError || !query.data) return <ErrorCard onRetry={() => void query.refetch()} retrying={query.isFetching} />;
  const { models: rows, totals, range } = query.data;
  const agents = byRequests(rows.filter((row) => isAgentModelRow(row.model)));
  const tools = byRequests(rows.filter((row) => isMcpModelRow(row.model) && !isMcpProtocolRow(row.model)));
  const protocol = sum(rows.filter((row) => isMcpProtocolRow(row.model)));
  const models = byRequests(rows.filter((row) => row.model !== null && !isAgentModelRow(row.model) && !isMcpModelRow(row.model)));
  const top = models[0];
  const share = top && totals.requests > 0 ? formatPercent(top.requests / totals.requests, 0) : '—';
  const agentSpend = agents.reduce((total, row) => total + row.spend, 0);
  const guards = latency.data?.available ? latency.data.guardrails : undefined;
  const subtitle = (n: number, noun: string) => n > TOP ? `Top ${TOP} of ${n} ${noun} · gateway operations` : 'Gateway operations';
  return <>{head}
    <section className="panel hero-metric"><div className="hero-top"><div><h2>Gateway operations</h2><p>{range.start} – {range.end} · model calls and MCP tool operations</p></div><div className="hero-number"><b>{formatInt(totals.requests)}</b><small>{formatCurrency(totals.spend)} spend</small></div></div>
      <div className="scoregrid">{[[formatInt(sum(models)), 'model requests'], [formatInt(sum(tools)), 'MCP tool operations'], [formatInt(protocol), 'MCP protocol listings'], [formatInt(sum(agents)), 'agent operations']].map(([value, label]) => <div key={label}><b>{value}</b><span>{label}</span></div>)}</div></section>
    <div className="runtime-metrics"><Metric label="Models used" value={formatInt(models.length)}>{top && totals.requests > 0 ? `${share} of ${formatInt(totals.requests)} gateway operations used ${top.model} (routing concentration; the total includes tool calls).` : 'Aliases with recorded requests in this window.'}</Metric><Metric label="MCP tools observed" value={formatInt(tools.length)}>Tool names retained in your usage accounting, including tools with an unknown server.</Metric><Metric label="A2A agent aliases observed" value={formatInt(agents.length)}>Agent aliases as recorded; handoffs are not instrumented.</Metric><Metric label="A2A operations" value={formatInt(sum(agents))}>{formatCurrency(agentSpend)} spend on agent aliases{agents[0] ? `; top alias ${agents[0].model}.` : '.'}</Metric></div>
    <div className="breakdown runtime-gap"><Panel title="Models by requests" subtitle={subtitle(models.length, 'models')}><Bars total={sum(models)} rows={models.slice(0, TOP).map((row) => ({ name: row.model!, value: row.requests, title: `${row.model} · ${formatCurrency(row.spend)} spend` }))}/></Panel><Panel title="MCP tools by operations" subtitle={subtitle(tools.length, 'tools')}><Bars color="purple" total={sum(tools)} rows={tools.slice(0, TOP).map((row) => ({ name: mcpToolLabel(row.model), value: row.requests }))}/></Panel></div>
    <div className="breakdown runtime-gap"><Panel title="A2A agent aliases" subtitle={subtitle(agents.length, 'aliases')}><Bars color="cyan" total={sum(agents)} rows={agents.slice(0, TOP).map((row) => ({ name: row.model!, value: row.requests }))}/></Panel><Panel title="Guardrails on your calls" subtitle="Checks run by the AI gateway · interventions blocked or changed a call">{guards ? guards.length ? <Bars color="amber" share={false} rows={guards.map((g) => ({ name: g.name, value: g.checked, display: `${formatInt(g.checked)} · ${formatInt(g.intervened)} intervened` }))}/> : <Unavailable>No guardrail ran on your calls in this period.</Unavailable> : <Unavailable>{latency.isPending ? 'Loading guardrails…' : 'Guardrail activity could not be read.'}</Unavailable>}</Panel></div>
    <section className="panel callout runtime-gap"><b>What is not measured</b><p>Accounting retains model and tool identities. Server-level calls, per-tool errors, latency, result-token share, and agent handoff outcomes are unavailable from this report.</p></section>
  </>;
}
