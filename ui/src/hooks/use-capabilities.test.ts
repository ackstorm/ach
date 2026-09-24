// use-capabilities.test.ts — vitest suite for the personal capabilities
// TanStack Query hooks (jsdom).
//
// The api module is fully mocked so NO real fetch happens; each test programs
// getJson's resolved { status, data }. Each test gets a FRESH QueryClient
// (retry disabled so error paths resolve immediately) provided via a
// renderHook wrapper (pattern copied from use-keys.test.ts). The session
// store is seeded with a fixed `me.email` (CAPABILITIES_QUERY_KEY carries
// identity, AC-04, mirroring KEYS_QUERY_KEY).

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import type { CapabilitiesResponse } from '@/lib/api-types';

// Mock the api module: every entrypoint is a vi.fn() each test programs.
vi.mock('@/lib/api', () => ({
  getJson: vi.fn(),
  postJson: vi.fn(),
  del: vi.fn(),
}));

import { getJson } from '@/lib/api';
import {
  CAPABILITIES_QUERY_KEY,
  useA2a,
  useCapabilities,
  useMcp,
  useModels,
} from './use-capabilities';
import { initialSessionState, useSessionStore } from '@/stores/session';

const getJsonMock = vi.mocked(getJson);

const EMAIL = 'alice@example.com';

const CAPS: CapabilitiesResponse = {
  scope: 'personal',
  models: [
    {
      name: 'ackstorm.fast',
      providers: ['openai'],
      mode: 'chat',
      max_input_tokens: 128000,
      max_output_tokens: 16384,
      input_cost_per_token: 1.5e-7,
      output_cost_per_token: 6e-7,
      supports_vision: false,
      supports_function_calling: true,
      supports_reasoning: false,
      supports_web_search: false,
    },
  ],
  mcp_servers: [
    {
      id: 'github-mcp',
      name: 'GitHub',
      description: 'Repos and issues.',
      url: 'https://mcp.internal/github',
      transport: 'http',
      auth_type: 'oauth2',
      status: 'healthy',
      tools: ['list_repos'],
      tool_count: 1,
      access_groups: [],
    },
  ],
  a2a_agents: [
    {
      id: 'research-agent',
      name: 'Research Agent',
      description: 'Web research.',
      url: 'https://a2a.internal/research',
      transport: 'JSONRPC',
      version: '1.0.0',
      skills: ['deep_research'],
      skill_count: 1,
      streaming: true,
    },
  ],
  provisioning: false,
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

beforeEach(() => {
  getJsonMock.mockReset();
  // Seed the session store with a fixed identity (CAPABILITIES_QUERY_KEY reads it).
  const { loadSession, markExpired } = useSessionStore.getState();
  useSessionStore.setState(
    {
      ...initialSessionState,
      me: {
        email: EMAIL,
        name: EMAIL,
        is_admin: false,
        openwork_enabled: false,
        suspend_propagation_seconds: 60,
        keys_used: null,
        max_keys: null,
        endpoint: 'https://litellm.example.com',
        chat_url: '',
      },
      loadSession,
      markExpired,
    },
    true,
  );
});

describe('CAPABILITIES_QUERY_KEY', () => {
  it('carries the caller identity (AC-04)', () => {
    expect(CAPABILITIES_QUERY_KEY(EMAIL)).toEqual(['capabilities', 'personal', EMAIL]);
    expect(CAPABILITIES_QUERY_KEY(undefined)).toEqual(['capabilities', 'personal', null]);
  });
});

describe('useCapabilities', () => {
  it('200 + CapabilitiesResponse -> data is that object', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: CAPS });

    const { result } = renderHook(() => useCapabilities(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(CAPS);
    expect(getJsonMock).toHaveBeenCalledWith(
      '/platform/console/capabilities?scope=personal',
      expect.objectContaining({ signal: expect.anything() }),
    );
  });

  it('non-200 (502) -> isError', async () => {
    getJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useCapabilities(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isError).toBe(true));
  });

  it('non-200 (503, key material provisioning) -> isError', async () => {
    getJsonMock.mockResolvedValue({ status: 503, data: null });

    const { result } = renderHook(() => useCapabilities(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isError).toBe(true));
  });
});

describe('useModels / useMcp / useA2a — share ONE fetch', () => {
  it('mounting all three under the same client fires exactly one network call', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: CAPS });
    const client = makeClient();

    const models = renderHook(() => useModels(), { wrapper: wrapperFor(client) });
    const mcp = renderHook(() => useMcp(), { wrapper: wrapperFor(client) });
    const a2a = renderHook(() => useA2a(), { wrapper: wrapperFor(client) });

    await waitFor(() => expect(models.result.current.isSuccess).toBe(true));
    await waitFor(() => expect(mcp.result.current.isSuccess).toBe(true));
    await waitFor(() => expect(a2a.result.current.isSuccess).toBe(true));

    expect(getJsonMock).toHaveBeenCalledTimes(1);
  });

  it('useModels selects {models, provisioning}', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: CAPS });

    const { result } = renderHook(() => useModels(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual({ models: CAPS.models, provisioning: false });
  });

  it('useMcp selects {servers, provisioning}', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: CAPS });

    const { result } = renderHook(() => useMcp(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual({
      servers: CAPS.mcp_servers,
      provisioning: false,
    });
  });

  it('useA2a selects {agents, provisioning}', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: CAPS });

    const { result } = renderHook(() => useA2a(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual({
      agents: CAPS.a2a_agents,
      provisioning: false,
    });
  });

  it('non-200 -> each selector lands in isError', async () => {
    getJsonMock.mockResolvedValue({ status: 502, data: null });
    const client = makeClient();

    const models = renderHook(() => useModels(), { wrapper: wrapperFor(client) });
    const mcp = renderHook(() => useMcp(), { wrapper: wrapperFor(client) });
    const a2a = renderHook(() => useA2a(), { wrapper: wrapperFor(client) });

    await waitFor(() => expect(models.result.current.isError).toBe(true));
    await waitFor(() => expect(mcp.result.current.isError).toBe(true));
    await waitFor(() => expect(a2a.result.current.isError).toBe(true));
  });
});
