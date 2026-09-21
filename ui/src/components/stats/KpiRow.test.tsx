// KpiRow.test.tsx — jsdom smoke/contract suite for the STATS-02 KPI row.
//
// Asserts the 4 EXACT labels + representative formatted values, plus the
// period-over-period delta chips (rendered from totals.deltas; null pct -> no chip).

import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';

import type { StatsSeriesPoint, StatsTotals } from '@/lib/api-types';
import { KpiRow } from './KpiRow';

const TOTALS: StatsTotals = {
  requests: 2_450_000,
  tokens: 1_000_000,
  spend: 1249.5,
  failed_requests: 3,
  input_tokens: 980_000,
  output_tokens: 20_000,
  cache_read_tokens: 124_000,
  cache_hit_pct: 0.124,
  avg_cost_per_1m_tokens: 0.51,
  deltas: {
    requests_pct: 0.182,
    tokens_pct: -0.05,
    spend_pct: 0.1,
    avg_cost_per_1m_tokens_pct: 0.2,
  },
};

describe('KpiRow', () => {
  it('renders the four exact labels + representative values', () => {
    const { getByText } = render(<KpiRow totals={TOTALS} />);
    expect(getByText('TOTAL REQUESTS')).toBeInTheDocument();
    expect(getByText('TOTAL TOKENS')).toBeInTheDocument();
    expect(getByText('SPEND')).toBeInTheDocument();
    expect(getByText('AVG COST / 1M TOKENS')).toBeInTheDocument();
    // abbreviate(2_450_000) -> "2.45M"; formatCurrency(1249.5) -> "$1,249.50"
    expect(getByText('2.45M')).toBeInTheDocument();
    expect(getByText('$1,249.50')).toBeInTheDocument();
  });

  it('renders one delta chip per card with signed pct + direction color', () => {
    const { container } = render(<KpiRow totals={TOTALS} />);
    const chips = container.querySelectorAll('[data-slot="kpi-delta"]');
    expect(chips).toHaveLength(4);
    // requests +18.2% up = good -> primary
    expect(chips[0].textContent).toContain('+18.2%');
    expect(chips[0].className).toContain('text-primary');
    // tokens -5.0% down = bad -> destructive
    expect(chips[1].textContent).toContain('-5.0%');
    expect(chips[1].className).toContain('text-destructive');
    // spend +10.0% up = bad (inverted) -> destructive
    expect(chips[2].textContent).toContain('+10.0%');
    expect(chips[2].className).toContain('text-destructive');
  });

  it('renders NO chip when pct is null (no prior baseline to compare)', () => {
    // Prior baseline empty (e.g. prior window had 0 tokens) -> no % to compute and
    // no up/down to judge: render NOTHING, not a confusing "▲ 100% (no info)".
    const totals: StatsTotals = {
      ...TOTALS,
      deltas: {
        requests_pct: null,
        tokens_pct: null,
        spend_pct: null,
        avg_cost_per_1m_tokens_pct: null,
      },
    };
    const { container } = render(<KpiRow totals={totals} />);
    expect(container.querySelectorAll('[data-slot="kpi-delta"]')).toHaveLength(0);
  });

  it('renders NO chip when pct is null AND the current value is 0/absent', () => {
    const totals: StatsTotals = {
      ...TOTALS,
      requests: 0,
      tokens: 0,
      spend: 0,
      avg_cost_per_1m_tokens: null,
      deltas: {
        requests_pct: null,
        tokens_pct: null,
        spend_pct: null,
        avg_cost_per_1m_tokens_pct: null,
      },
    };
    const { container } = render(<KpiRow totals={totals} />);
    expect(container.querySelectorAll('[data-slot="kpi-delta"]')).toHaveLength(0);
  });

  it('renders a failed-requests sub-line when failed_requests > 0', () => {
    const { getByText } = render(<KpiRow totals={TOTALS} />);
    // 3 failed; 3 / 2_450_000 -> 0.0%.
    expect(getByText(/3 failed/)).toBeInTheDocument();
  });

  it('renders NO failed sub-line when failed_requests is 0', () => {
    const totals: StatsTotals = { ...TOTALS, failed_requests: 0 };
    const { container } = render(<KpiRow totals={totals} />);
    expect(container.querySelector('[data-slot="kpi-failed"]')).toBeNull();
  });

  it('shows the output-token share in the tokens sub-line', () => {
    const { container } = render(<KpiRow totals={TOTALS} />);
    const sub = container.querySelector('[data-slot="kpi-tokens"]');
    expect(sub?.textContent).toContain('out');
    // output 20_000 / total 1_000_000 -> 2.0%
    expect(sub?.textContent).toContain('2.0%');
    expect(sub?.getAttribute('title')).toMatch(/output/i);
  });

  it('omits the tokens sub-line when there are no output tokens', () => {
    const totals: StatsTotals = { ...TOTALS, output_tokens: 0 };
    const { container } = render(<KpiRow totals={totals} />);
    expect(container.querySelector('[data-slot="kpi-tokens"]')).toBeNull();
  });

  it('adds a spend pricing tooltip', () => {
    render(<KpiRow totals={TOTALS} />);
    expect(screen.getByTestId('kpi-spend-label').getAttribute('title')).toMatch(/cache/i);
  });

  it('renders a background sparkline on flow cards when series is given', () => {
    const series: StatsSeriesPoint[] = [
      { date: '2026-07-01', spend: 1, requests: 10, tokens: 100, input_tokens: 80, output_tokens: 20, failed: 0 },
      { date: '2026-07-02', spend: 3, requests: 40, tokens: 400, input_tokens: 320, output_tokens: 80, failed: 1 },
      { date: '2026-07-03', spend: 2, requests: 25, tokens: 250, input_tokens: 200, output_tokens: 50, failed: 0 },
    ];
    const { container } = render(<KpiRow totals={TOTALS} series={series} />);
    // One sparkline per flow card (requests/tokens/spend) — AVG COST has no spark.
    expect(container.querySelectorAll('[data-slot="kpi-spark"]')).toHaveLength(3);
  });

  it('renders no sparkline when series is omitted', () => {
    const { container } = render(<KpiRow totals={TOTALS} />);
    expect(container.querySelectorAll('[data-slot="kpi-spark"]')).toHaveLength(0);
  });

  it('renders a custom fourthCard in place of the AVG COST card', () => {
    // The KEYS tab reuses this row but swaps the 4th cell: cards 1-3 stay
    // (requests/tokens/spend), AVG COST is gone, and the node renders instead.
    const { getByText, queryByText } = render(
      <KpiRow totals={TOTALS} fourthCard={<div>KEYS &amp; TEAMS</div>} />,
    );
    expect(getByText('TOTAL REQUESTS')).toBeInTheDocument();
    expect(getByText('TOTAL TOKENS')).toBeInTheDocument();
    expect(getByText('SPEND')).toBeInTheDocument();
    expect(queryByText('AVG COST / 1M TOKENS')).not.toBeInTheDocument();
    expect(getByText('KEYS & TEAMS')).toBeInTheDocument();
  });
});
