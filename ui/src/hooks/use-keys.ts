// use-keys.ts — TanStack Query hooks for the session keys lifecycle.
//
// Wraps the never-throw api wrapper (lib/api) so the query/mutation surface the
// TanStack-idiomatic isError/onSuccess states:
//
//   useKeys()       GET    /api/session/keys   -> KeyRow[] (throws on non-200 so
//                                                  the table renders its error
//                                                  branch; a 200 + empty array
//                                                  resolves to [] = empty state)
//   useCreateKey()  POST   /api/session/keys   -> CreateKeyResponse (stashes the
//                                                  one-time sk- in the in-memory
//                                                  fresh-keys store, then
//                                                  invalidates the list)
//   useDeleteKey()  DELETE /api/session/keys/{id} -> DeleteKeyResponse (drops the
//                                                  fresh key, invalidates list)
//
// Cancellation: useKeys threads TanStack Query's `{ signal }` into getJson so an
// unmount / superseding refetch aborts the in-flight fetch; apiFetch re-throws
// that AbortError (Phase-1 carry-forward), letting Query treat it as a
// cancellation instead of a fake network error. Mutations are NOT cancelled.
//
// Invalidation uses the provider's client via useQueryClient() (idiomatic), NOT
// the singleton import — so tests can inject their own client. Fresh-key actions
// are read via the store selector.

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { del, getJson, postJson, type ApiResult } from '@/lib/api';
import type {
  BlockKeyResponse,
  ChangeKeyTeamResponse,
  CreateKeyBody,
  CreateKeyResponse,
  DeleteKeyResponse,
  KeyRow,
  KeysResponse,
  MakeDefaultResponse,
} from '@/lib/api-types';
import { useFreshKeysStore } from '@/stores/fresh-keys';
import { useToast } from '@/hooks/use-toast';

/** Query key for the session keys list. Shared by the query + invalidations. */
export const KEYS_QUERY_KEY = ['session', 'keys'] as const;

/**
 * An Error carrying the backend HTTP `status` + parsed `detail`. The create-key
 * modal needs both to route a 422 field-rejection (`{ detail }`) to the matching
 * field; a 502 has no body so `detail` is null and the modal falls back to its
 * generic copy.
 */
export interface ApiCallError extends Error {
  status: number;
  detail: string | null;
}

/**
 * Build an ApiCallError from a parsed (never-throw api wrapper) response. Reads
 * `data.detail` defensively — only a string `detail` is carried, anything else
 * (or a null body, e.g. a 502) yields `detail: null`. The localized `data as ...`
 * narrowing is confined here so the response types stay un-weakened at the
 * call sites.
 */
function apiCallError(message: string, status: number, data: unknown): ApiCallError {
  const detail =
    data &&
    typeof data === 'object' &&
    'detail' in data &&
    typeof (data as { detail: unknown }).detail === 'string'
      ? (data as { detail: string }).detail
      : null;
  return Object.assign(new Error(message), { status, detail });
}

/**
 * GET /api/session/keys. Throws on a non-200 / malformed response so the query
 * lands in `isError`; a 200 with a `keys` array resolves to `KeyRow[]` (an empty
 * array is the empty state, not an error). The `{ signal }` makes cancellation
 * work end-to-end with apiFetch's AbortError re-throw.
 */
export function useKeys() {
  return useQuery({
    queryKey: KEYS_QUERY_KEY,
    queryFn: async ({ signal }): Promise<KeyRow[]> => {
      const { status, data } = await getJson<KeysResponse>('/api/session/keys', {
        signal,
      });
      if (status === 200 && data && Array.isArray(data.keys)) return data.keys;
      throw new Error('keys-load-failed');
    },
  });
}

/**
 * Derived: does the user have an explicit default key? Reads the keys query (no
 * extra fetch) and returns true iff some key carries is_default. Safe before the
 * query settles / on error — returns false (never throws). Shared by the AppShell
 * CHAT/Models/MCPs nav gating and the Models/MCP route gates (per-user catalog
 * reads are scoped to the default key via the gateway's custom auth).
 */
export function useHasDefaultKey(): boolean {
  const { data } = useKeys();
  return (data ?? []).some((k) => k.is_default);
}

/**
 * Shared factory for the key-lifecycle mutations. Each wraps the never-throw api
 * wrapper: a non-200 / null-body response throws an ApiCallError carrying
 * `errorTag` (+ status/detail), a success invalidates the keys list, and any
 * failure fires the per-hook error `toastMessage`. `onSuccessExtra` runs BEFORE
 * the invalidation for the two hooks that also touch the fresh-keys store (create
 * stashes the sk-, delete drops it); it receives both the parsed data and the
 * mutation variables (delete keys off the variables, create off the data).
 */
