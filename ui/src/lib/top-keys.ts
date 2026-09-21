// top-keys.ts — merge the user's full key list into the stats "top keys" slice.
//
// The /api/session/stats `keys[]` is built from spend logs, so a key with NO
// activity in the window is absent. The TOP API KEYS panel should still list the
// user's keys (idle ones included), so we union the activity rows with the user's
// key list — padding any key missing from the activity rows with zeros — then
// rank by spend desc. Dedup is by id OR alias (a key present in both sources is
// counted once). Pure + deterministic (no I/O), so it is unit-testable.

import type { KeyRow, StatsKeyRow } from './api-types';

export function mergeTopKeys(
  statsKeys: StatsKeyRow[],
  userKeys: KeyRow[]
): StatsKeyRow[] {
  // Every id/alias already represented by an activity row.
  const seen = new Set<string>();
  for (const k of statsKeys) {
    if (k.id) seen.add(k.id);
    if (k.key_alias) seen.add(k.key_alias);
  }

  // Append the user's keys that have no activity row yet, with zeroed usage.
  const padded: StatsKeyRow[] = userKeys
    .filter(
      (k) => !(k.id && seen.has(k.id)) && !(k.key_alias && seen.has(k.key_alias))
    )
    .map((k) => ({
      id: k.id,
      key_alias: k.key_alias,
      requests: 0,
      spend: 0,
      spend_pct: 0,
    }));

  // Spend desc (the activity rows arrive pre-sorted; the zero-spend padded rows
  // settle after them). A copy — never mutate the inputs.
  return [...statsKeys, ...padded].sort((a, b) => b.spend - a.spend);
}
