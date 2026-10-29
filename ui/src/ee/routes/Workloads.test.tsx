// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/react';
import { Workloads } from './Workloads';

const state = vi.hoisted(() => ({ report: {} as Record<string, unknown> }));
vi.mock('@/hooks/use-evidence-reports', async (orig) => ({
  ...(await orig<typeof import('@/hooks/use-evidence-reports')>()),
  useEvidenceReport: () => state.report,
  useEvidenceWindow: () => ({ tooLong: false }),
}));
vi.mock('@/components/analytics/RangePicker', () => ({ RangePicker: () => null }));
afterEach(cleanup);

const ok = { state: 'available', reason: null, sampled: false };
const report = { isPending: false, data: {
  window: { start: '2026-10-21T00:00:00Z', end: '2026-10-28T00:00:00Z', timezone: 'UTC' }, provenance: ok, generation_count: 100,
  runtime_parser_version: 1, runtime_attribution: 'same_trace',
  runtimes: [
    { name: 'claude_code', count: 70, input_tokens: 1260000, output_tokens: 10000, cost: 4, median_input: 128000, cache_read_share: 0.31, cost_per_generation: 0.058 },
    { name: 'opencode', count: 20, input_tokens: 0, output_tokens: 0, cost: 0, median_input: null, cache_read_share: null, cost_per_generation: null },
    { name: 'python_client', count: 10, input_tokens: 140000, output_tokens: 10000, cost: 0.1, median_input: 18000, cache_read_share: 0.71, cost_per_generation: 0.011 },
  ],
} };

it('compares clients by measured context, cache, input/output and unit cost', () => {
  state.report = report;
  render(<Workloads />);
  expect(screen.getByText('Best cache-read share').closest('.metric')).toHaveTextContent('71%');
  expect(screen.getByText('Largest median context').closest('.metric')).toHaveTextContent('128K');
  const panel = screen.getByRole('heading', { name: 'Your client comparison' }).closest('section')!;
  const cells = (label: string) => [...within(panel).getByText(label).closest('tr')!.querySelectorAll('td')].slice(3).map((td) => td.textContent);
  expect(cells('Claude Code')).toEqual(['128K', '31%', '126 : 1', '$0.06']);
  // A client that reports none of it reads —, never 0.
  expect(cells('OpenCode')).toEqual(['—', '—', '—', '—']);
});
