// observability-targets.ts — the dashboard's chosen targets (user policy, not
// universal benchmarks). Each status is independent: a high cache share never
// cancels a high input/output ratio. Missing or undefined measurements get no
// status at all, never a pass or fail.

export type TargetStatus = 'ok' | 'over' | null;

/** Input/output ratio: acceptable at or below 10:1, red above. */
export function ratioStatus(ratio: number | null | undefined): TargetStatus {
  if (ratio == null || !Number.isFinite(ratio)) return null;
  return ratio > 10 ? 'over' : 'ok';
}

/** Cache-read share (0..1): acceptable at or above 80%, red below. */
export function cacheStatus(share: number | null | undefined): TargetStatus {
  if (share == null || !Number.isFinite(share)) return null;
  return share < 0.8 ? 'over' : 'ok';
}

/** Group a User-Agent runtime label by product (versions already stripped server-side). */
export function runtimeLabel(name: string): string {
  return ({ claude_code: 'Claude Code', opencode: 'OpenCode', codex: 'Codex', python_client: 'Python client', node_client: 'Node client', other: 'Other', unknown: 'Unknown' } as Record<string, string>)[name] ?? name;
}
