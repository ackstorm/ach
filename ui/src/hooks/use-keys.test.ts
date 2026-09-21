// use-keys.test.ts — vitest suite for the keys TanStack Query hooks (jsdom).
//
// The api module is fully mocked so NO real fetch happens; each test programs
// getJson/postJson/del's resolved { status, data }. Each test gets a FRESH
// QueryClient (retry disabled so error paths resolve immediately) provided via a
// renderHook wrapper. The fresh-keys store is reset between tests so the
// create/delete onSuccess side-effects are asserted in isolation.

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
  useChangeKeyTeam,
  useCreateKey,
  useDeleteKey,
  useHasDefaultKey,
  useKeys,
  useMakeDefault,
  useToggleKeyBlock,
} from './use-keys';
import { initialFreshKeysState, useFreshKeysStore } from '@/stores/fresh-keys';
import { useToastStore } from '@/hooks/use-toast';

const getJsonMock = vi.mocked(getJson);
const postJsonMock = vi.mocked(postJson);
const delMock = vi.mocked(del);

// A representative key list row (mirrors the api-types KeyRow contract).
const ROW: KeyRow = {
  id: 'key-1',
  key_alias: 'my-key',
  spend: 0,
  budget: null,
  tpm_limit: null,
  rpm_limit: null,
  models: ['all-team-models'],
  team_id: 'team-1',
  created_at: '2026-03-01T10:00:00+00:00',
  expires: null,
  last_used: null,
  is_default: false,
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
});

describe('useKeys', () => {
  it('200 + { keys: [row] } -> data is that KeyRow[]', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: { keys: [ROW] } });

    const { result } = renderHook(() => useKeys(), {
      wrapper: wrapperFor(makeClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual([ROW]);
    expect(getJsonMock).toHaveBeenCalledWith(
      '/api/session/keys',
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
  it('200 -> stores the fresh sk- AND invalidates the keys query', async () => {
    postJsonMock.mockResolvedValue({
      status: 200,
      data: { key: 'sk-abc', id: 'key-1', team_id: 'team-x' },
    });

    const client = makeClient();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(() => useCreateKey(), {
      wrapper: wrapperFor(client),
    });

    await result.current.mutateAsync({});

    expect(useFreshKeysStore.getState().freshKeys).toEqual({ 'key-1': 'sk-abc' });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: KEYS_QUERY_KEY });
  });

  it('non-200 (502) -> rejects AND does not stash a fresh key', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useCreateKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(result.current.mutateAsync({})).rejects.toThrow();
    expect(useFreshKeysStore.getState().freshKeys).toEqual({});
  });

  it('a 502 rejection carries status=502 + detail=null on the thrown error', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useCreateKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(result.current.mutateAsync({})).rejects.toMatchObject({
      status: 502,
      detail: null,
    });
  });

  it('a 422 rejection carries status=422 + the backend detail string', async () => {
    postJsonMock.mockResolvedValue({
      status: 422,
      data: { detail: 'alias must be 1-128 characters' },
    });

    const { result } = renderHook(() => useCreateKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(result.current.mutateAsync({})).rejects.toMatchObject({
      status: 422,
      detail: 'alias must be 1-128 characters',
    });
  });
});

describe('useDeleteKey', () => {
  it('200 -> drops the fresh key AND invalidates the keys query', async () => {
    // Pre-seed the store so dropFresh has something to remove.
    useFreshKeysStore.getState().setFresh('key-1', 'sk-abc');

    delMock.mockResolvedValue({
      status: 200,
      data: { status: 'deleted', id: 'key-1' },
    });

    const client = makeClient();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(() => useDeleteKey(), {
      wrapper: wrapperFor(client),
    });

    await result.current.mutateAsync('key-1');

    expect(useFreshKeysStore.getState().freshKeys).toEqual({});
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: KEYS_QUERY_KEY });
  });

  it('non-200 (403) -> rejects', async () => {
    delMock.mockResolvedValue({ status: 403, data: null });

    const { result } = renderHook(() => useDeleteKey(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(result.current.mutateAsync('key-x')).rejects.toThrow();
  });
});

