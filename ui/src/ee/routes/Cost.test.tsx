// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { Cost, keyLabel } from './Cost';

const state = vi.hoisted(() => ({
  stats: { isPending: false, isError: false, data: {
    range: { start: '2026-10-01', end: '2026-10-03' },
    totals: { spend: 12, requests: 10, failed_requests: 0, deltas: { spend_pct: 0.5 as number | null } },
    budget: { current: 40, max_budget: 100, source: 'tag', projection: { basis: 'linear_cycle_rate', cycle_start: '2026-09-30T23:00:00Z', horizon_end: '2026-10-31T23:00:00Z', spend: 57.6 as number | null, pct: 0.576, reason: null as string | null } },
    series: [{ date: '2026-10-01', spend: 4 }, { date: '2026-10-02', spend: 0 }, { date: '2026-10-03', spend: 8 }],
    models: [{ model: 'ackstorm.smart', requests: 4, spend: 10, spend_pct: 10 / 12 }, { model: 'ackstorm.fast', requests: 6, spend: 2, spend_pct: 2 / 12 }],
    keys: [{ id: 'k1', key_alias: 'personal', requests: 10, spend: 12, spend_pct: 1 }],
  } },
  cost: undefined as unknown,
}));
vi.mock('@/stores/console-range', () => ({ useConsoleStats: () => state.stats, useConsoleDateRange: () => ({ start: '2026-10-01', end: '2026-10-03' }), useConsoleRange: (sel: (s: unknown) => unknown) => sel({ preset: 'This month', custom: null, setPreset: () => {}, setCustom: () => {} }) }));
vi.mock('@/hooks/use-evidence-reports', () => ({
  useEvidenceReport: () => ({ data: state.cost, isPending: false }),
  useEvidenceWindow: () => ({ range: { start: '2026-10-01', end: '2026-10-03' }, tooLong: false }),
  EVIDENCE_LIMIT_NOTICE: 'notice',
  sourceUnavailable: (p: { state: string; reason: string | null }) => p.state === 'not_instrumented' || Boolean(p.reason),
}));

const report = {
  provenance: { state: 'available', reason: null, sampled: false },
  priced_generations: 2021, zero_cost_generations: 3040, spend: 4.77, cost_per_model_request: 0.00236,
  sessions: 1, cost_per_session: 0.31, session_spend_coverage: 0.066,
  cache: { basis: 'x', with_cache: 3.389, without_cache: 13.308, saving: 9.919, saving_share: 0.745, priced_models: 4, unpriced_models: ['ackstorm.nano'], reason: null },
};


const hover = (svg: Element, clientX: number) => {
  svg.getBoundingClientRect = () => ({ left: 0, top: 0, width: Number(svg.getAttribute('viewBox')!.split(' ')[2]), height: 190, right: 0, bottom: 190, x: 0, y: 0, toJSON: () => ({}) });
  fireEvent.mouseMove(svg, { clientX, clientY: 60 });
};
afterEach(() => { cleanup(); state.cost = undefined; });

