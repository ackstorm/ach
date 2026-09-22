// use-latency.test.ts — vitest suite for the latency TanStack Query hook (jsdom).
//
// Mirrors use-stats.test.ts: the api module is fully mocked so NO real fetch
// happens; each test programs getJson's resolved { status, data }. Each test
// gets a FRESH QueryClient (retry disabled so the error path resolves
// immediately) provided via a renderHook wrapper.

import { describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import type { LatencyResponse } from '@/lib/api-types';

// Mock the api module: every entrypoint is a vi.fn() each test programs.
vi.mock('@/lib/api', () => ({
  getJson: vi.fn(),
  postJson: vi.fn(),
  del: vi.fn(),
}));

import { getJson } from '@/lib/api';
import { useLatency } from './use-latency';

const getJsonMock = vi.mocked(getJson);

// A minimal, type-valid LatencyResponse — the calm degraded shape (§10.2).
const LATENCY: LatencyResponse = {
  available: false,
  reason: 'unavailable',
  sampled: false,
  row_count: 0,
  window: null,
  latency: null,
  outcomes: [],
  by_model: [],
  data_scope: 'user',
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

describe('useLatency', () => {
  it('200 + LatencyResponse -> data is that object', async () => {
    getJsonMock.mockReset();
    getJsonMock.mockResolvedValue({ status: 200, data: LATENCY });

    const { result } = renderHook(
      () => useLatency({ start: '2026-05-01', end: '2026-05-31' }),
      { wrapper: wrapperFor(makeClient()) },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(LATENCY);
  });

  it('non-200 (502) -> isError', async () => {
    getJsonMock.mockReset();
    getJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(
      () => useLatency({ start: '2026-05-01', end: '2026-05-31' }),
      { wrapper: wrapperFor(makeClient()) },
    );

    await waitFor(() => expect(result.current.isError).toBe(true));
  });

  it('threads the range into the request URL + passes a signal', async () => {
    getJsonMock.mockReset();
    getJsonMock.mockResolvedValue({ status: 200, data: LATENCY });

    const { result } = renderHook(
      () => useLatency({ start: '2026-05-01', end: '2026-05-31' }),
      { wrapper: wrapperFor(makeClient()) },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const [url, options] = getJsonMock.mock.calls[0];
    expect(url).toContain('/platform/console/latency?');
    expect(url).toContain('start_date=2026-05-01');
    expect(url).toContain('end_date=2026-05-31');
    expect(options).toEqual(
      expect.objectContaining({ signal: expect.anything() }),
    );
  });
});
