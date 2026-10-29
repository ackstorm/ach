// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { Performance } from './Performance';
import type { GenerationLatency } from '@/hooks/use-evidence-reports';

const state = vi.hoisted(() => ({ data: {} as GenerationLatency, trends: { isError: true } as unknown }));
const range = vi.hoisted(() => ({ tooLong: false, pending: false }));
vi.mock('@/hooks/use-evidence-reports', () => ({ useEvidenceReport: (name: string) => name === 'latency' ? { data: range.pending ? undefined : state.data, isPending: range.pending, isError: false } : state.trends, useEvidenceWindow: () => ({ range: { start: '2026-09-01', end: '2026-09-30' }, tooLong: range.tooLong }), EVIDENCE_LIMIT_NOTICE: 'This view covers up to 31 days for now. Choose a shorter range.' }));
vi.mock('@/stores/console-range', () => ({ useConsoleDateRange: () => ({ start: '2026-09-01', end: '2026-09-30' }), useConsoleRange: (sel: (s: unknown) => unknown) => sel({ preset: '30d', custom: null, setPreset: () => {}, setCustom: () => {} }) }));

beforeEach(() => {
  state.data = {
    eligibility: 'observed_generations_not_logical_requests', by_model_truncated: false, provenance: { state: 'available', reason: null, sampled: false },
    available: true, row_count: 10, sampled: false, window: { start: '2026-09-01', end: '2026-09-30', days: 30 },
    latency: { p50_ms: 100, p95_ms: 200, p99_ms: 300, avg_ms: 120, ttft_p50_ms: null, ttft_p95_ms: null, tokens_per_sec_p50: null },
    outcomes: [], by_model: [],
    histogram: [{ label: '<100ms', lower_ms: 0, upper_ms: 100, count: 4 }],
  };
});
afterEach(() => { cleanup(); range.tooLong = false; range.pending = false; });

it('puts the period picker in the header and shows the 31-day notice for wider ranges', () => {
  range.tooLong = true;
  const { container } = render(<Performance />);
  expect(container.querySelector('.head .actions .range-trigger')).toHaveTextContent('Last 30 days');
  expect(screen.getByText(/covers up to 31 days/)).toBeInTheDocument();
});

it('keeps aggregate timing visible when the independent model breakdown fails', () => {
  Object.assign(state.data, { by_model_reason: 'source_unavailable' });
  render(<Performance />);
  expect(screen.getByText('100 ms')).toBeInTheDocument();
  const panel = screen.getByRole('heading', { name: 'Performance by model' }).closest('section')!;
  expect(within(panel).getByText(/model timing breakdown.*unavailable/i)).toBeInTheDocument();
  expect(within(panel).queryByText(/no retained requests/i)).not.toBeInTheDocument();
});

it('discloses capped model coverage without calling the complete aggregate sampled', () => {
  Object.assign(state.data, { by_model_truncated: true });
  render(<Performance />);
  expect(screen.getByText(/top 100 models/i)).toBeInTheDocument();
  expect(screen.getByText(/10 observed generations.*full-window aggregate/i)).toBeInTheDocument();
});

it('does not render missing timing as an observed zero histogram', () => {
  Object.assign(state.data, { source_coverage: { generation_count: 10, timing: 0, ttft: 0 } });
  state.data.histogram![0].count = 0;
  state.data.latency = null;
  render(<Performance />);
  expect(screen.queryByRole('img', { name: 'Observed generation duration distribution' })).not.toBeInTheDocument();
  expect(screen.getByText(/no generation duration measurements/i)).toBeInTheDocument();
});

it('exposes timing coverage in the distribution footer', () => {
  Object.assign(state.data, { source_coverage: { generation_count: 10, timing: 4, ttft: 0 } });
  render(<Performance />);
  expect(screen.getByText(/4 of 10 generations measured.*excludes upstream queueing/i)).toBeInTheDocument();
  // TTFT has its own cards; the distribution footer only reports timing.
  expect(screen.getByText(/generations measured/).textContent).not.toMatch(/ttft|first-token/i);
  expect(screen.queryByText('Unavailable')).not.toBeInTheDocument();
  expect(screen.getByRole('img', { name: 'Observed generation duration distribution' })).toBeInTheDocument();
  cleanup();
  state.data.by_model = [{ model: 'm', requests: 1, failed: 0, p50_ms: 1, p95_ms: 1, tokens_per_sec_p50: null }];
  render(<Performance />);
  expect(within(screen.getByRole('table')).getByRole('columnheader', { name: /Generations/ })).toBeInTheDocument();
});

