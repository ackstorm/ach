// top-keys.test.ts — vitest for mergeTopKeys (pure, no renderer).

import { describe, expect, it } from 'vitest';

import type { KeyRow, StatsKeyRow } from './api-types';
import { mergeTopKeys } from './top-keys';

function statsKey(over: Partial<StatsKeyRow> = {}): StatsKeyRow {
  return { id: 'k1', key_alias: 'one', requests: 0, spend: 0, spend_pct: 0, ...over };
}

function userKey(over: Partial<KeyRow> = {}): KeyRow {
  return {
    key_id: 'k1',
    type: 'ek',
    owner_email: 'alice@example.com',
    environment: 'prod',
    name: 'one',
    status: 'active',
    created_at: '2026-03-01T10:00:00Z',
    expires_at: null,
    ...over,
  };
}

describe('mergeTopKeys', () => {
  it('pads idle user keys (no activity row) with zeros', () => {
    const active = [statsKey({ id: 'a', key_alias: 'active', requests: 10, spend: 5 })];
    const all = [
      userKey({ key_id: 'a', name: 'active' }),
      userKey({ key_id: 'b', name: 'idle-1' }),
      userKey({ key_id: 'c', name: 'idle-2' }),
    ];
    const merged = mergeTopKeys(active, all);

    expect(merged).toHaveLength(3);
    // Active first (spend desc), idle keys padded with zero usage.
    expect(merged[0]).toMatchObject({ key_alias: 'active', requests: 10, spend: 5 });
    expect(merged.map((k) => k.key_alias)).toContain('idle-1');
    expect(merged.map((k) => k.key_alias)).toContain('idle-2');
    expect(merged.filter((k) => k.spend === 0)).toHaveLength(2);
  });

  it('does NOT duplicate a key present in both (matched by key_id or name)', () => {
    const active = [statsKey({ id: 'a', key_alias: 'active', requests: 3, spend: 2 })];
    // Same name, different key_id — must still dedup.
    const all = [userKey({ key_id: 'other-id', name: 'active' })];
    const merged = mergeTopKeys(active, all);
    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({ key_alias: 'active', requests: 3 });
  });

  it('ranks the union by spend desc and does not mutate the inputs', () => {
    const active = [
      statsKey({ id: 'a', key_alias: 'lo', spend: 1 }),
      statsKey({ id: 'b', key_alias: 'hi', spend: 9 }),
    ];
    const all = [userKey({ key_id: 'c', name: 'idle' })];
    const merged = mergeTopKeys(active, all);
    expect(merged.map((k) => k.key_alias)).toEqual(['hi', 'lo', 'idle']);
    // Inputs untouched.
    expect(active.map((k) => k.key_alias)).toEqual(['lo', 'hi']);
  });

  it('returns the activity rows unchanged when there are no user keys', () => {
    const active = [statsKey({ id: 'a', key_alias: 'one', spend: 4 })];
    expect(mergeTopKeys(active, [])).toEqual(active);
  });
});
