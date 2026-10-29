// fresh-keys.ts — in-memory id -> full `sk-` map of keys minted THIS browser
// session. Ported from src/ui/dashboard.js's freshKeys state.
//
// SECURITY INVARIANT (threat T-10-15): the full `sk-` virtual key lives ONLY in
// this in-memory store. It is NEVER written to any web Storage (localStorage /
// sessionStorage / IndexedDB / cookies) and this store therefore MUST NOT use
// Zustand's `persist` middleware. The freshly-minted key is shown to the user
// exactly once (on create) and is intentionally LOST on refresh (decision
// D-01): a refresh reloads the key list from the backend, which deliberately
// strips the `sk-` (D-17), so there is no way to recover a full key after
// reload — by design.
//
// Idiom matches stores/session.ts / config.ts: a plain `create<T>((set) => ...)`
// with an exported `initialFreshKeysState` for test resets. Selector-friendly:
// callers read `useFreshKeysStore((s) => s.freshKeys)` or a single action.

import { create } from 'zustand';

export interface FreshKeysState {
  /** key id -> full `sk-` virtual key (in-memory only; never persisted). */
  freshKeys: Record<string, string>;
  setFresh: (id: string, key: string) => void;
  dropFresh: (id: string) => void;
}

/** Initial (empty) state. Exported so tests can reset the store to it. */
export const initialFreshKeysState = {
  freshKeys: {} as Record<string, string>,
} as const;

export const useFreshKeysStore = create<FreshKeysState>((set) => ({
  ...initialFreshKeysState,

  // Merge the new entry over the existing map (object spread) so prior
  // session-minted keys survive.
  setFresh: (id, key) =>
    set((prev) => ({ freshKeys: { ...prev.freshKeys, [id]: key } })),

  // Remove `id` from the map. If `id` is absent, return the SAME state object
  // (stable identity) so selectors do not see a spurious change — mirrors
  // dashboard.js's `if (!(id in prev)) return prev`.
  dropFresh: (id) =>
    set((prev) => {
      if (!(id in prev.freshKeys)) return prev;
      const next = { ...prev.freshKeys };
      delete next[id];
      return { freshKeys: next };
    }),
}));
