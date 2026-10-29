// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { Overview } from './Overview';

const projection = { basis: 'linear_cycle_rate', cycle_start: '2026-09-30T23:00:00Z', horizon_end: '2026-10-31T23:00:00Z', spend: 57.6, pct: 0.576, reason: null };
const state = vi.hoisted(() => ({
  stats: { isPending: false, isError: false, data: {
    range: { start: '2026-10-01', end: '2026-10-27' },
    totals: { spend: 12, requests: 10, tokens: 5253658002, input_tokens: 20, output_tokens: 10, failed_requests: 1, cache_read_tokens: 0, deltas: { spend_pct: 0.109, tokens_pct: null, requests_pct: -0.018 } },
    budget: { current: 49.93 as number | null, max_budget: 100 as number | null, source: 'tag', reset_at: '2026-10-31T23:00:00Z', projection: {} as Record<string, unknown>, cycle_series: [{ date: '2026-10-01', spend: 5 }] },
    models: [] as Array<{ model: string; spend: number; spend_pct: number; requests: number }>,
    series: [{ date: '2026-10-26', spend: 4, requests: 5, tokens: 15, input_tokens: 10, output_tokens: 5, failed: 0 }],
  } },
  context: undefined as unknown,
  workloads: undefined as unknown,
  classification: undefined as unknown,
  traces: { isError: false, data: { traces: [{ trace_id: 'trace-a', project_id: 'default', started_at: '2026-10-26T10:00:00Z', span_count: 3, status: 'unknown' }] as Array<{ trace_id: string; project_id: string; started_at: string; span_count: number; status: string; input_tokens?: number | null; output_tokens?: number | null }>, total_count: 44726, provenance: { state: 'available', reason: null } } },
  sessions: { isError: false, data: { sessions: [{ session_id: '128db405-aaaa-bbbb', project_id: 'default', last_activity_at: '2026-10-26T10:00:00Z', duration_ms: 754_000, turns: 13, input_tokens: 12000 as number | null, output_tokens: 450 as number | null, status: 'unknown' }], provenance: { state: 'available', reason: null } } },
  sessionSort: undefined as unknown,
  tooLong: false,
}));
state.stats.data.budget.projection = projection;
vi.mock('@/stores/console-range', () => ({ useConsoleStats: () => state.stats, useConsoleDateRange: () => ({ start: '2026-10-01', end: '2026-10-27' }), useConsoleRange: (sel: (s: unknown) => unknown) => sel({ preset: 'This month', custom: null, setPreset: () => {}, setCustom: () => {} }) }));
vi.mock('@ee/hooks/use-traces', () => ({ useTraces: () => state.traces }));
vi.mock('@ee/hooks/use-sessions', () => ({ useSessions: (_cursor: number, sort: unknown) => (state.sessionSort = sort, state.sessions) }));
vi.mock('@/hooks/use-evidence-reports', () => ({
  useEvidenceReport: (name: string) => ({ data: name === 'context' ? state.context : name === 'classification' ? state.classification : state.workloads, isPending: false }),
  useEvidenceWindow: () => ({ range: { start: '2026-10-01', end: '2026-10-27' }, tooLong: state.tooLong }),
  EVIDENCE_LIMIT_NOTICE: 'This view covers up to 31 days for now. Choose a shorter range.',
  sourceUnavailable: (p: { state: string; reason: string | null }) => p.state === 'not_instrumented' || Boolean(p.reason),
}));

const ok = { state: 'available', reason: null, sampled: false };
const context = (ratio: number | null, share: number | null) => ({
  provenance: ok, tool_provenance: ok, generation_count: 100, input_coverage: 100,
  input_summary: { mean: 40000, median: 30000, max: 90000 },
  token_ratio: { input_tokens: 40000, output_tokens: 100, value: ratio == null ? null : ratio * 10, uncached_input_tokens: 4000, uncached_value: ratio, coverage: 100 },
  cache_read_share: { value: share, coverage: 90 },
  reasoning: { tokens: 20, share: 0.2, coverage: 50 },
  invocations: [{ name: 'Read', count: 7 }, { name: 'Skill', count: 2 }], invocation_count: 9,
  context_bands: [{ name: '<50K', count: 60 }, { name: '50–100K', count: 30 }, { name: '100–200K', count: 6 }, { name: '200–300K', count: 3 }, { name: '>=300K', count: 1 }],
  skill_invocation_count: 2, subagent_invocation_count: null,
});

