// resolve-state.ts — pure shell state machine for the alitellm-auth console.
//
// `resolveState(status, hasLoaded)` maps an HTTP status (or `null` while the
// first /platform/console/bootstrap call is in flight, or `0` for a network failure) plus
// the `hasLoaded` flag to exactly one of the five shell states:
//
//   loading | signin | authed | error | expired
//
// This function is PURE — it touches no browser globals and no I/O, so the
// shell's branching logic is unit-testable independently of a live backend.
//
// Mapping (ported verbatim from src/ui/state.js):
//   null             -> loading  (in flight, before the first response resolves)
//   200              -> authed   (render the authenticated shell; caller sets hasLoaded)
//   401 & !hasLoaded -> signin   (cold load: render the sign-in landing, NO redirect)
//   401 &  hasLoaded -> expired  (mid-session: silent redirect to the console login)
//   anything else    -> error    (4xx/5xx and 0 = network failure -> error card)

export type ShellState = 'loading' | 'signin' | 'authed' | 'error' | 'expired';

export function resolveState(
  status: number | null,
  hasLoaded: boolean,
): ShellState {
  if (status === null || status === undefined) {
    return 'loading';
  }
  if (status === 200) {
    return 'authed';
  }
  if (status === 401) {
    return hasLoaded ? 'expired' : 'signin';
  }
  // 0 (network failure), 4xx, 5xx, and any other status -> error card.
  return 'error';
}
