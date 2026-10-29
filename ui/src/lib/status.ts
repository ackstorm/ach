// status.ts — the status label + tone per domain, one map each, so every page
// that shows the same state says the same word in the same colour.
import type { KeyRow, TraceSummary } from '@/lib/api-types';
import type { TargetStatus } from '@/lib/observability-targets';

export type Tone = 'ok' | 'warn' | 'danger' | 'info' | 'neutral' | 'muted';
export type Status = { label: string; tone: Tone };

export const KEY_STATUS: Record<KeyRow['status'], Status> = {
  active: { label: 'Active', tone: 'ok' },
  suspended: { label: 'Suspended', tone: 'warn' },
  expired: { label: 'Expired', tone: 'muted' },
  invalid: { label: 'Invalid', tone: 'muted' },
  revoked: { label: 'Revoked', tone: 'danger' },
};

export const ENV_STATUS = {
  available: { label: 'Available', tone: 'ok' },
  degraded: { label: 'Degraded', tone: 'warn' },
  notReady: { label: 'Not ready', tone: 'danger' },
  outsideTeams: { label: 'Not in your teams', tone: 'neutral' },
} as const satisfies Record<string, Status>;

/** MCP server health; anything unrecognised reads as its own word, neutral. */
export function mcpStatus(status: string | null): Status {
  const s = (status || 'unknown').toLowerCase();
  return { label: s, tone: s === 'healthy' ? 'ok' : s === 'unhealthy' ? 'danger' : 'neutral' };
}

/** Trace outcome — Overview and Traces both use this. */
export const TRACE_OUTCOME: Record<TraceSummary['status'], Status> = {
  success: { label: 'Success', tone: 'ok' },
  error: { label: 'Failed', tone: 'danger' },
  unknown: { label: 'Unknown', tone: 'neutral' },
};
export const traceOutcome = (status: string | null | undefined): Status =>
  TRACE_OUTCOME[status as TraceSummary['status']] ?? TRACE_OUTCOME.unknown;

/** Overview target: `over` is the failing side (Above / Below target). */
export function targetStatus(status: TargetStatus, ok: string, over: string): Status {
  if (status === null) return { label: 'Unavailable', tone: 'neutral' };
  return status === 'over' ? { label: over, tone: 'danger' } : { label: ok, tone: 'ok' };
}

export const COMING_SOON: Status = { label: 'Coming soon', tone: 'info' };
