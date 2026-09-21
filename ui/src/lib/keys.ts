// keys.ts — pure row-selection + status-computation for the /keys data table.
//
// Ported verbatim (semantics) from src/ui/keys-table.js. These three functions
// are PURE — no DOM, no I/O — so the populated-vs-empty render gate and the
// status pills are unit-testable without a component renderer (no jsdom dep).

import type { KeyRow } from './api-types';

// A key is "Revoked" when an explicit revoked/blocked flag is truthy. The
// projected /keys shape carries no positive "active" field, so absence == active
// (UI-SPEC §C4 "Active … when not revoked").
export function isRevoked(key: KeyRow | null | undefined): boolean {
  return Boolean(key && (key.revoked || key.blocked));
}

// A key is "Disabled" when LiteLLM's reversible `blocked` flag is set (via
// /key/block). Distinct from a true `revoked` — a disabled key can be re-enabled.
// The keys table reads this for the "Disabled" status pill and the kebab's
// Disable/Enable toggle; `isRevoked` still treats blocked as revoked for the
// dashboard active-key count (a disabled key is not active).
export function isBlocked(key: KeyRow | null | undefined): boolean {
  return Boolean(key && key.blocked);
}

// Parse a LiteLLM `expires` value to an epoch-ms instant, or null when it
// carries no usable expiry. LiteLLM has shipped this field in several shapes
// across versions: an offset-bearing ISO string ("…+00:00" / "…Z"), a NAIVE ISO
// string with no timezone ("2026-06-04T08:30:00", possibly with microseconds), a
// space-separated timestamp, or (rarely) an epoch number. `Date.parse` alone
// silently returns NaN for some of these — which made an EXPIRED key fall through
// to "Active" (the bug this guards). We normalize first, and treat a naive
// timestamp as UTC (LiteLLM persists UTC) so the comparison is correct.
function parseExpiryMs(value: unknown): number | null {
  if (value == null) return null;
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return null;
    // Heuristic: < 1e12 is epoch SECONDS, otherwise already milliseconds.
    return value < 1e12 ? value * 1000 : value;
  }
  if (typeof value !== 'string') return null;
  let s = value.trim();
  if (!s) return null;
  // "YYYY-MM-DD HH:MM:SS" -> ISO "T" separator.
  s = s.replace(' ', 'T');
  // A time component with NO timezone designator is pinned to UTC.
  const hasTz = /(?:[zZ]|[+-]\d{2}:?\d{2})$/.test(s);
  if (s.includes('T') && !hasTz) s += 'Z';
  const ts = Date.parse(s);
  return Number.isFinite(ts) ? ts : null;
}

// D-08: a key is "Expired" when its expiry is in the past — computed from the
// projected `expires` field. This does NOT consider revoked; revoked-precedence
// (a revoked key reads `Revoked`, never `Expired`) is the CALLER's concern via
// `!isRevoked(k) && isExpired(k)`.
export function isExpired(key: KeyRow | null | undefined): boolean {
  if (!key) return false;
  const ts = parseExpiryMs(key.expires);
  return ts !== null && ts < Date.now();
}

// FID-04 (D-15) guard — the PURE row-selection gate the keys table uses to
// decide whether to render populated rows or the empty/loading/error state.
// Returns the SAME array reference when given an array (no copy, no mutation),
// and an empty array for anything else (null/undefined/object). Never throws.
export function selectKeyRows(value: unknown): KeyRow[] {
  return Array.isArray(value) ? (value as KeyRow[]) : [];
}
