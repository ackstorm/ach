// RequestsChart.test.tsx — vitest suite for the STATS-04 Requests-by-Day bar
// chart (jsdom).
//
// Recharts' ResponsiveContainer measures its parent (0x0 under jsdom) and
// renders nothing, so the populated branch never paints. We mock it to inject a
// fixed 600xCHART_HEIGHT size into its single child so the real chart renders.
// Tests stay pragmatic — they assert which BRANCH renders (loading skeleton /
// locked empty copy / a real SVG surface), never exact pixel paths.

import { cloneElement, isValidElement } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render } from '@testing-library/react';

import type { StatsSeriesPoint } from '@/lib/api-types';
import { CHART_EMPTY_COPY, CHART_HEIGHT } from './chart-common';

vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    ResponsiveContainer: ({ children }: { children: React.ReactNode }) =>
      isValidElement(children)
        ? cloneElement(children as React.ReactElement<{ width?: number; height?: number }>, {
            width: 600,
            height: CHART_HEIGHT,
          })
        : children,
  };
});

import { RequestsChart, RequestsMetricToggle } from './RequestsChart';

const SERIES: StatsSeriesPoint[] = [
  { date: '2026-03-01', spend: 1.5, requests: 10, tokens: 1234, input_tokens: 1000, output_tokens: 234, failed: 2 },
  { date: '2026-03-02', spend: 2.25, requests: 20, tokens: 5678, input_tokens: 4000, output_tokens: 1678, failed: 0 },
  { date: '2026-03-03', spend: 0, requests: 0, tokens: 0, input_tokens: 0, output_tokens: 0, failed: 0 },
];

describe('RequestsChart', () => {
  it('renders the chart skeleton while loading (no chart, no empty copy)', () => {
    const { container, queryByText } = render(<RequestsChart series={SERIES} loading />);
    const skeleton = container.querySelector('[data-slot="skeleton"]');
    expect(skeleton).not.toBeNull();
    expect(skeleton).toHaveAttribute('data-variant', 'chart');
    expect(queryByText(CHART_EMPTY_COPY)).toBeNull();
    expect(container.querySelector('.recharts-surface')).toBeNull();
  });

  it('renders the locked empty copy for an empty series (no surface)', () => {
    const { container, getByText } = render(<RequestsChart series={[]} />);
    expect(getByText(CHART_EMPTY_COPY)).toBeInTheDocument();
    expect(container.querySelector('.recharts-surface')).toBeNull();
  });

  it('renders the locked empty copy for a null series (no surface)', () => {
    const { container, getByText } = render(<RequestsChart series={null} />);
    expect(getByText(CHART_EMPTY_COPY)).toBeInTheDocument();
    expect(container.querySelector('.recharts-surface')).toBeNull();
  });

  it('renders a chart surface for a populated series (empty copy absent)', () => {
    const { container, queryByText } = render(<RequestsChart series={SERIES} />);
    expect(queryByText(CHART_EMPTY_COPY)).toBeNull();
    // A Recharts SVG surface exists; fall back to any svg if the class shifts.
    const surface =
      container.querySelector('.recharts-surface') ?? container.querySelector('svg');
    expect(surface).not.toBeNull();
  });

  it('renders the inline REQUESTS/TOKENS toggle and switches metric (uncontrolled)', () => {
    const { getByText } = render(<RequestsChart series={SERIES} />);
    const tokensBtn = getByText('tokens');
    expect(getByText('requests')).toBeInTheDocument();
    fireEvent.click(tokensBtn);
    // after the switch the tokens segment is the pressed one
    expect(tokensBtn).toHaveAttribute('aria-pressed', 'true');
  });

  it('renders real bars in requests mode (regression: no Fragment-wrapped bars)', () => {
    const { container } = render(<RequestsChart series={SERIES} metric="requests" />);
    // Recharts only emits .recharts-bar layers when it detects <Bar> as DIRECT
    // children — a Fragment wrapper would suppress them and blank the chart.
    expect(container.querySelectorAll('.recharts-bar').length).toBeGreaterThan(0);
  });

  it('renders a real bar in tokens mode', () => {
    const { container } = render(<RequestsChart series={SERIES} metric="tokens" />);
    expect(container.querySelectorAll('.recharts-bar').length).toBeGreaterThan(0);
  });

  it('does NOT render the inline toggle when the metric is controlled', () => {
    const { container } = render(
      <RequestsChart series={SERIES} metric="tokens" />
    );
    // the toggle is hoisted to the panel header by the container, so the chart
    // itself renders no metric buttons.
    expect(
      container.querySelector('[data-slot="requests-metric-tokens"]')
    ).toBeNull();
  });
});

describe('RequestsMetricToggle', () => {
  it('marks the active metric and calls onChange on click', () => {
    const onChange = vi.fn();
    const { getByText } = render(
      <RequestsMetricToggle metric="requests" onChange={onChange} />
    );
    expect(getByText('requests')).toHaveAttribute('aria-pressed', 'true');
    expect(getByText('tokens')).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(getByText('tokens'));
    expect(onChange).toHaveBeenCalledWith('tokens');
  });

  it('offers a failed metric in the toggle', () => {
    const { getByRole } = render(
      <RequestsMetricToggle metric="requests" onChange={() => {}} />
    );
    expect(getByRole('button', { name: /failed/i })).toBeInTheDocument();
  });
});