it('paginates the model table', () => {
  state.data.by_model = Array.from({ length: 20 }, (_, i) => ({ model: `m${i}`, requests: 1, failed: 0, p50_ms: 1, p95_ms: 1 })) as GenerationLatency['by_model'];
  render(<Performance />);
  const table = () => within(screen.getByRole('table'));
  expect(table().getByText('m0')).toBeInTheDocument();
  expect(table().queryByText('m10')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  expect(table().getByText('m10')).toBeInTheDocument();
  expect(table().queryByText('m0')).not.toBeInTheDocument();
});

it('shows output tok/s in the card and table, em dash when missing', () => {
  state.data.latency!.tokens_per_sec_p50 = 42.5;
  state.data.by_model = [{ model: 'a', requests: 1, failed: 0, p50_ms: 1, p95_ms: 1, tokens_per_sec_p50: 12.34 }, { model: 'b', requests: 1, failed: 0, p50_ms: 1, p95_ms: 1 }] as GenerationLatency['by_model'];
  render(<Performance />);
  expect(screen.getByText('42.5')).toBeInTheDocument();
  expect(screen.getByText('12.3')).toBeInTheDocument();
  expect(screen.getByText(/not streaming speed/i)).toBeInTheDocument();
});

it('shows time to first token with its streamed coverage and a TTFT column', () => {
  Object.assign(state.data, {
    latency: { ...state.data.latency!, ttft_p50_ms: 1245, ttft_p95_ms: 6108 },
    source_coverage: { generation_count: 38605, timing: 38605, ttft: 31254, tokens_per_sec: 0 },
    by_model: [{ model: 'm', requests: 2, failed: 0, p50_ms: 10, p95_ms: 20, ttft_p50_ms: 1203, tokens_per_sec_p50: null }],
  });
  render(<Performance />);
  const median = screen.getByText('Median time to first token').closest('.metric') as HTMLElement;
  expect(within(median).getByText('1.2 s')).toBeInTheDocument();
  expect(within(median).getByText(/31,254 of 38,605 generations streamed/)).toBeInTheDocument();
  const table = screen.getByRole('table');
  expect(within(table).getByRole('columnheader', { name: 'TTFT P50' })).toBeInTheDocument();
  expect(within(table).getByText('1.2 s')).toBeInTheDocument();
});

it('shows a dash, not zero, for an unmeasured TTFT and a missing queue metric', () => {
  render(<Performance />);
  const ttft = screen.getByText('Median time to first token').closest('.metric')!;
  expect(within(ttft as HTMLElement).getByText('—')).toBeInTheDocument();
  const queue = screen.getByText('Gateway queue P95').closest('.metric')!;
  expect(within(queue as HTMLElement).getByText('—')).toBeInTheDocument();
  expect(within(queue as HTMLElement).getByText(/No verified queue measurement/)).toBeInTheDocument();
});

const row = (model: string, requests: number) => ({ model, requests, failed: 0, p50_ms: 100, p95_ms: 200, ttft_p50_ms: null, tokens_per_sec_p50: null });

it('has no filters; every model is a row and a donut slice', () => {
  state.data.by_model = [row('alpha', 7), row('beta', 3)] as GenerationLatency['by_model'];
  const { container } = render(<Performance />);
  expect(container.querySelector('.report-filters, select')).toBeNull();
  const panel = screen.getByRole('heading', { name: 'Performance by model' }).closest('section')!;
  expect([...panel.querySelectorAll('tbody td.entity')].map((td) => td.textContent)).toEqual(['alpha', 'beta']);
  expect(panel.querySelectorAll('.wc-slice')).toHaveLength(2);
});

it('while pending shows the header and panel titles with skeletons, not the session card', () => {
  range.pending = true;
  render(<Performance />);
  expect(screen.getByRole('heading', { name: 'Performance' })).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: 'Performance by model' })).toBeInTheDocument();
  expect(screen.getAllByRole('status', { name: 'Loading' }).length).toBeGreaterThan(0);
  expect(screen.queryByText(/Connecting to ACH/)).toBeNull();
  expect(screen.queryByText(/unavailable/i)).toBeNull();
});
