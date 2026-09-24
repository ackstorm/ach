// use-keys.test.ts — vitest suite for the ACH env-key TanStack Query hooks
// (jsdom).
//
// The api module is fully mocked so NO real fetch happens; each test programs
// getJson/postJson/del's resolved { status, data }. Each test gets a FRESH
// QueryClient (retry disabled so error paths resolve immediately) provided via a
// renderHook wrapper. The fresh-keys store is reset between tests so the
// create/delete onSuccess side-effects are asserted in isolation. The session
// store is seeded with a fixed `me.email` (KEYS_QUERY_KEY carries identity,
// AC-04).

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import type { KeyRow } from '@/lib/api-types';

// Mock the api module: every entrypoint is a vi.fn() each test programs.
vi.mock('@/lib/api', () => ({
  getJson: vi.fn(),
  postJson: vi.fn(),
  del: vi.fn(),
}));

import { del, getJson, postJson } from '@/lib/api';
import {
  KEYS_QUERY_KEY,
  useCreateKey,
  useDeleteKey,
  useKeys,
  useResumeKey,
  useSuspendKey,
} from './use-keys';
import { initialFreshKeysState, useFreshKeysStore } from '@/stores/fresh-keys';
import { initialSessionState, useSessionStore } from '@/stores/session';
import { useToastStore } from '@/hooks/use-toast';

const getJsonMock = vi.mocked(getJson);
const postJsonMock = vi.mocked(postJson);
const delMock = vi.mocked(del);

const EMAIL = 'alice@example.com';

// A representative key list row (mirrors render.KeyListRow).
const ROW: KeyRow = {
  key_id: 'ekid_1',
  type: 'ek',
  owner_email: EMAIL,
  environment: 'prod',
  name: 'my-key',
  status: 'active',
  created_at: '2026-03-01T10:00:00Z',
  expires_at: null,
};

/** Build a fresh QueryClient with retries off so error tests resolve fast. */
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
  postJsonMock.mockReset();
  delMock.mockReset();
  // Reset the fresh-keys store to its empty initial state, keeping the actions.
  const { setFresh, dropFresh } = useFreshKeysStore.getState();
  useFreshKeysStore.setState({ ...initialFreshKeysState, setFresh, dropFresh }, true);
  // Seed the session store with a fixed identity (KEYS_QUERY_KEY reads it).
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

describe('useKeys', () => {
  it('200 + { items: [row] } -> data is that KeyRow[]', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: { items: [ROW], next_cursor: null } });

    const { result } = renderHook(() => useKeys(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual([ROW]);
    expect(getJsonMock).toHaveBeenCalledWith(
      '/platform/keys?type=ek&limit=500',
      expect.objectContaining({ signal: expect.anything() }),
    );
  });

  it('non-200 (502) -> isError', async () => {
    getJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useKeys(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isError).toBe(true));
  });
});

describe('useCreateKey', () => {
  it('200 -> stores the fresh plaintext under key_id AND invalidates the keys query', async () => {
    postJsonMock.mockResolvedValue({
      status: 200,
      data: {
        key_id: 'ekid_1',
        plaintext: 'ek-abc',
        environment: 'prod',
        name: 'my-key',
        owner_email: EMAIL,
        created_at: '2026-03-01T10:00:00Z',
        expires_at: null,
      },
    });

    const client = makeClient();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(() => useCreateKey(), {
      wrapper: wrapperFor(client),
    });

    await result.current.mutateAsync({ environment: 'prod', name: 'my-key' });

    expect(postJsonMock).toHaveBeenCalledWith('/platform/keys', {
      environment: 'prod',
      name: 'my-key',
    });
    expect(useFreshKeysStore.getState().freshKeys).toEqual({ ekid_1: 'ek-abc' });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: KEYS_QUERY_KEY(EMAIL) });
  });

  it('non-200 (502) -> rejects AND does not stash a fresh key', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useCreateKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(
      result.current.mutateAsync({ environment: 'prod', name: 'x' }),
    ).rejects.toThrow();
    expect(useFreshKeysStore.getState().freshKeys).toEqual({});
  });

  it('a 502 rejection carries status=502 + detail=null on the thrown error', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useCreateKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(
      result.current.mutateAsync({ environment: 'prod', name: 'x' }),
    ).rejects.toMatchObject({ status: 502, detail: null });
  });

  it('a 400 rejection reads ACH error envelope error.message as detail', async () => {
    postJsonMock.mockResolvedValue({
      status: 400,
      data: { error: { code: 'invalid_argument', message: 'environment and name required' } },
    });

    const { result } = renderHook(() => useCreateKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(
      result.current.mutateAsync({ environment: '', name: '' }),
    ).rejects.toMatchObject({ status: 400, detail: 'environment and name required' });
  });
});

describe('useDeleteKey', () => {
  it('204 -> drops the fresh key AND invalidates the keys query', async () => {
    // Pre-seed the store so dropFresh has something to remove.
    useFreshKeysStore.getState().setFresh('ekid_1', 'ek-abc');

    delMock.mockResolvedValue({ status: 204, data: null });

    const client = makeClient();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(() => useDeleteKey(), {
      wrapper: wrapperFor(client),
    });

    await result.current.mutateAsync('ekid_1');

    expect(delMock).toHaveBeenCalledWith('/platform/keys/ekid_1');
    expect(useFreshKeysStore.getState().freshKeys).toEqual({});
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: KEYS_QUERY_KEY(EMAIL) });
  });

  it('non-204 (403) -> rejects', async () => {
    delMock.mockResolvedValue({ status: 403, data: null });

    const { result } = renderHook(() => useDeleteKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(result.current.mutateAsync('ekid_x')).rejects.toThrow();
  });
});

describe('useSuspendKey', () => {
  it('204 -> POSTs the suspend endpoint AND invalidates the keys query', async () => {
    postJsonMock.mockResolvedValue({ status: 204, data: null });

    const client = makeClient();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(() => useSuspendKey(), {
      wrapper: wrapperFor(client),
    });

    await result.current.mutateAsync('ekid_1');

    expect(postJsonMock).toHaveBeenCalledWith('/platform/keys/ekid_1/suspend', {});
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: KEYS_QUERY_KEY(EMAIL) });
  });

  it('non-204 -> pushes an error toast', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });
    const toastSpy = vi.spyOn(useToastStore.getState(), 'toast');

    const { result } = renderHook(() => useSuspendKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(result.current.mutateAsync('ekid_x')).rejects.toThrow();
    await waitFor(() =>
      expect(toastSpy).toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' })),
    );
    toastSpy.mockRestore();
  });
});

describe('useResumeKey', () => {
  it('204 -> POSTs the resume endpoint AND invalidates the keys query', async () => {
    postJsonMock.mockResolvedValue({ status: 204, data: null });

    const client = makeClient();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(() => useResumeKey(), {
      wrapper: wrapperFor(client),
    });

    await result.current.mutateAsync('ekid_1');

    expect(postJsonMock).toHaveBeenCalledWith('/platform/keys/ekid_1/resume', {});
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: KEYS_QUERY_KEY(EMAIL) });
  });

  it('non-204 -> pushes an error toast', async () => {
    postJsonMock.mockResolvedValue({ status: 409, data: null });
    const toastSpy = vi.spyOn(useToastStore.getState(), 'toast');

    const { result } = renderHook(() => useResumeKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(result.current.mutateAsync('ekid_x')).rejects.toThrow();
    await waitFor(() =>
      expect(toastSpy).toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' })),
    );
    toastSpy.mockRestore();
  });
});
