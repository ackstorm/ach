// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router';
import { cleanup, render, screen, within } from '@testing-library/react';
import type { StatsResponse } from '@/lib/api-types';

vi.mock('@/stores/console-range', () => ({ useConsoleStats: vi.fn(), useConsoleDateRange: () => ({ start: '2026-09-01', end: '2026-09-29' }), useConsoleRange: (sel: (s: unknown) => unknown) => sel({ preset: 'This month', custom: null, setPreset: () => {}, setCustom: () => {} }) }));
const latency = vi.hoisted(() => ({ value: { isPending: false, data: { available: true, guardrails: [] as unknown[] | null } } as Record<string, unknown> }));
vi.mock('@/hooks/use-evidence-reports', () => ({ useEvidenceReport: () => latency.value }));
import { useConsoleStats } from '@/stores/console-range';
import { Runtime } from './Runtime';

const statsMock = vi.mocked(useConsoleStats);
const set = (over: object) => statsMock.mockReturnValue(over as unknown as ReturnType<typeof useConsoleStats>);
afterEach(() => { cleanup(); latency.value = { isPending: false, data: { available: true, guardrails: [] } }; });
const renderPage = () => render(<MemoryRouter><Runtime /></MemoryRouter>);

describe('Runtime', () => {
  it('while pending renders the header and panel titles with skeletons, not the session card', () => {
    set({ isPending: true, isError: false, data: undefined });
    renderPage();
    expect(screen.getByRole('heading', { name: 'Runtime' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Models by requests' })).toBeInTheDocument();
    expect(screen.getAllByRole('status', { name: 'Loading' }).length).toBeGreaterThan(0);
    expect(screen.queryByText(/Connecting to ACH/)).toBeNull();
  });

  it('splits accounting rows into models, MCP tools (protocol separate) and agents', () => {
    set({ isPending: false, isError: false, data: {
      range: { start: '2026-09-01', end: '2026-09-29' },
      totals: { requests: 100, spend: 10 },
      models: [
        { model: 'acme.fast', requests: 60, spend: 4 },
        { model: 'MCP: github/list_repos', requests: 17, spend: 0 },
        { model: 'MCP: list_tools', requests: 9, spend: 0 },
        { model: 'a2a/research', requests: 4, spend: 0 },
        { model: 'agent.research', requests: 3, spend: 0 },
      ],
    } as unknown as StatsResponse });
    renderPage();
    const models = screen.getByRole('heading', { name: 'Models by requests' }).closest('section')!;
    expect(within(models).getByText('acme.fast')).toHaveAttribute('title', 'acme.fast · $4.00 spend');
    expect(within(models).getByText('60 · 100.0%')).toBeInTheDocument(); // requests · share; cost lives in the title
    expect(within(models).queryByText('a2a/research')).not.toBeInTheDocument();
    const tools = screen.getByRole('heading', { name: 'MCP tools by operations' }).closest('section')!;
    expect(within(tools).getByText('github/list_repos')).toBeInTheDocument();
    expect(within(tools).getByText('17 · 100.0%')).toBeInTheDocument(); // no "ops" suffix
    expect(within(tools).queryByText('list_tools')).not.toBeInTheDocument();
    const agents = screen.getByRole('heading', { name: 'A2A agent aliases' }).closest('section')!;
    expect(within(agents).getByText('a2a/research')).toBeInTheDocument();
    expect(within(agents).getByText('agent.research')).toBeInTheDocument();
    expect(within(agents).getByText('4 · 57.1%')).toBeInTheDocument();
    expect(screen.getByText(/60% of 100 gateway operations used acme\.fast/)).toBeInTheDocument();
    for (const label of ['Models used', 'MCP tools observed', 'A2A agent aliases observed', 'A2A operations']) expect(screen.getByText(label)).toBeInTheDocument();
    expect(screen.getByText(/top alias a2a\/research/)).toBeInTheDocument();
    expect(screen.queryByText(/Handoff outcomes · unavailable/)).not.toBeInTheDocument();
    expect(document.querySelector('.analytics-layout, .summary-stack')).toBeNull();
  });

  it('keeps zero usage honest and never divides an empty period', () => {
    set({ isPending: false, isError: false, data: { range: { start: '2026-09-29', end: '2026-09-29' }, totals: { requests: 0, spend: 0 }, models: [] } });
    renderPage();
    expect(screen.getAllByText('No observations in this window.')).toHaveLength(3);
    expect(screen.queryByText(/NaN|Infinity/)).not.toBeInTheDocument();
  });

  it('shows the error card and retries when stats fail', () => {
    set({ isPending: false, isError: true, refetch: vi.fn(), isFetching: false });
    renderPage();
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument();
  });

  it('lists guardrails with checks and interventions, and says when they could not be read', () => {
    set({ isPending: false, isError: false, data: { models: [], totals: { requests: 0, spend: 0 }, range: { start: '2026-09-01', end: '2026-09-29' } } });
    latency.value = { isPending: false, data: { available: true, guardrails: [{ name: 'credential-filter', checked: 42656, intervened: 4 }] } };
    renderPage();
    const panel = screen.getByRole('heading', { name: 'Guardrails on your calls' }).closest('section')!;
    expect(within(panel).getByText('credential-filter')).toBeInTheDocument();
    expect(within(panel).getByText('42,656 · 4 intervened')).toBeInTheDocument();
    cleanup();
    latency.value = { isPending: false, data: { available: true, guardrails: null } };
    renderPage();
    expect(screen.getByText('Guardrail activity could not be read.')).toBeInTheDocument();
  });
});

