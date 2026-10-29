// keys.ts — pure row-selection for the /keys data table.
//
// PURE — no DOM, no I/O — so the populated-vs-empty render gate is
// unit-testable without a component renderer (no jsdom dep). Status
// derivation (revoked/expired/etc.) is no longer a client concern — ACH's
// `KeyRow.status` is the server-computed effective state (D-30).

import type { KeyRow } from './api-types';

// FID-04 (D-15) guard — the PURE row-selection gate the keys table uses to
// decide whether to render populated rows or the empty/loading/error state.
// Returns the SAME array reference when given an array (no copy, no mutation),
// and an empty array for anything else (null/undefined/object). Never throws.
export function selectKeyRows(value: unknown): KeyRow[] {
  return Array.isArray(value) ? (value as KeyRow[]) : [];
}
