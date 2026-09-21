// session.ts — Zustand session store + boot, ported from src/ui/app.js.
//
// Holds the three pieces of state the App component feeds through the pure
// resolveState(status, hasLoaded) state machine:
//
//   status     — HTTP status of the last GET /api/session/me, or null while a
//                (re)load is in flight (drives the loading card; no white flash).
//   me         — the SessionMe payload, set ONLY on a 200 response.
//   hasLoaded  — sticky/monotonic flag: flipped true after the first 200 and
//                never reset. It distinguishes a cold-load 401 (-> signin) from
//                a mid-session 401 (-> expired) — the cold-load-vs-mid-session
//                distinction owned by api.js's module-level _hasLoaded flag in
//                the old Preact app, now lifted into store state.
//
// loadSession() reproduces app.js's loadSession callback verbatim:
//   1. set status = null FIRST (show loading immediately on every (re)load);
//   2. await getJson<SessionMe>('/api/session/me') (never-throws; status 0 on
//      network failure);
//   3. on 200: store `me` AND flip hasLoaded true (a LATER 401 now resolves to
//      expired, not signin);
//   4. ALWAYS set status to the response status.
//
// Faithful to app.js, `me` is touched ONLY on a 200 — a non-200 response does
// NOT clear it, so a mid-session 401 leaves the previously-loaded `me` intact.

import { create } from 'zustand';
import { getJson } from '../lib/api';
import type { SessionMe } from '../lib/api-types';

export interface SessionState {
  status: number | null;
  me: SessionMe | null;
  hasLoaded: boolean;
  loadSession: () => Promise<void>;
  markExpired: () => void;
}

/** Initial (cold-load) state. Exported so tests can reset the store to it. */
export const initialSessionState = {
  status: null,
  me: null,
  hasLoaded: false,
} as const;

export const useSessionStore = create<SessionState>((set) => ({
  ...initialSessionState,

  loadSession: async () => {
    // Show the loading card immediately on every (re)load — no white flash
    // (UI-SPEC §Loading Sequence step 1).
    set({ status: null });

    const { status, data } = await getJson<SessionMe>('/api/session/me');

    if (status === 200) {
      // `me` is set ONLY on 200; hasLoaded flips true so a later 401 routes to
      // "expired". hasLoaded is monotonic — never set back to false here.
      set({ me: data, hasLoaded: true });
    }

    // Always record the response status (200 authed / 401 signin|expired /
    // 0 network failure -> error). A non-200 does NOT clear `me` (mirrors
    // app.js, which only ever calls setMe on 200).
    set({ status });
  },

  // markExpired — flip status to 401 so resolveState(401, hasLoaded) === 'expired'
  // and App's redirect effect fires. GUARDED on hasLoaded: a cold-load 401 (before
  // the first authenticated /me) must stay 'signin', so this no-ops until the user
  // has loaded at least once. Sets state only (no fetch) — it cannot loop.
  markExpired: () => set((s) => (s.hasLoaded ? { status: 401 } : s)),
}));
