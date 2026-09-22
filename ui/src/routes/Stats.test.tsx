// Stats.test.tsx — vitest suite for the #/stats Usage & Spend CONTAINER (jsdom).
//
// useStats is fully mocked so NO real fetch happens; each test programs its
// return to drive the loading / error / success branches. Recharts'
// ResponsiveContainer is mocked (same cloneElement trick as SpendChart.test.tsx)
// so the charts + donut paint a real SVG surface under jsdom. Tests are
// pragmatic — they assert which BRANCH renders + the locked copy, never pixels.

import { cloneElement, isValidElement } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import { CHART_HEIGHT } from '@/components/stats/chart-common';
import type { KeyRow, LatencyResponse, StatsResponse } from '@/lib/api-types';

// Inject a fixed size into ResponsiveContainer's single child so the populated
// chart/donut branch paints (jsdom measures the parent as 0x0 otherwise).
vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    ResponsiveContainer: ({ children }: { children: React.ReactNode }) =>
      isValidElement(children)
        ? cloneElement(
            children as React.ReactElement<{ width?: number; height?: number }>,
            { width: 600, height: CHART_HEIGHT },
          )
        : children,
  };
});

// Mock the stats hook — it drives the whole container's branch selection.
vi.mock('@/hooks/use-stats', () => ({
  useStats: vi.fn(),
}));

// useKeys backs the TOP API KEYS merge (idle keys). Mocked so no real fetch
// fires; defaults to an empty list (no padding) in beforeEach.
vi.mock('@/hooks/use-keys', () => ({
  useKeys: vi.fn(),
  KEYS_QUERY_KEY: ['session', 'keys'],
}));

// useLatency backs the LATENCY + REQUEST OUTCOMES panels (a separate endpoint).
// Mocked so no real fetch fires; defaults to the calm available:false state in
// beforeEach (the panels render their "not available" branch, no crash).
vi.mock('@/hooks/use-latency', () => ({
  useLatency: vi.fn(),
}));

import { useStats } from '@/hooks/use-stats';
import { useKeys } from '@/hooks/use-keys';
import { useLatency } from '@/hooks/use-latency';
import { Stats } from './Stats';

const useStatsMock = vi.mocked(useStats);
const useKeysMock = vi.mocked(useKeys);
const useLatencyMock = vi.mocked(useLatency);

// A minimal but valid StatsResponse fixture (one model row, one series point,
// one key row, a configured budget).
function makeStats(overrides: Partial<StatsResponse> = {}): StatsResponse {
  return {
    range: {
      start: '2026-02-01',
      end: '2026-03-02',
      days: 30,
      compare: { start: '2026-01-02', end: '2026-01-31' },
    },
    totals: {
      requests: 1234,
      tokens: 567890,
      spend: 12.5,
      failed_requests: 0,
      input_tokens: 460000,
      output_tokens: 107890,
      cache_read_tokens: 0,
      cache_hit_pct: null,
      avg_cost_per_1m_tokens: 0.01,
      deltas: {
        requests_pct: null,
        tokens_pct: null,
        spend_pct: null,
        avg_cost_per_1m_tokens_pct: null,
      },
    },
    series: [
      { date: '2026-03-01', spend: 1.5, requests: 10, tokens: 1234, input_tokens: 1000, output_tokens: 234, failed: 2 },
      { date: '2026-03-02', spend: 2.25, requests: 20, tokens: 5678, input_tokens: 4000, output_tokens: 1678, failed: 0 },
    ],
    models: [
      {
        model: 'gpt-4o',
        requests: 800,
        input_tokens: 100000,
        output_tokens: 50000,
        total_tokens: 150000,
        cache_read_tokens: 30000,
        spend: 10,
        spend_pct: 0.8,
        last_used: '2026-03-02T00:00:00+00:00',
      },
    ],
    keys: [
      {
        id: 'key-abc123',
        key_alias: 'my-key',
        requests: 800,
        spend: 10,
        spend_pct: 0.8,
      },
    ],
    budget: {
      current: 12.5,
      max_budget: 50,
      budget_duration: '30d',
      source: 'tag',
      pct: 25,
      has_budget: true,
    },
    capabilities: {
      token_split: true,
      per_model_last_used: true,
      deltas: true,
      per_key_spend: true,
    },
    data_scope: 'user',
    ...overrides,
  };
}

// Program useStats to the pending (loading) state.
function setPending(): void {
  useStatsMock.mockReturnValue({
    data: undefined,
    isPending: true,
    isError: false,
    isSuccess: false,
    refetch: vi.fn(),
  } as unknown as UseQueryResult<StatsResponse>);
}

// Program useStats to the error state, returning the (mock) refetch fn used.
function setError(): ReturnType<typeof vi.fn> {
  const refetch = vi.fn();
  useStatsMock.mockReturnValue({
    data: undefined,
    isPending: false,
    isError: true,
    isSuccess: false,
    refetch,
  } as unknown as UseQueryResult<StatsResponse>);
  return refetch;
}

// Program useStats to a populated success state.
function setSuccess(data: StatsResponse = makeStats()): void {
  useStatsMock.mockReturnValue({
    data,
    isPending: false,
    isError: false,
    isSuccess: true,
    refetch: vi.fn(),
  } as unknown as UseQueryResult<StatsResponse>);
}

