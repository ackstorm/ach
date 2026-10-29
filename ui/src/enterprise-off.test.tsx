// Overview, navigation and routes with Enterprise disabled and enabled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { Overview } from './routes/Overview';
import { Landing } from './App';
import { ConsoleNavigation } from './components/layout/ConsoleNavigation';
import { EnterpriseOnly } from './components/EnterpriseOnly';
import { useSessionStore } from './stores/session';
import type { SessionMe } from './lib/api-types';

vi.mock('@ee/routes', async (importOriginal) => ({ ...await importOriginal<typeof import('@ee/routes')>(), EeOverview: () => <h1>Enterprise overview</h1> }));
const state = vi.hoisted(() => ({
  stats: { isPending: false, isError: false, data: {
    range: { start: '2026-10-01', end: '2026-10-27' },
    totals: { spend: 12, requests: 10, tokens: 30, input_tokens: 20, output_tokens: 10, failed_requests: 1, cache_read_tokens: 0, deltas: { spend_pct: 0.109, tokens_pct: 0.688, requests_pct: -0.018 } },
    budget: { current: 49.93, max_budget: 100, source: 'tag', reset_at: '2026-10-31T23:00:00Z', projection: {}, cycle_series: [] },
    models: [{ model: 'ackstorm.smart', requests: 4, input_tokens: 10, output_tokens: 2, total_tokens: 12, cache_read_tokens: 0, spend: 3, spend_pct: 0.75, last_used: null }, { model: 'ackstorm.fast', requests: 6, input_tokens: 5, output_tokens: 1, total_tokens: 6, cache_read_tokens: 0, spend: 1, spend_pct: 0.25, last_used: null }],
    keys: [{ id: 'k1', key_alias: 'personal', requests: 9, spend: 4, spend_pct: 1 }],
    series: [{ date: '2026-10-26', spend: 4, requests: 5, tokens: 15, input_tokens: 10, output_tokens: 5, failed: 0 }],
  } },
}));
vi.mock('@/stores/console-range', () => ({ useConsoleStats: () => state.stats, useConsoleDateRange: () => ({ start: '2026-10-01', end: '2026-10-27' }), useConsoleRange: (sel: (s: unknown) => unknown) => sel({ preset: 'This month', custom: null, setPreset: () => {}, setCustom: () => {} }) }));
// The previous period (Sep 04 – Sep 30) answers a median of 1060 ms.
vi.mock('@/hooks/use-gateway-latency', () => ({ useGatewayLatency: (r: { start: string }) => ({ isError: false, data: { available: true, sampled: true, row_count: 500, latency: { p50_ms: r.start === '2026-09-04' ? 1060 : 1378, p95_ms: 14125, p99_ms: null, avg_ms: null, ttft_p50_ms: null, ttft_p95_ms: null, tokens_per_sec_p50: 130 }, outcomes: [{ status: 'success', count: 498 }, { status: 'failure', count: 2 }] } }) }));
vi.mock('@/hooks/use-evidence-reports', () => ({
  useEvidenceReport: () => ({ data: undefined, isPending: false }),
  useEvidenceWindow: () => ({ range: { start: '2026-10-01', end: '2026-10-27' }, tooLong: false }),
  EVIDENCE_LIMIT_NOTICE: 'limit',
  sourceUnavailable: () => false,
}));

const me = { email: 'a@b.c', name: 'A', is_admin: false } as SessionMe;
const setEnterprise = (enterprise: boolean) => useSessionStore.setState({ me: { ...me, enterprise } });

const hover = (svg: Element, clientX: number) => {
  svg.getBoundingClientRect = () => ({ left: 0, top: 0, width: Number(svg.getAttribute('viewBox')!.split(' ')[2]), height: 190, right: 0, bottom: 190, x: 0, y: 0, toJSON: () => ({}) });
  fireEvent.mouseMove(svg, { clientX, clientY: 60 });
};

afterEach(() => { cleanup(); window.sessionStorage.clear(); });

const analyticsLinks = () => screen.queryAllByRole('link').filter((a) => a.getAttribute('href')?.includes('/analytics/'));