describe('Cost v3', () => {
  it('while pending renders the header and panel titles with skeletons, not the session card', () => {
    state.stats.isPending = true;
    render(<MemoryRouter><Cost /></MemoryRouter>);
    expect(screen.getByRole('heading', { name: 'Cost' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Daily spend' })).toBeInTheDocument();
    expect(screen.getAllByRole('status', { name: 'Loading' }).length).toBeGreaterThan(0);
    expect(screen.queryByText(/Connecting to ACH/)).toBeNull();
    state.stats.isPending = false;
  });

  it('draws daily spend below the metric cards, with the period picker in the header', () => {
    const { container } = render(<MemoryRouter><Cost /></MemoryRouter>);
    expect(screen.getByRole('heading', { name: 'Daily spend' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'Daily recorded spend, 3 days, $12.00 in total' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /Detailed usage/ })).toBeNull();
    expect(screen.queryByText(/requests per day/i)).toBeNull();
    const grid = container.querySelector('.metric-grid')!;
    expect(grid.compareDocumentPosition(container.querySelector('.hero-metric')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(container.querySelector('.head .actions .range-trigger')).toBeInTheDocument();
    expect(container.querySelector('.report-filters')).toBeNull();
  });

  it('hides $0 models: the detail table keeps All spend plus a page of 10, with the hidden count', () => {
    const saved = state.stats.data.models;
    state.stats.data.models = [...Array.from({ length: 12 }, (_, i) => ({ model: `m${i}`, requests: 2, spend: 1, spend_pct: 1 / 12 })), ...['z1', 'z2', 'z3'].map((model) => ({ model, requests: 1, spend: 0, spend_pct: 0 }))];
    render(<MemoryRouter><Cost /></MemoryRouter>);
    const table = screen.getByRole('heading', { name: 'Cost detail' }).closest<HTMLElement>('section')!;
    expect(within(table).getAllByRole('row')).toHaveLength(12); // header + All spend + 10
    expect(within(table).getByText('1–10 of 12')).toBeInTheDocument();
    expect(within(table).getByText('3 models with no recorded spend hidden.')).toBeInTheDocument();
    expect(within(table).queryByText('z1')).toBeNull();
    // The pager is the panel's last row, sharing it with the hidden note; no contract note.
    const foot = table.querySelector('.table')!.lastElementChild!;
    expect(foot).toHaveClass('report-foot');
    expect(foot.lastElementChild).toHaveClass('report-pager');
    expect(foot).toHaveTextContent('3 models with no recorded spend hidden.');
    expect(table.textContent).not.toMatch(/daily-activity contract/);
    expect(document.body.textContent).not.toMatch(/LiteLLM/);
    state.stats.data.models = saved;
  });

  it('shows each model\'s previous-period spend and change; an unread window is —', () => {
    const saved = state.stats.data.models;
    state.stats.data.models = [{ model: 'ackstorm.smart', requests: 4, spend: 10, spend_pct: 10 / 12, prev_spend: 6 }, { model: 'ackstorm.fast', requests: 6, spend: 2, spend_pct: 2 / 12, prev_spend: null }] as unknown as typeof saved;
    render(<MemoryRouter><Cost /></MemoryRouter>);
    const table = screen.getByRole('heading', { name: 'Cost detail' }).closest<HTMLElement>('section')!;
    const cells = (model: string) => [...within(table).getByText(model).closest('tr')!.querySelectorAll('td')].slice(-2).map((td) => td.textContent);
    expect(cells('ackstorm.smart')).toEqual(['$6.00', '▲ $4.00']);
    expect(cells('ackstorm.fast')).toEqual(['—', '—']);
    state.stats.data.models = saved;
  });

  it('compares with the previous period and never divides by a zero baseline', () => {
    render(<MemoryRouter><Cost /></MemoryRouter>);
    expect(screen.getByText('+50.0% vs previous period ($8.00)')).toBeInTheDocument();
    cleanup();
    state.stats.data.totals.deltas.spend_pct = null;
    render(<MemoryRouter><Cost /></MemoryRouter>);
    expect(screen.getByText('No comparable previous period')).toBeInTheDocument();
    state.stats.data.totals.deltas.spend_pct = 0.5;
  });

  it('labels the projection horizon and reports a missing one with its reason', () => {
    render(<MemoryRouter><Cost /></MemoryRouter>);
    expect(screen.getByText('$57.60')).toBeInTheDocument();
    expect(screen.getByText(/^Projected by /)).toBeInTheDocument();
    cleanup();
    Object.assign(state.stats.data.budget.projection, { spend: null, reason: 'too_early' });
    render(<MemoryRouter><Cost /></MemoryRouter>);
    expect(screen.getByText('Available after the first day of the cycle')).toBeInTheDocument();
    Object.assign(state.stats.data.budget.projection, { spend: 57.6, reason: null });
  });

  it('shows per-request and per-session cost with their denominators and coverage', () => {
    state.cost = report;
    render(<MemoryRouter><Cost /></MemoryRouter>);
    expect(screen.getAllByText('$0.0024').length).toBeGreaterThan(0);
    expect(screen.getByText(/3,040 generations with no recorded cost/)).toBeInTheDocument();
    expect(screen.getByText(/6.6% of spend carries a session id/)).toBeInTheDocument();
  });

  it('compares recorded vs hypothetical cache cost with the hypothetical as denominator', () => {
    state.cost = report;
    render(<MemoryRouter><Cost /></MemoryRouter>);
    expect(screen.getByText('$3.39')).toBeInTheDocument();
    expect(screen.getByText('$13.31')).toBeInTheDocument();
    expect(screen.getByText('74.5% of the hypothetical cost')).toBeInTheDocument();
    expect(screen.getByText(/Not compared \(no cache price\): ackstorm.nano/)).toBeInTheDocument();
  });

  it('missing cost evidence is unavailable, never zero', () => {
    render(<MemoryRouter><Cost /></MemoryRouter>);
    expect(screen.getByText('Generation cost evidence is unavailable.')).toBeInTheDocument();
    expect(screen.getByText(/The cache comparison is unavailable/)).toBeInTheDocument();
    for (const label of ['Cost per model request', 'Cost per session']) {
      expect(screen.getByText(label).nextElementSibling).toHaveTextContent('—');
    }
  });

  it('hovering a daily spend bar shows that day\'s spend', () => {
    const { container } = render(<MemoryRouter><Cost /></MemoryRouter>);
    hover(container.querySelector('.hero-metric svg.desktop-chart')!, 1100);
    expect(container.querySelector('.hero-metric .chart-tip')).toHaveTextContent('Oct 03Spend$8.00');
  });
});

describe('keyLabel', () => {
  it('adds the id suffix only to names that repeat', () => {
    const rows = [
      { id: 'pkid_aaaa1111', key_alias: 'personal', requests: 1, spend: 1, spend_pct: null },
      { id: 'pkid_bbbb2222', key_alias: 'personal', requests: 1, spend: 1, spend_pct: null },
      { id: 'ekid_cccc3333', key_alias: 'n8n', requests: 1, spend: 1, spend_pct: null },
    ];
    expect(rows.map((r) => keyLabel(r, rows))).toEqual(['personal · 1111', 'personal · 2222', 'n8n']);
  });
});
