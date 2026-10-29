// api.ts — typed, never-throw same-origin fetch wrapper.
//
// Ported from src/ui/api.js. `apiFetch<T>` performs a single fetch and resolves
// to `{ status, data }`:
//   - a thrown network error (offline, DNS failure, reset) maps to
//     `{ status: 0, data: null }` so callers render an error state instead of
//     producing an uncaught rejection (threat T-09-05);
//   - a body that is not valid JSON (e.g. a 401 with an empty body) maps `data`
//     to `null` while preserving the real HTTP `status`.
//
// The ONE exception to the never-throw rule: an `AbortError` is RE-THROWN. When
// TanStack Query cancels an in-flight query (unmount / superseding refetch) it
// aborts the underlying fetch; that abort must surface as a cancellation, not as
// a fake `{ status: 0 }` network error that would flip the UI to an error state.
//
// getJson/postJson/del all REUSE apiFetch (they never bypass the single path).
// Writes ALWAYS carry `content-type: application/json` because the backend
// write-guard expects JSON (D-28: same-origin fetch, Sec-Fetch-Site/Origin checked).

import { notifyUnauthorized } from './on-unauthorized';

export interface ApiResult<T> {
  status: number;
  data: T | null;
}

export async function apiFetch<T>(
  url: string,
  options?: RequestInit,
): Promise<ApiResult<T>> {
  try {
    const resp = await fetch(url, options);
    let data: T | null = null;
    try {
      // Body may be empty (e.g. a 401 with no JSON); tolerate that.
      data = (await resp.json()) as T;
    } catch {
      data = null;
    }
    // A mid-session 401 on a platform endpoint means the cookie expired. Notify
    // the shell (via the decoupled handler) so it can mark the session expired
    // and redirect to login. Guarded to /platform/* so a 401 on any other
    // endpoint never triggers a redirect. The cold-load-vs-mid-session
    // distinction is enforced downstream: markExpired no-ops until hasLoaded.
    if (resp.status === 401 && url.startsWith('/platform/')) {
      notifyUnauthorized();
    }
    return { status: resp.status, data };
  } catch (err) {
    // A TanStack-Query cancellation (unmount / superseding refetch) aborts the
    // fetch — re-throw it so Query treats it as a cancellation, not a network
    // error that would flip the UI to an error state. Real failures still map
    // to the never-throw { status: 0, data: null } envelope (threat T-09-05).
    //
    // Detect robustly BY NAME: an abort surfaces as a `DOMException` (browsers
    // and Node) whose `name` is "AbortError" — and a DOMException is NOT an
    // `Error` subclass in every realm, so an `instanceof Error` guard would miss
    // it. We therefore key off the `name` property alone.
    if (
      typeof err === 'object' &&
      err !== null &&
      (err as { name?: unknown }).name === 'AbortError'
    ) {
      throw err;
    }
    return { status: 0, data: null };
  }
}

export const getJson = <T>(url: string, options?: RequestInit): Promise<ApiResult<T>> =>
  apiFetch<T>(url, options);

export const postJson = <T>(url: string, body: unknown): Promise<ApiResult<T>> =>
  apiFetch<T>(url, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  });

export const del = <T>(url: string): Promise<ApiResult<T>> =>
  apiFetch<T>(url, {
    method: 'DELETE',
    headers: { 'content-type': 'application/json' },
  });
