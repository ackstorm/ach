// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// ContextSkills.tsx — how context, the prompt cache, tools, skills and
// delegated agents shape the caller's own generations
// (/platform/observability/context). Each figure has its own matched
// population; one the source does not carry reads unavailable, never 0.
import { EVIDENCE_LIMIT_NOTICE, sourceUnavailable, useEvidenceReport, useEvidenceWindow, type ContextReport } from '@/hooks/use-evidence-reports';
import { abbreviate, formatDecimal, formatInt, formatPercent } from '@/lib/format';
import { ErrorCard } from '@/components/layout/ErrorCard';
import { Bars, Metric, Panel, ReportHead, SkelKpis, SkelPanel, Unavailable } from '@/components/analytics/ReportParts';

const BANDS: Record<string, string> = { '<50K': 'Under 50K', '50–100K': '50K–100K', '100–200K': '100K–200K', '200–300K': '200K–300K', '>=300K': '300K+' };

export function ContextSkills() {
  const query = useEvidenceReport<ContextReport>('context');
  const { tooLong } = useEvidenceWindow();
  const head = <ReportHead title="Skills & context">See how context, cache, tools, skills, and delegated agents shape your model usage.</ReportHead>;
  if (tooLong) return <>{head}<p className="report-source-note">{EVIDENCE_LIMIT_NOTICE}</p></>;
  if (query.isPending) return <>{head}<SkelKpis /><SkelPanel title="Input and cache shape" /></>;
  if (query.isError || !query.data) return <ErrorCard onRetry={() => void query.refetch()} retrying={query.isFetching} />;
  const c = query.data;
  if (sourceUnavailable(c.provenance)) return <>{head}<Unavailable>Your generation evidence could not be read for this period.</Unavailable></>;
  const ratio = c.token_ratio.uncached_value ?? null;
  const large = c.context_bands.slice(3).reduce((sum, band) => sum + band.count, 0);
  const tools = sourceUnavailable(c.tool_provenance) ? undefined : c;
  const named = (rows: ContextReport['skills'], color: string, none: string) => rows == null ? <Unavailable>Names could not be read for this period.</Unavailable> : rows.length ? <Bars color={color} share={false} rows={rows.map((row) => ({ name: row.name, value: row.count }))} /> : <Unavailable>{none}</Unavailable>;
  return <>{head}
    <div className="metric-grid">
      <Metric label="Average generation input" value={abbreviate(c.input_summary.mean)}>Median {abbreviate(c.input_summary.median)} · maximum {abbreviate(c.input_summary.max)} over {formatInt(c.input_coverage)} generations.</Metric>
      <Metric label="Skill loads" value={tools ? formatInt(c.skill_invocation_count) : undefined}>{tools ? `Skill tool calls by your clients · ${formatInt(large)} generations carried 200K+ input.` : 'Tool calls could not be read.'}</Metric>
      <Metric label="Subagent starts" value={tools ? formatInt(c.subagent_invocation_count) : undefined}>{tools ? 'Agent and Task tool calls: work handed to a subagent.' : 'Tool calls could not be read.'}</Metric>
    </div>
    <section className="panel hero-metric report-gap"><div className="hero-top"><div><h2>Input and cache shape</h2><p>Your generations in this period · provider prompt-cache semantics</p></div><div className="hero-number"><b>{ratio == null ? '—' : `${formatDecimal(ratio, 0)} : 1`}</b><small>{abbreviate(c.token_ratio.uncached_input_tokens)} uncached input / {abbreviate(c.token_ratio.output_tokens)} output · with cache reads {c.token_ratio.value == null ? '—' : `${formatDecimal(c.token_ratio.value, 0)} : 1`}</small></div></div>
      <div className="scoregrid">
        <div><b>{abbreviate(c.cache_read_tokens)}</b><span>Cache-read tokens</span><p>{formatInt(c.cache_coverage)} generations report it</p></div>
        <div><b>{abbreviate(c.cache_creation_tokens)}</b><span>Cache-created tokens</span><p>{formatInt(c.cache_creation_coverage)} generations report it</p></div>
        <div><b>{formatPercent(c.cache_read_share.value)}</b><span>Prompt cache-read share</span><p>Cache-read over input, {formatInt(c.cache_read_share.coverage)} generations</p></div>
        <div><b>{formatPercent(c.reasoning.share)}</b><span>Reasoning share of output</span><p>{formatInt(c.reasoning.coverage)} generations report it</p></div>
      </div>
    </section>
    <div className="breakdown report-gap">
      <Panel title="Your generation-input distribution" subtitle={`${formatInt(c.input_coverage)} generations by input tokens`}><Bars total={c.input_coverage} rows={c.context_bands.map((band) => ({ name: BANDS[band.name] ?? band.name, value: band.count }))} /></Panel>
      <Panel title="Most-used skills" subtitle="Named from the Skill tool's arguments">{tools ? named(c.skills, 'purple', 'No skills loaded in this period.') : <Unavailable>Tool calls could not be read.</Unavailable>}</Panel>
    </div>
    <div className="breakdown report-gap">
      <Panel title="Delegated subagents" subtitle="Subagent type from the Agent and Task tools">{tools ? named(c.subagents, 'cyan', 'No subagents started in this period.') : <Unavailable>Tool calls could not be read.</Unavailable>}</Panel>
      <Panel title="Tools the model asked for" subtitle={tools ? `${formatInt(c.invocation_count)} calls${c.invocations_truncated ? ' · top 100 tools' : ''}` : 'Tool calls'}>{tools ? <Bars color="blue" total={c.invocation_count ?? undefined} rows={c.invocations.slice(0, 10).map((row) => ({ name: row.name, value: row.count }))} /> : <Unavailable>Tool calls could not be read.</Unavailable>}</Panel>
    </div>
    <p className="report-source-note">A tool call is what the model asked for, not a successful result. Cache-read and response-cache hits are different caches and are never combined.</p>
  </>;
}