function useKeyMutation<TArgs, TData>(
  request: (args: TArgs) => Promise<ApiResult<TData>>,
  errorTag: string,
  toastMessage: string,
  onSuccessExtra?: (data: TData, variables: TArgs) => void,
) {
  const queryClient = useQueryClient();
  const { toast } = useToast();

  return useMutation({
    mutationFn: async (args: TArgs): Promise<TData> => {
      const { status, data } = await request(args);
      if (status !== 200 || !data) throw apiCallError(errorTag, status, data);
      return data;
    },
    onSuccess: (data, variables) => {
      onSuccessExtra?.(data, variables);
      queryClient.invalidateQueries({ queryKey: KEYS_QUERY_KEY });
    },
    onError: () => {
      toast({ message: toastMessage, variant: 'error' });
    },
  });
}

/**
 * POST /api/session/keys. Backend returns HTTP 200 with { key, id, team_id }
 * (session.py::session_create_key). On success the one-time full `sk-` is stashed
 * in the in-memory fresh-keys store (never persisted; T-10-15) and the list is
 * invalidated. Guard at the hook via the `status !== 200` check — the response
 * type's `key`/`id` stay non-null (plan carry-forward).
 *
 * A 422 parses to a truthy `{ detail }` body but the `status !== 200` branch
 * still throws and carries the detail; a 502 has a null `data` so detail is null.
 * The create-key modal reads `.status` + `.detail` to route a field-rejection.
 */
export function useCreateKey() {
  const setFresh = useFreshKeysStore((s) => s.setFresh);

  return useKeyMutation<CreateKeyBody, CreateKeyResponse>(
    (body) => postJson<CreateKeyResponse>('/api/session/keys', body),
    'create-key-failed',
    'Could not create the key.',
    (data) => {
      if (data.id) setFresh(data.id, data.key);
    },
  );
}

/**
 * DELETE /api/session/keys/{id}. Backend returns HTTP 200 { status: "deleted",
 * id } (session.py::session_delete_key). On success the fresh key is dropped from
 * the in-memory store and the list is invalidated.
 */
export function useDeleteKey() {
  const dropFresh = useFreshKeysStore((s) => s.dropFresh);

  return useKeyMutation<string, DeleteKeyResponse>(
    (id) => del<DeleteKeyResponse>(`/api/session/keys/${encodeURIComponent(id)}`),
    'delete-key-failed',
    'Could not delete the key.',
    (_data, id) => {
      dropFresh(id);
    },
  );
}

/**
 * POST /api/session/keys/{id}/default. Backend returns HTTP 200 { status:
 * "default", id } (session.py::session_make_default), promoting the key to the
 * user's explicit default and clearing any prior default. On success the list is
 * invalidated so the DEFAULT badge + revoke-guard re-render.
 */
export function useMakeDefault() {
  return useKeyMutation<string, MakeDefaultResponse>(
    (id) => postJson<MakeDefaultResponse>(`/api/session/keys/${encodeURIComponent(id)}/default`, {}),
    'make-default-failed',
    'Could not set the default key. Please try again.',
  );
}

/**
 * POST /api/session/keys/{id}/team. Moves an owned key to another team the user
 * belongs to (session.py::session_change_key_team). On success the list is
 * invalidated so the row's team + any team-derived UI re-render.
 */
export function useChangeKeyTeam() {
  return useKeyMutation<{ id: string; teamId: string }, ChangeKeyTeamResponse>(
    ({ id, teamId }) =>
      postJson<ChangeKeyTeamResponse>(`/api/session/keys/${encodeURIComponent(id)}/team`, {
        team_id: teamId,
      }),
    'change-team-failed',
    'Could not change the key team.',
  );
}

/**
 * POST /api/session/keys/{id}/block. Disables (blocked:true) or re-enables
 * (blocked:false) a key via LiteLLM /key/block | /key/unblock — reversible, not a
 * delete (session.py::session_block_key). On success the list is invalidated so
 * the Status pill + kebab label re-render.
 */
export function useToggleKeyBlock() {
  return useKeyMutation<{ id: string; blocked: boolean }, BlockKeyResponse>(
    ({ id, blocked }) =>
      postJson<BlockKeyResponse>(`/api/session/keys/${encodeURIComponent(id)}/block`, { blocked }),
    'block-key-failed',
    'Could not update the key.',
  );
}
