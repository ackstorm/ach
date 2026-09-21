// use-stats.test.ts — vitest suite for the stats TanStack Query hook (jsdom).
//
// The api module is fully mocked so NO real fetch happens; each test programs
// getJson's resolved { status, data }. Each test gets a FRESH QueryClient (retry
// disabled so the error path resolves immediately) provided via a renderHook
// wrapper (pattern copied from use-keys.test.ts).

import { describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import type { StatsResponse } from '@/lib/api-types';

// Mock the api module: every entrypoint is a vi.fn() each test programs.
vi.mock('@/lib/api', () => ({
  getJson: vi.fn(),
  postJson: vi.fn(),
  del: vi.fn(),
}));

import { getJson } from '@/lib/api';
import { useStats } from './use-stats';

const getJsonMock = vi.mocked(getJson);

// A minimal, type-valid StatsResponse (mirrors the api-types contract).
const STATS: StatsResponse = {
  range: {
    start: '2026-05-01',
    end: '2026-05-31',
    days: 31,
    compare: { start: '2026-03-31', end: '2026-04-30' },
  },
  totals: {
    requests: 0,
    tokens: 0,
    spend: 0,
    failed_requests: 0,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_hit_pct: null,
    avg_cost_per_1m_tokens: null,
    deltas: {
      requests_pct: null,
      tokens_pct: null,
      spend_pct: null,
      avg_cost_per_1m_tokens_pct: null,
    },
  },
  series: [],
  models: [],
  keys: [],
  budget: {
    current: 0,
    max_budget: null,
    budget_duration: null,
    source: 'unknown',
    pct: null,
    has_budget: false,
  },
  capabilities: {
    token_split: false,
    per_model_last_used: false,
    deltas: false,
    per_key_spend: false,
  },
};

/** Build a fresh QueryClient with retries off so error tests resolve fast. */
function makeClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });
}

/** renderHook wrapper providing a given QueryClient. */
function wrapperFor(client: QueryClient) {
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

describe('useStats', () => {
  it('200 + StatsResponse -> data is that object', async () => {
    getJsonMock.mockReset();
    getJsonMock.mockResolvedValue({ status: 200, data: STATS });

    const { result } = renderHook(
      () => useStats({ start: '2026-05-01', end: '2026-05-31' }),
      { wrapper: wrapperFor(makeClient()) },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(STATS);
  });

  it('non-200 (502) -> isError', async () => {
    getJsonMock.mockReset();
    getJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(
      () => useStats({ start: '2026-05-01', end: '2026-05-31' }),
      { wrapper: wrapperFor(makeClient()) },
    );

    await waitFor(() => expect(result.current.isError).toBe(true));
  });

  it('threads the range into the request URL + passes a signal', async () => {
    getJsonMock.mockReset();
    getJsonMock.mockResolvedValue({ status: 200, data: STATS });

    const { result } = renderHook(
      () => useStats({ start: '2026-05-01', end: '2026-05-31' }),
      { wrapper: wrapperFor(makeClient()) },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const [url, options] = getJsonMock.mock.calls[0];
    expect(url).toContain('start_date=2026-05-01');
    expect(url).toContain('end_date=2026-05-31');
    expect(options).toEqual(
      expect.objectContaining({ signal: expect.anything() }),
    );
  });
});