// Default useKeys to an empty list before every test (mergeTopKeys then leaves
// the stats `keys[]` unchanged). Individual tests may override.
beforeEach(() => {
  useKeysMock.mockReturnValue({
    data: [],
    isPending: false,
    isError: false,
    isSuccess: true,
  } as unknown as UseQueryResult<KeyRow[]>);
  // Default the latency query to the calm degrade (available:false) so the two
  // supplementary panels render their "not available" branch without a fetch.
  useLatencyMock.mockReturnValue({
    data: {
      available: false,
      sampled: false,
      row_count: 0,
      window: null,
      latency: null,
      outcomes: [],
      by_model: [],
      data_scope: 'user',
    },
    isPending: false,
    isError: false,
    isSuccess: true,
  } as unknown as UseQueryResult<LatencyResponse>);
});

afterEach(() => {
  vi.clearAllMocks();
  cleanup();
});

describe('Stats — loading', () => {
  beforeEach(() => {
    setPending();
  });

  it('renders skeletons, keeps the title, and shows no error heading', () => {
    const { container } = render(<Stats />);
    // At least one per-panel skeleton is present.
    expect(
      container.querySelectorAll('[data-slot="skeleton"]').length,
    ).toBeGreaterThan(0);
    // The page title still renders during loading.
    expect(screen.getByText('Usage & Spend')).toBeInTheDocument();
    // The whole-page error heading is absent while loading.
    expect(screen.queryByText("Couldn't load usage")).not.toBeInTheDocument();
  });
});

describe('Stats — error', () => {
  it('shows the locked error heading and retry re-fires the query', () => {
    const refetch = setError();
    render(<Stats />);
    expect(screen.getByText("Couldn't load usage")).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'retry' }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });
});

describe('Stats — success', () => {
  beforeEach(() => {
    setSuccess();
  });

  it('renders the page title + sub copy', () => {
    render(<Stats />);
    expect(screen.getByText('Usage & Spend')).toBeInTheDocument();
    expect(
      screen.getByText(
        'Requests, tokens, models, and spend for the selected period.',
      ),
    ).toBeInTheDocument();
  });

  it('renders the user-global data-scope label under the title (spec §6)', () => {
    render(<Stats />);
    expect(
      screen.getByText('Personal statistics · Includes all your Environments.'),
    ).toBeInTheDocument();
  });

  it('renders all the section labels', () => {
    render(<Stats />);
    expect(screen.getByText('DAILY SPEND')).toBeInTheDocument();
    expect(screen.getByText('REQUESTS BY DAY')).toBeInTheDocument();
    expect(screen.getByText('USAGE BY MODEL')).toBeInTheDocument();
    expect(screen.getByText('USAGE BREAKDOWN')).toBeInTheDocument();
  });

  it('renders the KPI labels', () => {
    render(<Stats />);
    expect(screen.getByText('TOTAL REQUESTS')).toBeInTheDocument();
    expect(screen.getByText('TOTAL TOKENS')).toBeInTheDocument();
    // SPEND appears as both a KPI label and a column header; assert presence.
    expect(screen.getAllByText('SPEND').length).toBeGreaterThan(0);
    expect(screen.getByText('AVG COST / 1M TOKENS')).toBeInTheDocument();
  });

  it('renders a model row and the budget copy', () => {
    render(<Stats />);
    // The single fixture model — it appears in the donut legend AND the table.
    expect(screen.getAllByText('gpt-4o').length).toBeGreaterThan(0);
    // The budget panel shows the "{current} of {max}" copy for a set budget.
    expect(screen.getByText(/\$12\.50 of \$50\.00/)).toBeInTheDocument();
  });

  it('shows no skeletons and no error heading on success', () => {
    const { container } = render(<Stats />);
    expect(container.querySelectorAll('[data-slot="skeleton"]').length).toBe(0);
    expect(screen.queryByText("Couldn't load usage")).not.toBeInTheDocument();
  });

  it('renders one CSV export button (model breakdown only)', () => {
    const { container } = render(<Stats />);
    expect(
      container.querySelectorAll('[data-slot="export-csv"]').length
    ).toBe(1);
  });
});

describe('Stats — preset interaction', () => {
  beforeEach(() => {
    setSuccess();
  });

  const MS_PER_DAY = 86400000;
  const inclusiveDays = (r: { start: string; end: string }): number => {
    const start = Date.parse(`${r.start}T00:00:00Z`);
    const end = Date.parse(`${r.end}T00:00:00Z`);
    return Math.round((end - start) / MS_PER_DAY) + 1;
  };

  it('defaults to a 7-inclusive-day range (7d preset)', () => {
    render(<Stats />);
    const defaultCall = useStatsMock.mock.calls.at(-1)?.[0];
    expect(defaultCall).toBeDefined();
    expect(inclusiveDays(defaultCall!)).toBe(7);
  });

  it('clicking the 30d preset re-queries useStats with a 30-inclusive-day range', () => {
    render(<Stats />);

    // The default 7d range was the most-recent call before the click.
    const defaultCall = useStatsMock.mock.calls.at(-1)?.[0];
    expect(defaultCall).toBeDefined();

    fireEvent.click(screen.getByRole('button', { name: '30d' }));

    const latest = useStatsMock.mock.calls.at(-1)?.[0];
    expect(latest).toBeDefined();
    // The range changed from the 7d default.
    expect(latest).not.toEqual(defaultCall);
    // The 30d range spans exactly 30 inclusive UTC days (new-Date()-independent).
    expect(inclusiveDays(latest!)).toBe(30);
  });
});
