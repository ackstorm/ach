// keys.test.ts — FID-04 regression suite. Ported verbatim (behavior) from
// src/ui/keys-table.test.js.
//
// Runs in the default vitest node environment (no jsdom, no DOM) — it exercises
// the PURE row-selection + status-computation logic that gates whether a
// `/keys` payload reaches the populated table, WITHOUT rendering any component.
//
// WHY this suite exists (FID-04 triage, D-15): the reported "keys already
// generated not displaying" item. The data path is CORRECT end-to-end — a
// well-formed `{keys:[...]}` payload yields its rows; only a genuinely empty
// list yields the empty state. These tests LOCK that the data path stays
// correct so a future regression (e.g. an Array.isArray guard rejecting a valid
// payload) is caught.
import { describe, it, expect } from 'vitest';
import { selectKeyRows, isRevoked, isExpired } from './keys';
import type { KeyRow } from './api-types';

// A representative `/keys` payload in the backend's _project_session_key shape
// (src/api/app/litellm_client.py): id, key_alias, spend, budget, tpm_limit,
// rpm_limit, models, created_at, expires. No revoked/blocked flag
// (absence == active).
const PAYLOAD: KeyRow[] = [
  {
    id: 'key-abc123',
    key_alias: 'ci-runner',
    spend: 12.5,
    budget: null,
    tpm_limit: 1000000,
    rpm_limit: 100,
    models: ['all-team-models'],
    team_id: 'team-1',
    created_at: '2026-03-01T10:00:00+00:00',
    expires: null,
    last_used: null,
    is_default: false,
  },
  {
    id: 'key-def456',
    key_alias: null,
    spend: 0,
    budget: null,
    tpm_limit: null,
    rpm_limit: null,
    models: null,
    team_id: null,
    created_at: '2026-02-01T08:00:00+00:00',
    expires: '2099-01-01T00:00:00+00:00',
    last_used: null,
    is_default: false,
  },
];

describe('FID-04 — selectKeyRows (the populated-vs-empty render gate)', () => {
  it('returns the rows verbatim for a representative populated /keys payload', () => {
    const rows = selectKeyRows(PAYLOAD);
    expect(rows).toHaveLength(2);
    expect(rows[0].id).toBe('key-abc123');
    // Same array reference passed through — no shape mutation, no row dropping.
    expect(rows).toBe(PAYLOAD);
  });

  it('returns an empty array for the genuinely-empty case (expected-empty, path b)', () => {
    expect(selectKeyRows([])).toEqual([]);
  });

  it('coerces a non-array payload to an empty array (never throws)', () => {
    expect(selectKeyRows(null)).toEqual([]);
    expect(selectKeyRows(undefined)).toEqual([]);
    expect(selectKeyRows({ keys: PAYLOAD })).toEqual([]); // a stray object, not the keys array
  });
});

describe('FID-04 / D-08 — status computation against representative rows', () => {
  it('treats a row with no revoked/blocked flag as active (not revoked, not expired)', () => {
    expect(isRevoked(PAYLOAD[0])).toBe(false);
    expect(isExpired(PAYLOAD[0])).toBe(false); // expires null -> never expired
  });

  it('computes Expired only when expires is in the past', () => {
    // The source JS test passes partial row literals; cast to KeyRow to port
    // the assertion byte-faithfully without widening the production signature.
    const past = { id: 'k1', expires: '2000-01-01T00:00:00+00:00' } as KeyRow;
    const future = { id: 'k2', expires: '2099-01-01T00:00:00+00:00' } as KeyRow;
    expect(isExpired(past)).toBe(true);
    expect(isExpired(future)).toBe(false);
  });

  it('detects a past expiry across LiteLLM date shapes (regression: expired must not read Active)', () => {
    // A NAIVE ISO string (no timezone) — Date.parse treats this as local time,
    // but we pin it to UTC; either way a year-2000 instant is firmly in the past.
    expect(isExpired({ id: 'a', expires: '2000-01-01T00:00:00' } as KeyRow)).toBe(true);
    // Microsecond precision, naive.
    expect(
      isExpired({ id: 'b', expires: '2000-01-01T00:00:00.123456' } as KeyRow),
    ).toBe(true);
    // Space-separated timestamp (no "T").
    expect(isExpired({ id: 'c', expires: '2000-01-01 00:00:00' } as KeyRow)).toBe(true);
    // Epoch SECONDS (2000-01-01) — coerced to ms before comparing.
    expect(
      isExpired({ id: 'd', expires: 946684800 as unknown as string } as KeyRow),
    ).toBe(true);
    // A FUTURE naive timestamp is NOT expired.
    expect(isExpired({ id: 'e', expires: '2099-01-01T00:00:00' } as KeyRow)).toBe(false);
  });

  it('an unparseable / empty expiry is treated as no-expiry (never throws, not expired)', () => {
    expect(isExpired({ id: 'f', expires: 'not-a-date' } as KeyRow)).toBe(false);
    expect(isExpired({ id: 'g', expires: '' } as KeyRow)).toBe(false);
  });

  it('Revoked takes precedence — a revoked row is never reported as expired here', () => {
    const revokedAndPast = {
      id: 'k3',
      revoked: true,
      expires: '2000-01-01T00:00:00+00:00',
    } as KeyRow;
    expect(isRevoked(revokedAndPast)).toBe(true);
    // isExpired still returns true on the raw date; the table gates Expired on
    // !revoked so the rendered pill is Revoked. This asserts the precedence
    // inputs the row uses, matching `!revoked && isExpired(k)`.
    expect(isExpired(revokedAndPast)).toBe(true);
  });

  it('honors the blocked flag as revoked (backend block)', () => {
    expect(isRevoked({ id: 'k4', blocked: true } as KeyRow)).toBe(true);
  });
});