describe('Enterprise disabled', () => {
  beforeEach(() => setEnterprise(false));

  it('the landing page is the core Overview', () => {
    render(<MemoryRouter><Landing /></MemoryRouter>);
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.queryByText('Enterprise overview')).toBeNull();
  });

  it('the core Overview reads LiteLLM only and links to no analytics page', () => {
    render(<MemoryRouter><Overview /></MemoryRouter>);
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.queryByText(/Work classification/)).toBeNull();
    for (const ee of ['Verified signals', 'Uncached input / output', 'Average generation input', 'Top apps', 'Tool uses']) expect(screen.queryByText(ee)).toBeNull();
    expect(document.querySelectorAll('.kpi')).toHaveLength(4);
    expect(screen.queryByRole('heading', { name: 'Runtime' })).toBeNull();
    const usage = screen.getByRole('heading', { name: 'Usage by model' }).closest('section')!;
    expect([...usage.querySelectorAll('tbody td.entity')].map((td) => td.textContent)).toEqual(['ackstorm.smart', 'ackstorm.fast']);
    expect(usage.querySelectorAll('.wc-slice')).toHaveLength(2);
    for (const name of ['Request outcome', 'Responsiveness', 'Spend by key', 'Requests per day']) expect(screen.getByRole('heading', { name })).toBeInTheDocument();
    expect(screen.getByText('Median latency')).toBeInTheDocument();
    expect(screen.getAllByText('latest 500 calls').length).toBeGreaterThan(0);
    expect(screen.queryByText('Traces')).toBeNull();
    expect(analyticsLinks()).toEqual([]);
  });

  it('puts each KPI change on its own line, latency against the previous period\'s median', () => {
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const lines = [...document.querySelectorAll('.kpi')].map((k) => [...k.querySelectorAll('.delta')].map((d) => d.textContent));
    expect(lines.map((l) => l[1])).toEqual(['▲ 10.9% vs previous period', '▲ 68.8% vs previous period', '▼ 1.8% vs previous period', '▲ 30.0% vs previous period']);
    expect(document.querySelectorAll('.kpi .chg.up')).toHaveLength(2); // spend and latency rising are bad
  });

  it('navigation marks Enterprise entries and links to no analytics page', () => {
    render(<MemoryRouter><ConsoleNavigation /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /Analytics/ }));
    expect(screen.getByText('Observability')).toBeInTheDocument();
    for (const label of ['Cost', 'Workloads', 'Skills & context', 'Traces']) expect(screen.getByText(`${label} · Enterprise only`)).toBeInTheDocument();
    expect(analyticsLinks()).toEqual([]);
  });

  it('hovering the daily charts shows that day\'s figures', () => {
    render(<MemoryRouter><Overview /></MemoryRouter>);
    const panel = (name: string) => screen.getByRole('heading', { name }).closest('section')!;
    hover(panel('Requests per day').querySelector('svg.desktop-chart')!, 600);
    expect(panel('Requests per day').querySelector('.chart-tip')).toHaveTextContent('Oct 26Requests5Failed0');
    hover(panel('Tokens per day').querySelector('svg.desktop-chart')!, 100);
    expect(panel('Tokens per day').querySelector('.chart-tip')).toHaveTextContent('Oct 26Input10Output5');
  });

  it('an Enterprise route shows the notice, not the page', () => {
    render(<EnterpriseOnly><p>secret</p></EnterpriseOnly>);
    expect(screen.getByRole('heading', { name: 'Enterprise only' })).toBeInTheDocument();
    expect(screen.queryByText('secret')).toBeNull();
  });
});

describe('Enterprise enabled', () => {
  beforeEach(() => setEnterprise(true));

  it('the landing page is the Enterprise Overview', () => {
    render(<MemoryRouter><Landing /></MemoryRouter>);
    expect(screen.getByRole('heading', { name: 'Enterprise overview' })).toBeInTheDocument();
  });

  it('navigation links Cost and routes render their page', () => {
    render(<MemoryRouter><ConsoleNavigation /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /Analytics/ }));
    expect(screen.getByRole('link', { name: 'Cost' })).toBeInTheDocument();
    cleanup();
    render(<EnterpriseOnly><p>secret</p></EnterpriseOnly>);
    expect(screen.getByText('secret')).toBeInTheDocument();
  });
});
