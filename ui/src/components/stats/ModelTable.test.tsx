// ModelTable.test.tsx — jsdom smoke/contract suite for the STATS-06 model table.
//
// Asserts the 8 EXACT column headers + a representative row, the empty state,
// and capability gating (token_split / per_model_last_used -> em-dash cells).

import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';

import type { StatsCapabilities, StatsModelRow } from '@/lib/api-types';
import { ModelTable } from './ModelTable';

const CAPS: StatsCapabilities = {
  token_split: true,
  per_model_last_used: true,
  deltas: true,
  per_key_spend: true,
};

const MODELS: StatsModelRow[] = [
  {
    model: 'gpt-4o',
    requests: 1234,
    input_tokens: 600_000,
    output_tokens: 400_000,
    total_tokens: 1_000_000,
    cache_read_tokens: 250_000,
    spend: 12.5,
    spend_pct: 0.6,
    last_used: '2026-03-03T05:35:44.827000+00:00',
  },
  {
    model: 'claude-3',
    requests: 500,
    input_tokens: 100_000,
    output_tokens: 50_000,
    total_tokens: 150_000,
    cache_read_tokens: 0,
    spend: 3.25,
    spend_pct: 0.2,
    last_used: null,
  },
];

describe('ModelTable', () => {
  it('renders the exact column headers (TYPE + NAME prefix the metrics)', () => {
    const { getByText } = render(
      <ModelTable models={MODELS} capabilities={CAPS} />
    );
    for (const h of [
      'TYPE',
      'NAME',
      'REQUESTS',
      'INPUT',
      'OUTPUT',
      'TOTAL',
      'CACHED',
      'SPEND',
      '% SPEND',
      'LAST USED',
    ]) {
      expect(getByText(h)).toBeInTheDocument();
    }
  });

  it('renders a representative row (model name + formatted spend/%)', () => {
    const { getByText } = render(
      <ModelTable models={MODELS} capabilities={CAPS} />
    );
    expect(getByText('gpt-4o')).toBeInTheDocument();
    expect(getByText('$12.50')).toBeInTheDocument(); // spend
    expect(getByText('60.0%')).toBeInTheDocument(); // spend_pct 0.6 -> 60.0%
  });

  it('renders the abbreviated cached-input tokens per row', () => {
    const { getByText } = render(
      <ModelTable models={MODELS} capabilities={CAPS} />
    );
    // gpt-4o cache_read_tokens 250_000 -> "250K".
    expect(getByText('250K')).toBeInTheDocument();
  });

  it('collapses INPUT/OUTPUT cells to em-dash when token_split is false', () => {
    const caps: StatsCapabilities = { ...CAPS, token_split: false };
    const { container } = render(
      <ModelTable models={MODELS} capabilities={caps} />
    );
    const inputCells = container.querySelectorAll('td[data-col="input"]');
    expect(inputCells.length).toBeGreaterThan(0);
    inputCells.forEach((c) => expect(c.textContent).toBe('—'));
  });

  it('collapses LAST USED to em-dash when per_model_last_used is false', () => {
    const caps: StatsCapabilities = { ...CAPS, per_model_last_used: false };
    const { container } = render(
      <ModelTable models={MODELS} capabilities={caps} />
    );
    const cells = container.querySelectorAll('td[data-col="lastused"]');
    cells.forEach((c) => expect(c.textContent).toBe('—'));
  });

  it('renders the empty copy for no rows', () => {
    const { getByText } = render(<ModelTable models={[]} capabilities={CAPS} />);
    expect(getByText('No usage in this range')).toBeInTheDocument();
  });

  it('tags MCP rows and blanks their token/cost cells', () => {
    const base = {
      requests: 3,
      input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
      cache_read_tokens: 0,
      spend: 0,
      spend_pct: 0,
      last_used: null,
    };
    const { getByText, getByTestId } = render(
      <ModelTable
        capabilities={null}
        models={[
          { ...base, model: 'gemini/flash', spend: 1.5, total_tokens: 1000 },
          { ...base, model: 'MCP: mcp-gitlab.gitlab_api' },
        ]}
      />,
    );
    expect(getByText('MCP Tool')).toBeInTheDocument();
    expect(getByText('mcp-gitlab.gitlab_api')).toBeInTheDocument();
    // MCP row shows em-dash (not $0.00) for spend.
    expect(getByTestId('usage-spend-mcp').textContent).toBe('—');
  });
});
