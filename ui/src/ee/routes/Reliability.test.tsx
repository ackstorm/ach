// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { Reliability } from './Reliability';

const state = vi.hoisted(() => ({ latency: {} as Record<string, unknown>, performance: {} as Record<string, unknown> }));
vi.mock('@/stores/console-range', () => ({
  useConsoleStats: () => ({ isPending: false, isError: false, data: { totals: { requests: 1000, failed_requests: 40 } } }),
  useConsoleRange: (sel: (s: unknown) => unknown) => sel({ preset: '30d', custom: null, setPreset: () => {}, setCustom: () => {} }),
  useConsoleDateRange: () => ({ start: '2026-09-01', end: '2026-09-30' }),
}));
vi.mock('@/hooks/use-evidence-reports', () => ({
  useEvidenceReport: (name: string) => name === 'latency' ? state.latency : state.performance,
}));

const causes = (list: unknown[], extra = {}) => ({ isPending: false, data: { failure_causes: { state: 'derived', reason: null, causes: list, ...extra } } });

beforeEach(() => {
  state.latency = { isPending: false, data: { available: true, finish: { coverage: 800, truncated: 9, content_filtered: 1 }, guardrails: [{ name: 'credential-filter', checked: 42656, intervened: 4 }] } };
  state.performance = causes([
    { owner: 'provider', class: 'Anthropic.RateLimitError', status: '429', value: 368 },
    { owner: 'you', class: 'ProxyException', status: '403', value: 3679 },
    { owner: 'tool', class: 'MCPToolResultError', status: '', value: 16 },
  ]);
});
afterEach(cleanup);

it('splits failures by who can act and names the top cause in plain words', () => {
  render(<Reliability />);
  const panel = screen.getByRole('heading', { name: 'What failed in your work' }).closest('section')!;
  const row = (owner: string) => [...within(panel).getByText(owner).closest('tr')!.querySelectorAll('td')].slice(1).map((td) => td.textContent);
  expect(row('You')).toEqual(['3,679', '90.5%', 'Model not in your access (3,679)']); // share of all failures
  expect(row('Model provider')[2]).toBe('Provider rate limit (368)');
  fireEvent.mouseOver(within(panel).getByText('Model provider'));
  expect(panel.querySelector('.wc-name')).toHaveTextContent('Model provider');
  expect(panel.querySelector('.wc-slice.on')).toHaveClass('wc-s-5');
  expect(screen.getByText(/Use a model from your Models page/)).toBeInTheDocument();
});

it('shows guardrail interventions, truncated and filtered responses', () => {
  render(<Reliability />);
  expect(screen.getByText(/Blocked or changed 4 of 42,656 guardrail checks/)).toBeInTheDocument();
  const truncated = screen.getByText('Truncated responses').closest('.metric') as HTMLElement;
  expect(within(truncated).getByText('9')).toBeInTheDocument();
  expect(within(truncated).getByText(/1 stopped by a content filter/)).toBeInTheDocument();
  expect(screen.queryByText('Preview')).not.toBeInTheDocument();
});

it('reads missing sources as unavailable, never as zero', () => {
  state.latency = { isPending: false, data: { available: true, finish: null, guardrails: null } };
  state.performance = causes([], { state: 'partial', reason: 'source_unavailable' });
  render(<Reliability />);
  const guard = screen.getByText('Guardrail interventions').closest('.metric') as HTMLElement;
  expect(within(guard).getByText('—')).toBeInTheDocument();
  expect(within(guard).getByText(/could not be read/)).toBeInTheDocument();
  expect(screen.getByText('Failure causes could not be read.')).toBeInTheDocument();
  expect(screen.getByText('You · unavailable')).toBeInTheDocument();
  expect(screen.queryByText(/One useful correction/)).not.toBeInTheDocument();
});
