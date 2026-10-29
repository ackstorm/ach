// use-environments.test.ts — vitest suite for the Environments list hook
// (jsdom). Mirrors use-teams.test.ts.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import type { EnvironmentRow } from '@/lib/api-types';

// Mock the api module: getJson is the only entrypoint this hook touches.
vi.mock('@/lib/api', () => ({
  getJson: vi.fn(),
}));

import { getJson } from '@/lib/api';
import { useEnvironments } from './use-environments';

const getJsonMock = vi.mocked(getJson);

const ENV: EnvironmentRow = { name: 'prod', status: 'Available' };

/** Build a fresh QueryClient with retries off so the failure test resolves fast. */
function makeClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

/** renderHook wrapper providing a given QueryClient. */
function wrapperFor(client: QueryClient) {
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

beforeEach(() => {
  getJsonMock.mockReset();
});

describe('useEnvironments', () => {
  it('200 + { items: [env] } -> data is that EnvironmentRow[]', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: { items: [ENV], next_cursor: null } });

    const { result } = renderHook(() => useEnvironments(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual([ENV]);
    expect(getJsonMock).toHaveBeenCalledWith('/platform/environments?limit=500');
  });

  it('non-200 (502) -> resolves to [] (graceful, never throws)', async () => {
    getJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useEnvironments(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual([]);
    expect(result.current.isError).toBe(false);
  });
});