afterEach(() => { cleanup(); window.localStorage.clear(); state.context = undefined; state.workloads = undefined; state.classification = undefined; state.tooLong = false; });

describe('Overview v3', () => {
  it('while pending renders the header, KPI frames and panel titles, not the session card', () => {
    state.stats.isPending = true;
    const { container } = render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(container.querySelectorAll('.kpi')).toHaveLength(4);
    expect(screen.getByRole('heading', { name: 'Tokens per day' })).toBeInTheDocument();
    expect(screen.queryByText(/Connecting to ACH/)).toBeNull();
    state.stats.isPending = false;
  });

  it('orders the KPI row Spend → Tokens → Gateway operations → Average generation input', () => {
    const { container } = render(<MemoryRouter><Overview /></MemoryRouter>);
    const heads = [...container.querySelectorAll('.kpi .khead')].map((el) => el.lastChild?.textContent);
    expect(heads).toEqual(['Spend', 'Tokens', 'Gateway operations', 'Average generation input']);
    expect(screen.getByText('5.25B')).toBeInTheDocument();
  });

  it('puts each LiteLLM KPI change on its own line; a missing change shows none', () => {
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const lines = [...document.querySelectorAll('.kpi')].map((k) => [...k.querySelectorAll('.delta')].map((d) => d.textContent));
    expect(lines.map((l) => l[1] ?? null)).toEqual(['▲ 10.9% vs previous period', null, '▼ 1.8% vs previous period', null]);
  });

  it('shows verified signals only from measured counts', () => {
    state.context = context(40, 0.95);
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const band = screen.getByRole('region', { name: 'Verified signals' });
    expect([...band.querySelectorAll('.status-item b')].map((b) => b.textContent)).toEqual(['Large requests observed', 'Prompt cache-read share is high', 'Skills are visible']);
    expect(within(band).getByText('4 generations carried 200K+ input tokens')).toBeInTheDocument();
    expect(within(band).getByRole('link', { name: /Review all/ })).toHaveAttribute('href', '/analytics/context');
    cleanup();
    state.context = undefined;
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.queryByRole('region', { name: 'Verified signals' })).toBeNull();
  });

  it('gives ratio and cache independent statuses: 40:1 red with 95% cache acceptable', () => {
    state.context = context(40, 0.95);
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByText('Above target · > 10:1')).toHaveClass('pill', 'danger');
    expect(screen.getByText('Within target · ≥ 80%')).toHaveClass('pill', 'ok');
    expect(screen.getByText('20.0%')).toBeInTheDocument(); // reasoning share
  });

  it('shows the cache share as large as the ratio; only an off-target figure is red', () => {
    state.context = context(40, 0.95);
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const ratio = screen.getByText('40.0 : 1');
    const cache = screen.getByText('95.0%');
    expect(cache.tagName).toBe(ratio.tagName);
    expect(cache.parentElement).toHaveClass('ratio-main');
    expect(screen.getByText('Uncached input / output')).toHaveClass('label');
    expect(screen.getByText(/With cache reads: 400\.0 : 1/)).toBeInTheDocument();
    expect(screen.getByText('Prompt cache-read share')).toHaveClass('label');
    expect(ratio).toHaveClass('red');
    expect(cache).not.toHaveClass('red');
    cleanup();
    state.context = context(5, 0.5);
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByText('5.0 : 1')).not.toHaveClass('red');
    expect(screen.getByText('50.0%')).toHaveClass('red');
  });

  it('the Spend card is this cycle\'s allowance spend against the cap', () => {
    const { container } = render(<MemoryRouter><Overview /></MemoryRouter>);
    const card = container.querySelector('.kpi')!;
    expect(card).toHaveTextContent('$49.93 / $100.00');
    expect(card).toHaveTextContent('50% of this cycle’s allowance');
    cleanup();
    state.stats.data.budget.max_budget = null;
    const again = render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(again.container.querySelector('.kpi')).toHaveTextContent('Spent this cycle');
    expect(again.container.querySelector('.kpi .of')).toBeNull();
    state.stats.data.budget.max_budget = 100;
  });

  it('says nothing about LiteLLM and has no Personal label', () => {
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(document.body.textContent).not.toMatch(/LiteLLM|Personal/);
  });

  it('a range over 31 days shows the evidence notice instead of querying', () => {
    state.tooLong = true;
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getAllByText(/covers up to 31 days/).length).toBeGreaterThan(0);
    expect(screen.queryByRole('region', { name: 'Input and output activity' })).toBeNull();
  });

  it('the period picker sits in the page header', () => {
    const { container } = render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(container.querySelector('.head .actions .range-trigger')).toHaveTextContent('This month');
  });

  it('a missing measurement gets no status, never a pass or fail', () => {
    state.context = context(null, null);
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getAllByText('Unavailable')).toHaveLength(2);
  });

  it('shows remaining allowance and the backend projection to reset', () => {
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByText('$50.07 of $100.00 left this cycle.')).toBeInTheDocument();
    expect(screen.getByText('$57.60')).toBeInTheDocument();
    expect(screen.getByText(/^Projected by /)).toBeInTheDocument();
  });

  it('a backend without budget.projection renders, projection unavailable', () => {
    const saved = state.stats.data.budget.projection;
    delete (state.stats.data.budget as { projection?: unknown }).projection;
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByText(/Projection unavailable/)).toBeInTheDocument();
    state.stats.data.budget.projection = saved;
  });

  it('a missing cap is no limit, never a zero allowance', () => {
    state.stats.data.budget.max_budget = null;
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByText('No limit')).toBeInTheDocument();
    expect(screen.getByText(/no limit configured/)).toBeInTheDocument();
    state.stats.data.budget.max_budget = 100;
  });

  it('an unknown cycle spend keeps the cap and is never shown as $0', () => {
    state.stats.data.budget.current = null;
    state.stats.data.budget.projection = { ...projection, spend: null, pct: null, reason: 'spend_unknown' };
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByText('Spend unavailable against a $100.00 cap.')).toBeInTheDocument();
    expect(screen.getAllByText('Unavailable').filter((e) => !e.classList.contains('pill'))).toHaveLength(2); // spent + remaining (target pills aside)
    expect(screen.getByText(/Spend unavailable$/)).toBeInTheDocument(); // projection reason
    expect(screen.queryByText('$0.00')).not.toBeInTheDocument();
    state.stats.data.budget.current = 49.93;
    state.stats.data.budget.projection = projection;
  });

  it('Traces panel: recent sessions by last activity, the short session ID links to the session', () => {
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const panel = screen.getByRole('heading', { name: 'Traces' }).closest<HTMLElement>('section')!;
    expect(within(panel).getByText('Recent sessions')).toBeInTheDocument();
    expect(state.sessionSort).toEqual({ key: 'last_activity_at', dir: 'desc' });
    expect(within(panel).getByRole('link', { name: '128db405' })).toHaveAttribute('href', '/analytics/traces?session=128db405-aaaa-bbbb&project=default');
  });

  it('recent sessions: last activity → ID → turns → tokens → duration → coloured outcome, up to 10 rows', () => {
    const saved = state.sessions.data.sessions;
    state.sessions.data.sessions = Array.from({ length: 12 }, (_, i) => ({ ...saved[0], session_id: `s${i}-session`, last_activity_at: '2026-10-28T07:13:00Z', status: i === 0 ? 'error' : 'unknown', input_tokens: i === 1 ? null : 12000 }));
    const { container } = render(<MemoryRouter><Overview /></MemoryRouter>);
    const rows = container.querySelectorAll('.feedrow');
    expect(rows).toHaveLength(10);
    const cells = [...rows[0].children];
    expect(cells[0].tagName).toBe('TIME');
    expect(cells[0]).toHaveAttribute('dateTime', '2026-10-28T07:13:00Z');
    expect(cells[0].textContent).toMatch(/^Oct 28, \d{2}:13$/);
    expect(cells[1]).toHaveTextContent('s0-sessi');
    expect(cells[2]).toHaveTextContent('13 turns');
    expect(cells[3]).toHaveTextContent('12.45K');
    expect(cells[3]).toHaveAttribute('title', '12,000 in · 450 out');
    expect(cells[4]).toHaveTextContent('12 min 34 s');
    expect(cells[5]).toHaveTextContent('Failed');
    expect(cells[5]).toHaveClass('pill', 'danger');
    expect(rows[1].children[3]).toHaveTextContent('—');
    expect(rows[1].children[5]).toHaveClass('pill', 'neutral');
    state.sessions.data.sessions = saved;
  });

  it('Usage by model: same widget as Cost, 10 rows of value + share, $0 rows hidden', () => {
    state.stats.data.models = [...Array.from({ length: 12 }, (_, i) => ({ model: `m${i}`, spend: 20 - i, spend_pct: 0.05, requests: 1234 })), { model: 'free', spend: 0, spend_pct: 0, requests: 3 }]; // spend sum 174
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const panel = screen.getByRole('heading', { name: 'Usage by model' }).closest<HTMLElement>('section')!;
    expect(panel.querySelectorAll('.hrow')).toHaveLength(10);
    expect(within(panel).getByText('$20.00 · 11.5%')).toBeInTheDocument(); // share of the whole list, computed in Bars
    expect(within(panel).queryByText('free')).toBeNull();
    expect(within(panel).queryByRole('link', { name: /All costs/ })).toBeNull();
    const fills = new Set([...panel.querySelectorAll<HTMLElement>('.hrow .track i')].map((bar) => bar.style.background));
    expect([...fills]).toEqual(['var(--green)']); // one colour per list, the skin's primary
    state.stats.data.models = [];
  });

  it('lists top apps and tool uses from evidence, Unknown kept', () => {
    state.context = context(5, 0.9);
    state.workloads = { provenance: ok, runtimes: [{ name: 'claude_code', count: 30 }, { name: 'unknown', count: 2 }] };
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const section = screen.getByRole('heading', { name: 'Runtime' }).closest<HTMLElement>('section')!;
    const activity = within(section);
    expect(screen.queryByText('Activity')).toBeNull();
    expect(activity.getByRole('link', { name: 'Open Runtime' })).toHaveAttribute('href', '/analytics/runtime');
    expect(activity.getByRole('link', { name: /Traces/ })).toHaveAttribute('href', '/analytics/traces');
    expect(activity.getByRole('link', { name: /Remaining allowance/ })).toHaveAttribute('href', '/analytics/cost');
    expect(section.querySelectorAll('.go svg')).toHaveLength(3); // one arrow style for Runtime, Traces and allowance
    expect(activity.getByText('Claude Code')).toBeInTheDocument();
    expect(activity.getByText('Unknown')).toBeInTheDocument();
    expect(screen.getByText('Read')).toBeInTheDocument();
    expect(screen.getByText(/Tool calls emitted by the model: 9 across 2 distinct tools/)).toBeInTheDocument();
  });

  it('Top apps: Generations by default; Tokens and Cost reorder and relabel, tool uses unchanged', () => {
    state.context = context(5, 0.9);
    state.workloads = { provenance: ok, runtimes: [{ name: 'claude_code', count: 30, input_tokens: 1000, output_tokens: 500, cost: 0 }, { name: 'opencode', count: 10, input_tokens: 5_000_000, output_tokens: 1000, cost: 49.3 }] };
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const apps = () => [...document.querySelectorAll('.insight')[0].querySelectorAll('.hrow')].map((row) => row.textContent);
    expect(apps()).toEqual(['Claude Code30 · 75.0%', 'OpenCode10 · 25.0%']);
    expect(screen.getByText(/^Generations by client/)).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('group', { name: 'Top apps metric' })).getByRole('button', { name: 'Tokens' }));
    expect(apps()).toEqual(['OpenCode5M · 100.0%', 'Claude Code1.5K · 0.0%']);
    expect(screen.getByText(/^Tokens \(input \+ output\) by client/)).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('group', { name: 'Top apps metric' })).getByRole('button', { name: 'Cost' }));
    expect(apps()).toEqual(['OpenCode$49.30 · 100.0%', 'Claude Code$0.00 · 0.0%']);
    expect(window.localStorage.getItem('console.select.top-apps')).toBe('Cost');
    expect(screen.getByText(/subscription traffic records \$0/)).toBeInTheDocument();
    expect(screen.getByText(/Tool calls emitted by the model: 9/)).toBeInTheDocument();
  });

  it('Work classification: selector in the title, table with Share (default sort) then the donut by domain, trivial muted and last, persisted metric', () => {
    const cat = (category: string, sessions: number, cost: number) => ({ category, domain: category.split('.')[0], sessions, generations: sessions * 2, input_tokens: 1000, output_tokens: 500, cost });
    state.classification = { provenance: ok, taxonomy: 'v3', categories: [cat('trivial', 20, 0), cat('technology.genai_development', 12, 1), cat('other', 10, 5)], coverage: { sessions: 379, labelled: 50, share: 0.13, sessionless_generations: 10658 } };
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const panel = screen.getByRole('heading', { name: 'Work classification' }).closest<HTMLElement>('section')!;
    const rows = () => [...panel.querySelectorAll('tbody tr')].map((row) => [...row.children].map((td) => td.textContent));
    expect(panel.querySelector('.panel-title [role=group]')).toHaveAccessibleName('Work classification metric');
    expect(within(panel).getByText('Per session · share of sessions')).toBeInTheDocument();
    expect([...panel.querySelectorAll('th')].map((th) => th.textContent)).toEqual(['Category', 'Share', 'Sessions', 'Generations', 'Tokens', 'Cost', '$/session']);
    expect(panel.querySelector('th[aria-sort=descending]')).toHaveTextContent('Share');
    expect(rows()).toEqual([
      ['GenAI development', '28.6%', '12', '24', '1.5K', '$1.00', '$0.08'],
      ['Other', '23.8%', '10', '20', '1.5K', '$5.00', '$0.50'],
      ['Trivial', '47.6%', '20', '40', '1.5K', '$0.00', '$0.00'],
    ]);
    expect(panel.querySelectorAll('tbody tr')[2]).toHaveClass('muted');
    expect(panel.querySelector('tbody .wc-swatch.wc-c-technology-genai_development')).not.toBeNull();
    expect(panel.querySelectorAll('.wc-slice')).toHaveLength(3);
    const slice = panel.querySelector('.wc-slice.wc-c-technology-genai_development')!;
    expect(slice.querySelector('title')).toHaveTextContent('GenAI development: 12 · 28.6%');
    fireEvent.mouseEnter(slice);
    expect(panel.querySelector('.wc-name')).toHaveTextContent('GenAI development');
    expect(panel.querySelector('.wc-total')).toHaveTextContent('12');
    expect(panel.querySelector('.wc-noun')).toHaveTextContent('28.6% of sessions');
    expect(panel.querySelectorAll('tbody tr')[0]).toHaveClass('on');
    fireEvent.mouseOver(within(panel).getByText('Other'));
    expect(panel.querySelector('.wc-slice.on')).toHaveClass('wc-c-other');
    expect(panel.querySelector('.wc-name')).toHaveTextContent('Other');
    fireEvent.mouseLeave(within(panel).getByRole('img'));
    expect(within(panel).getByRole('img')).toHaveAttribute('aria-label', expect.stringMatching(/^Work classification by sessions: 42 in total\. GenAI development 12 \(28\.6%\)/));
    expect(panel.querySelector('.wc-total')).toHaveTextContent('42');
    expect(within(panel).getByText('50 of 379 sessions classified · taxonomy v3 · 10,658 generations without a session are not classified')).toHaveClass('report-source-note');
    expect(panel.querySelector('.report-coverage')).toBeNull();
    fireEvent.click(within(within(panel).getByRole('group', { name: 'Work classification metric' })).getByRole('button', { name: 'Cost' }));
    expect(rows().map((r) => r[0])).toEqual(['Other', 'GenAI development', 'Trivial']); // re-sorted by cost, trivial last
    expect(panel.querySelectorAll('.wc-slice')).toHaveLength(2); // a zero slice draws nothing
    expect(panel.querySelector('th[aria-sort=descending]')).toHaveTextContent('Share'); // share of the selected metric
    expect(rows().map((r) => r[1])).toEqual(['83.3%', '16.7%', '0.0%']);
    fireEvent.click(panel.querySelector<HTMLElement>('th[data-col=generations] button')!); // headers sort too; Trivial stays last
    expect(rows().map((r) => r[0])).toEqual(['GenAI development', 'Other', 'Trivial']);
    expect(window.localStorage.getItem('console.select.work-classification')).toBe('Cost');
    cleanup();
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(within(screen.getByRole('group', { name: 'Work classification metric' })).getByRole('button', { name: 'Cost' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('Work classification: zero coverage reads Not classified yet; a failed source is unavailable', () => {
    state.classification = { provenance: ok, taxonomy: 'v3', categories: [], coverage: { sessions: 12, labelled: 0, share: 0, sessionless_generations: 0 } };
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByText('Not classified yet.')).toBeInTheDocument();
    expect(screen.getByText('0 of 12 sessions classified · taxonomy v3')).toBeInTheDocument();
    cleanup();
    state.classification = { provenance: { state: 'unavailable', reason: 'query_failed', sampled: false }, taxonomy: 'v3', categories: [], coverage: null };
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByText('Work classification is unavailable right now.')).toBeInTheDocument();
    expect(screen.queryByText(/Soon/)).toBeNull();
  });
});
