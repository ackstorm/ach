// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/react';
import { ContextSkills } from './ContextSkills';

const state = vi.hoisted(() => ({ context: {} as Record<string, unknown> }));
vi.mock('@/hooks/use-evidence-reports', async (orig) => ({
  ...(await orig<typeof import('@/hooks/use-evidence-reports')>()),
  useEvidenceReport: () => state.context,
  useEvidenceWindow: () => ({ tooLong: false }),
}));
vi.mock('@/components/analytics/RangePicker', () => ({ RangePicker: () => null }));

const ok = { state: 'available', reason: null, sampled: false };
const report = (extra = {}) => ({ isPending: false, data: {
  provenance: ok, tool_provenance: ok, generation_count: 10, input_coverage: 10,
  input_summary: { mean: 104600, median: 83900, max: 407000 },
  token_ratio: { input_tokens: 1830000, output_tokens: 1000, value: 1830, uncached_input_tokens: 183000, uncached_value: 183, coverage: 10 },
  cache_read_share: { value: 0.95, coverage: 9 }, reasoning: { tokens: null, share: null, coverage: 0 },
  context_bands: [{ name: '<50K', count: 4 }, { name: '50–100K', count: 2 }, { name: '100–200K', count: 2 }, { name: '200–300K', count: 1 }, { name: '>=300K', count: 1 }],
  cache_read_tokens: 136750000, cache_coverage: 9, cache_creation_tokens: 5410000, cache_creation_coverage: 9,
  invocations: [{ name: 'Bash', count: 40 }], invocation_count: 52, skill_invocation_count: 7, subagent_invocation_count: 5,
  skills: [{ name: 'git:commit', count: 5 }], subagents: [{ name: 'Explore', count: 5 }], ...extra,
} });
afterEach(cleanup);

it('shows the input shape, skills and subagents by name from measured counts', () => {
  state.context = report();
  render(<ContextSkills />);
  expect(screen.getByText('183 : 1')).toBeInTheDocument();
  expect(screen.getByText('95.0%')).toBeInTheDocument();
  const skills = screen.getByRole('heading', { name: 'Most-used skills' }).closest('section')!;
  expect(within(skills).getByText('git:commit')).toBeInTheDocument();
  const agents = screen.getByRole('heading', { name: 'Delegated subagents' }).closest('section')!;
  expect(within(agents).getByText('Explore')).toBeInTheDocument();
  expect(screen.getByText(/Reasoning share/).parentElement).toHaveTextContent('—');
});

it('reads unread names and an unread tool source as unavailable, never zero', () => {
  state.context = report({ skills: null, tool_provenance: { state: 'unavailable', reason: 'source_unavailable', sampled: false } });
  render(<ContextSkills />);
  expect(screen.getAllByText('Tool calls could not be read.').length).toBeGreaterThan(0);
  expect(screen.queryByText('git:commit')).toBeNull();
});
