// UsageDonut.test.tsx — jsdom smoke/contract suite for the STATS-05
// Usage-by-Model SPEND donut (recharts).
//
// Recharts' ResponsiveContainer measures its parent (0x0 under jsdom) and
// renders nothing, so the populated branch never paints. We mock it the same way
// the chart tests do (SpendChart.test.tsx) to inject a fixed size into its single
// child so the real donut renders. Branches are asserted by capability + data.

import { cloneElement, isValidElement } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { render } from '@testing-library/react';

import type { StatsCapabilities, StatsModelRow } from '@/lib/api-types';
import { formatCurrency } from '@/lib/format';

vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    ResponsiveContainer: ({ children }: { children: React.ReactNode }) =>
      isValidElement(children)
        ? cloneElement(
            children as React.ReactElement<{ width?: number; height?: number }>,
            { width: 600, height: 200 }
          )
        : children,
  };
});

import { displayNames, UsageDonut } from './UsageDonut';

const CAPS: StatsCapabilities = {
  token_split: true,
  per_model_last_used: true,
  deltas: true,
  per_key_spend: true,
};

// A minimal StatsModelRow factory — only the fields the donut reads matter; the
// rest are filled with contract-shaped zeros.
function modelRow(
  model: string,
  spend: number,
  spend_pct: number | null
): StatsModelRow {
  return {
    model,
    requests: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    cache_read_tokens: 0,
    spend,
    spend_pct,
    last_used: null,
  };
}

const MODELS: StatsModelRow[] = [
  modelRow('gpt-4o', 12.5, 0.5),
  modelRow('claude-3', 7.5, 0.3),
];

describe('UsageDonut', () => {
  it('renders the ready donut (TOTAL caption + per-model legend + centered total)', () => {
    const { container, getByText } = render(
      <UsageDonut totalSpend={20} models={MODELS} capabilities={CAPS} />
    );
    expect(getByText('TOTAL')).toBeInTheDocument();
    // The legend lists each model by name.
    expect(getByText('gpt-4o')).toBeInTheDocument();
    expect(getByText('claude-3')).toBeInTheDocument();
    // The centered total is formatCurrency(totalSpend).
    expect(getByText(formatCurrency(20))).toBeInTheDocument();
    // No empty / coming-soon copy in the ready branch.
    expect(container.textContent).not.toContain('No usage in this range');
    expect(container.textContent).not.toContain('Coming soon');
    // A recharts SVG surface exists (mocked ResponsiveContainer renders it).
    const surface =
      container.querySelector('.recharts-surface') ?? container.querySelector('svg');
    expect(surface).not.toBeNull();
  });

  it('falls back to the slice-sum total when totalSpend is null', () => {
    const { getByText } = render(
      <UsageDonut totalSpend={null} models={MODELS} capabilities={CAPS} />
    );
    // 12.5 + 7.5 = 20 -> "$20.00".
    expect(getByText(formatCurrency(20))).toBeInTheDocument();
  });

  it('renders the empty copy for an empty models list', () => {
    const { getByText, container } = render(
      <UsageDonut totalSpend={0} models={[]} capabilities={CAPS} />
    );
    expect(getByText('No usage in this range')).toBeInTheDocument();
    expect(container.querySelector('svg')).toBeNull();
  });

  it('renders the empty copy for a null models list', () => {
    const { getByText, container } = render(
      <UsageDonut totalSpend={null} models={null} capabilities={CAPS} />
    );
    expect(getByText('No usage in this range')).toBeInTheDocument();
    expect(container.querySelector('svg')).toBeNull();
  });

  it('buckets Top-5 + Other: 7 models -> 6 legend rows, Other = sum of bottom 2', () => {
    const seven: StatsModelRow[] = [
      modelRow('m1', 100, 0.4),
      modelRow('m2', 80, 0.32),
      modelRow('m3', 40, 0.16),
      modelRow('m4', 20, 0.08),
      modelRow('m5', 6, 0.024),
      modelRow('m6', 3, 0.012),
      modelRow('m7', 1, 0.004),
    ];
    const { getByText, container } = render(
      <UsageDonut totalSpend={250} models={seven} capabilities={CAPS} />
    );
    // The legend is the 6 rows: m1..m5 + Other.
    const legend = container.querySelector('[data-slot="usage-donut-legend"]');
    expect(legend).not.toBeNull();
    expect(legend!.querySelectorAll('li')).toHaveLength(6);
    // Other is present, and its spend = m6 + m7 = 3 + 1 = 4 -> "$4.00".
    expect(getByText('Other')).toBeInTheDocument();
    expect(getByText(formatCurrency(4))).toBeInTheDocument();
    // The top-5 names render; m6/m7 are folded into Other (not shown).
    expect(getByText('m1')).toBeInTheDocument();
    expect(getByText('m5')).toBeInTheDocument();
    expect(container.textContent).not.toContain('m6');
    expect(container.textContent).not.toContain('m7');
  });

  it('renders an em-dash for a null spend_pct slice', () => {
    const { getByText } = render(
      <UsageDonut
        totalSpend={5}
        models={[modelRow('only', 5, null)]}
        capabilities={CAPS}
      />
    );
    expect(getByText('only')).toBeInTheDocument();
    // The pct cell is the em-dash (U+2014) for a null spend_pct.
    expect(getByText('—')).toBeInTheDocument();
  });

  it('renders the coming-soon copy when per_model_spend capability is false', () => {
    const caps = { ...CAPS, per_model_spend: false } as unknown as StatsCapabilities;
    const { getByText, container } = render(
      <UsageDonut totalSpend={20} models={MODELS} capabilities={caps} />
    );
    expect(getByText('Coming soon')).toBeInTheDocument();
    // The coming-soon panel still shows the TOTAL caption + an em-dash total.
    expect(getByText('TOTAL')).toBeInTheDocument();
    expect(container.querySelector('svg')).toBeNull();
  });
});

describe('displayNames', () => {
  const slice = (model: string, isOther = false) => ({
    model, spend: 1, spend_pct: 0.5, isOther,
  });

  it('strips the shared provider/ prefix when ALL named slices share it', () => {
    const slices = [slice('gemini/gemini-3-pro'), slice('gemini/gemini-flash')];
    expect(displayNames(slices)).toEqual(['gemini-3-pro', 'gemini-flash']);
  });

  it('keeps full names when prefixes differ or are absent', () => {
    expect(
      displayNames([slice('gemini/gemini-3-pro'), slice('gpt-4o')])
    ).toEqual(['gemini/gemini-3-pro', 'gpt-4o']);
  });

  it('never strips the Other slice and ignores it for prefix detection', () => {
    const slices = [slice('gemini/a'), slice('gemini/b'), slice('Other', true)];
    expect(displayNames(slices)).toEqual(['a', 'b', 'Other']);
  });
});

describe('legend tooltip', () => {
  it('puts the FULL model name in the legend title attribute', () => {
    const models: StatsModelRow[] = [
      {
        model: 'gemini/gemini-3-pro-preview',
        requests: 1, input_tokens: 1, output_tokens: 1, total_tokens: 2,
        cache_read_tokens: 0, spend: 5, spend_pct: 1, last_used: null,
      },
    ];
    const { container } = render(
      <UsageDonut models={models} totalSpend={5} capabilities={CAPS} />
    );
    const item = container.querySelector(
      '[data-slot="usage-donut-legend"] [title="gemini/gemini-3-pro-preview"]'
    );
    expect(item).not.toBeNull();
  });
});
