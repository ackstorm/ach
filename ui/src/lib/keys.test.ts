// keys.test.ts — FID-04 regression suite for selectKeyRows.
//
// Runs in the default vitest node environment (no jsdom, no DOM) — it exercises
// the PURE row-selection logic that gates whether a `/platform/keys` payload
// reaches the populated table, WITHOUT rendering any component.
import { describe, it, expect } from 'vitest';
import { selectKeyRows } from './keys';
import type { KeyRow } from './api-types';

const PAYLOAD: KeyRow[] = [
  {
    key_id: 'ekid_1',
    type: 'ek',
    owner_email: 'alice@example.com',
    environment: 'prod',
    name: 'ci-runner',
    status: 'active',
    created_at: '2026-03-01T10:00:00Z',
    expires_at: null,
  },
  {
    key_id: 'ekid_2',
    type: 'ek',
    owner_email: 'alice@example.com',
    environment: 'staging',
    name: 'other-key',
    status: 'expired',
    created_at: '2026-02-01T08:00:00Z',
    expires_at: '2099-01-01T00:00:00Z',
  },
];

describe('FID-04 — selectKeyRows (the populated-vs-empty render gate)', () => {
  it('returns the rows verbatim for a representative populated /platform/keys payload', () => {
    const rows = selectKeyRows(PAYLOAD);
    expect(rows).toHaveLength(2);
    expect(rows[0].key_id).toBe('ekid_1');
    // Same array reference passed through — no shape mutation, no row dropping.
    expect(rows).toBe(PAYLOAD);
  });

  it('returns an empty array for the genuinely-empty case (expected-empty, path b)', () => {
    expect(selectKeyRows([])).toEqual([]);
  });

  it('coerces a non-array payload to an empty array (never throws)', () => {
    expect(selectKeyRows(null)).toEqual([]);
    expect(selectKeyRows(undefined)).toEqual([]);
    expect(selectKeyRows({ items: PAYLOAD })).toEqual([]); // a stray object, not the array
  });
});