describe('useMakeDefault', () => {
  it('200 -> POSTs the default endpoint AND invalidates the keys query', async () => {
    postJsonMock.mockResolvedValue({
      status: 200,
      data: { status: 'default', id: 'key-1' },
    });

    const client = makeClient();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(() => useMakeDefault(), {
      wrapper: wrapperFor(client),
    });

    await result.current.mutateAsync('key-1');

    expect(postJsonMock).toHaveBeenCalledWith('/api/session/keys/key-1/default', {});
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: KEYS_QUERY_KEY });
  });

  it('non-200 (502) -> rejects', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useMakeDefault(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(result.current.mutateAsync('key-x')).rejects.toThrow();
  });

  it('non-200 -> pushes an error toast', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });
    const toastSpy = vi.spyOn(useToastStore.getState(), 'toast');

    const { result } = renderHook(() => useMakeDefault(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(result.current.mutateAsync('key-x')).rejects.toThrow();
    await waitFor(() =>
      expect(toastSpy).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error' }),
      ),
    );
    toastSpy.mockRestore();
  });
});

describe('useChangeKeyTeam', () => {
  it('200 -> POSTs the team endpoint AND invalidates the keys query', async () => {
    postJsonMock.mockResolvedValue({
      status: 200,
      data: { status: 'moved', id: 'key-1', team_id: 'team-2' },
    });

    const client = makeClient();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(() => useChangeKeyTeam(), {
      wrapper: wrapperFor(client),
    });

    await result.current.mutateAsync({ id: 'key-1', teamId: 'team-2' });

    expect(postJsonMock).toHaveBeenCalledWith('/api/session/keys/key-1/team', {
      team_id: 'team-2',
    });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: KEYS_QUERY_KEY });
  });

  it('non-200 (502) -> rejects', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });

    const { result } = renderHook(() => useChangeKeyTeam(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(
      result.current.mutateAsync({ id: 'key-x', teamId: 'team-2' }),
    ).rejects.toThrow();
  });

  it('non-200 -> pushes an error toast', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });
    const toastSpy = vi.spyOn(useToastStore.getState(), 'toast');

    const { result } = renderHook(() => useChangeKeyTeam(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(
      result.current.mutateAsync({ id: 'key-x', teamId: 'team-2' }),
    ).rejects.toThrow();
    await waitFor(() =>
      expect(toastSpy).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error' }),
      ),
    );
    toastSpy.mockRestore();
  });
});

describe('useToggleKeyBlock', () => {
  it('non-200 -> pushes an error toast', async () => {
    postJsonMock.mockResolvedValue({ status: 502, data: null });
    const toastSpy = vi.spyOn(useToastStore.getState(), 'toast');

    const { result } = renderHook(() => useToggleKeyBlock(), {
      wrapper: wrapperFor(makeClient()),
    });

    await expect(
      result.current.mutateAsync({ id: 'key-x', blocked: true }),
    ).rejects.toThrow();
    await waitFor(() =>
      expect(toastSpy).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error' }),
      ),
    );
    toastSpy.mockRestore();
  });
});

describe('useHasDefaultKey', () => {
  it('true once a key with is_default loads', async () => {
    getJsonMock.mockResolvedValue({
      status: 200,
      data: { keys: [{ ...ROW, is_default: true }] },
    });
    const { result } = renderHook(() => useHasDefaultKey(), {
      wrapper: wrapperFor(makeClient()),
    });
    await waitFor(() => expect(result.current).toBe(true));
  });

  it('false when keys load but none is_default', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: { keys: [ROW] } });
    const { result } = renderHook(() => useHasDefaultKey(), {
      wrapper: wrapperFor(makeClient()),
    });
    await waitFor(() => expect(getJsonMock).toHaveBeenCalled());
    expect(result.current).toBe(false);
  });

  it('false while pending / on error (never throws)', () => {
    getJsonMock.mockResolvedValue({ status: 500, data: null });
    const { result } = renderHook(() => useHasDefaultKey(), {
      wrapper: wrapperFor(makeClient()),
    });
    expect(result.current).toBe(false);
  });
});
