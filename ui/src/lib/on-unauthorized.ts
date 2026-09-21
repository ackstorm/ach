// on-unauthorized.ts — a tiny leaf module that decouples the pure fetch wrapper
// (lib/api) from the session store.
//
// api.ts must stay a leaf: it is imported BY the session store, so it cannot
// import the store back without an api <-> session cycle. Instead api.ts calls
// notifyUnauthorized() when a same-origin /api/session/* request returns 401,
// and the App shell registers a handler (setUnauthorizedHandler) that marks the
// session store expired. resolveState(401, hasLoaded=true) then resolves to
// 'expired' and App's existing effect redirects to /api/oauth/login.
//
// One module-level handler is enough — the app registers exactly one.

type UnauthorizedHandler = () => void;

let handler: UnauthorizedHandler | null = null;

/** Register the callback fired on a mid-session 401. Replaces any previous;
 *  pass null to clear (App does this on unmount). */
export function setUnauthorizedHandler(fn: UnauthorizedHandler | null): void {
  handler = fn;
}

/** Invoke the registered handler, if any. No-op when none is registered. */
export function notifyUnauthorized(): void {
  handler?.();
}
