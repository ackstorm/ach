// use-teams.test.ts — vitest suite for the session teams query hook (jsdom).
//
// The api module is mocked so NO real fetch happens; each test programs getJson's
// resolved { status, data }. Each test gets a FRESH QueryClient (retry disabled so
// the failure path resolves immediately) provided via a renderHook wrapper.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import type { Team } from '@/lib/api-types';

// Mock the api module: getJson is the only entrypoint this hook touches.
vi.mock('@/lib/api', () => ({
  getJson: vi.fn(),
}));

import { getJson } from '@/lib/api';
import { useTeams } from './use-teams';

const getJsonMock = vi.mocked(getJson);

// A representative team (mirrors the api-types Team contract).
const TEAM: Team = { id: 'team-platform', alias: 'platform' };

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

describe('useTeams', () => {
  it('200 + { teams: [team] } -> data is that Team[]', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: { teams: [TEAM] } });

    const { result } = renderHook(() => useTeams(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual([TEAM]);
    expect(getJsonMock).toHaveBeenCalledWith('/api/session/teams');
  });

  it('non-200 (502) -> resolves to [] (graceful, never throws)', async () => {
    getJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useTeams(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual([]);
    expect(result.current.isError).toBe(false);
  });
});
