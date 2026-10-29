// use-keys.ts — TanStack Query hooks for the ACH env-key lifecycle
// (internal/platformapi/envkeys — GET/POST /platform/keys + suspend/resume).
//
//   useKeys()        GET    /platform/keys?type=ek&limit=500 -> KeyRow[] (throws
//                                                    on non-200 so the table
//                                                    renders its error branch;
//                                                    a 200 + empty items is the
//                                                    empty state)
//   useCreateKey()    POST   /platform/keys          -> CreateKeyResponse
//                                                    (stashes the one-time
//                                                    plaintext in the in-memory
//                                                    fresh-keys store, then
//                                                    invalidates the list)
//   useDeleteKey()    DELETE /platform/keys/{id}     -> 204 no body
//   useSuspendKey()   POST   /platform/keys/{id}/suspend -> 204 no body
//   useResumeKey()    POST   /platform/keys/{id}/resume  -> 204 no body
//
// Cancellation: useKeys threads TanStack Query's `{ signal }` into getJson so an
// unmount / superseding refetch aborts the in-flight fetch; apiFetch re-throws
// that AbortError, letting Query treat it as a cancellation instead of a fake
// network error. Mutations are NOT cancelled.
//
// KEYS_QUERY_KEY is a FUNCTION of the caller's email (AC-04): the cache key
// carries identity so a stale list from a previous session can never leak
// across a logout/login in the same tab. Every hook reads the email from
// useSessionStore and threads it through identically.
//
// Invalidation uses the provider's client via useQueryClient() (idiomatic), NOT
// a singleton import — so tests can inject their own client.

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { del, getJson, postJson, type ApiResult } from '@/lib/api';
import type { CreateKeyBody, CreateKeyResponse, KeyRow, KeysResponse } from '@/lib/api-types';
import { useFreshKeysStore } from '@/stores/fresh-keys';
import { useSessionStore } from '@/stores/session';
import { useToast } from '@/hooks/use-toast';

/** Query key for the keys list, scoped to the caller's identity (AC-04). */
export function KEYS_QUERY_KEY(email: string | null | undefined) {
  return ['keys', email ?? null] as const;
}

/**
 * An Error carrying the backend HTTP `status` + parsed `detail`. `detail` reads
 * ACH's §15.5 error envelope (`{ error: { code, message }, request_id }`) — see
 * internal/platformapi/render/json.go:52 — never a bespoke `detail` field.
 */
export interface ApiCallError extends Error {
  status: number;
  detail: string | null;
}

/**
 * Build an ApiCallError from a parsed (never-throw api wrapper) response. Reads
 * `data.error.message` defensively — anything else (or a null body, e.g. a 502)
 * yields `detail: null`.
 */
function apiCallError(message: string, status: number, data: unknown): ApiCallError {
  const detail =
    data &&
    typeof data === 'object' &&
    'error' in data &&
    typeof (data as { error: unknown }).error === 'object' &&
    (data as { error: { message?: unknown } | null }).error !== null &&
    typeof (data as { error: { message?: unknown } }).error.message === 'string'
      ? (data as { error: { message: string } }).error.message
      : null;
  return Object.assign(new Error(message), { status, detail });
}

/**
 * GET /platform/keys?type=ek&limit=500. Throws on a non-200 / malformed
 * response so the query lands in `isError`; a 200 with an `items` array
 * resolves to `KeyRow[]` (an empty array is the empty state, not an error).
 */
export function useKeys() {
  const email = useSessionStore((s) => s.me?.email);
  return useQuery({
    queryKey: KEYS_QUERY_KEY(email),
    queryFn: async ({ signal }): Promise<KeyRow[]> => {
      const { status, data } = await getJson<KeysResponse>(
        '/platform/keys?type=ek&limit=500',
        { signal },
      );
      if (status === 200 && data && Array.isArray(data.items)) return data.items;
      throw new Error('keys-load-failed');
    },
  });
}

/**
 * Shared factory for the key-lifecycle mutations. Each wraps the never-throw
 * api wrapper: a 204 (no body) or a 200 with a body is success; anything else
 * throws an ApiCallError carrying `errorTag` (+ status/detail). A success
 * invalidates the keys list; any failure fires the per-hook error
 * `toastMessage`. `onSuccessExtra` runs BEFORE the invalidation for
 * useCreateKey/useDeleteKey (create stashes the plaintext, delete drops it
 * from the fresh-keys store); it receives both the parsed data (null on a 204)
 * and the mutation variables.
 */
function useKeyMutation<TArgs, TData>(
  request: (args: TArgs) => Promise<ApiResult<TData>>,
  errorTag: string,
  toastMessage: string,
  onSuccessExtra?: (data: TData | null, variables: TArgs) => void,
) {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const email = useSessionStore((s) => s.me?.email);

  return useMutation({
    mutationFn: async (args: TArgs): Promise<TData | null> => {
      const { status, data } = await request(args);
      if (status === 204) return null;
      if (status !== 200 || !data) throw apiCallError(errorTag, status, data);
      return data;
    },
    onSuccess: (data, variables) => {
      onSuccessExtra?.(data, variables);
      queryClient.invalidateQueries({ queryKey: KEYS_QUERY_KEY(email) });
    },
    onError: () => {
      toast({ message: toastMessage, variant: 'error' });
    },
  });
}

/**
 * POST /platform/keys. Backend returns HTTP 200 with CreateKeyResponse (the
 * plaintext ek- exactly once). On success the one-time plaintext is stashed in
 * the in-memory fresh-keys store (never persisted) keyed by `key_id`, and the
 * list is invalidated.
 */
export function useCreateKey() {
  const setFresh = useFreshKeysStore((s) => s.setFresh);

  return useKeyMutation<CreateKeyBody, CreateKeyResponse>(
    (body) => postJson<CreateKeyResponse>('/platform/keys', body),
    'create-key-failed',
    'Could not create the key.',
    (data) => {
      if (data?.key_id) setFresh(data.key_id, data.plaintext);
    },
  );
}

/**
 * DELETE /platform/keys/{id}. Backend returns HTTP 204 with no body (idempotent
 * revoke from any state). On success the fresh key is dropped from the
 * in-memory store and the list is invalidated.
 */
export function useDeleteKey() {
  const dropFresh = useFreshKeysStore((s) => s.dropFresh);

  return useKeyMutation<string, null>(
    (id) => del<null>(`/platform/keys/${encodeURIComponent(id)}`),
    'delete-key-failed',
    'Could not delete the key.',
    (_data, id) => {
      dropFresh(id);
    },
  );
}

/**
 * POST /platform/keys/{id}/suspend. Backend returns HTTP 204 with no body
 * (§8.2 — ACH-only state flip, the backing LiteLLM key is untouched). On
 * success the list is invalidated so the Status pill re-renders. The
 * suspend-propagation-window toast (spec §8.2) is the CALLER's concern (it
 * needs `me.suspend_propagation_seconds`, not owned by this hook).
 */
export function useSuspendKey() {
  return useKeyMutation<string, null>(
    (id) => postJson<null>(`/platform/keys/${encodeURIComponent(id)}/suspend`, {}),
    'suspend-key-failed',
    'Could not suspend the key.',
  );
}

/** POST /platform/keys/{id}/resume. Backend returns HTTP 204 with no body. */
export function useResumeKey() {
  return useKeyMutation<string, null>(
    (id) => postJson<null>(`/platform/keys/${encodeURIComponent(id)}/resume`, {}),
    'resume-key-failed',
    'Could not resume the key.',
  );
}
