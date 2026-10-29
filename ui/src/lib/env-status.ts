// env-status.ts — pure helpers deciding whether an Environment can back a new
// ek_ key. The collapsed `EnvironmentRow.status` is a UI summary; the real
// backend gate the operator resolves against is the `AccessGroupSynced`
// condition (see CLAUDE.md's Environment two-axis status). An Environment
// whose OTHER sub-condition (ExecutionResourcesResolved) is unresolved shows
// status !== 'Available' even though key creation works fine — that mismatch
// disabled every Environment in production (an outage). When `conditions` is
// absent (older platform-api), fall back to the old `status === 'Available'`
// rule so nothing regresses.

import type { EnvironmentRow } from '@/lib/api-types';

function findCondition(env: EnvironmentRow, type: string) {
  return env.conditions?.find((c) => c.type === type);
}

/** True when a key can be minted into this Environment. */
export function isKeyable(env: EnvironmentRow): boolean {
  if (!env.conditions?.length) return env.status === 'Available';
  return findCondition(env, 'AccessGroupSynced')?.status === 'True';
}

/**
 * When the environment is keyable but not fully `Available`, the reason the
 * `Available` condition gives (e.g. an unresolved content ref) — surfaced as
 * a non-blocking hint, never an error.
 */
export function degradedReason(env: EnvironmentRow): string | undefined {
  if (!isKeyable(env) || env.status === 'Available') return undefined;
  return findCondition(env, 'Available')?.reason;
}
