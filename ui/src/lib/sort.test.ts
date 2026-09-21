import { describe, expect, it } from 'vitest';

import { compareValues, sortRows } from './sort';

describe('compareValues', () => {
  it('orders numbers ascending and descending', () => {
    expect(compareValues(1, 2, 'asc')).toBeLessThan(0);
    expect(compareValues(1, 2, 'desc')).toBeGreaterThan(0);
    expect(compareValues(2, 2, 'asc')).toBe(0);
  });

  it('sorts null/undefined LAST regardless of direction', () => {
    expect(compareValues(null, 5, 'asc')).toBeGreaterThan(0);
    expect(compareValues(null, 5, 'desc')).toBeGreaterThan(0);
    expect(compareValues(5, undefined, 'asc')).toBeLessThan(0);
    expect(compareValues(undefined, null, 'asc')).toBe(0);
  });

  it('treats non-finite numbers as missing (sort last)', () => {
    expect(compareValues(NaN, 1, 'desc')).toBeGreaterThan(0);
    expect(compareValues(Infinity, 1, 'asc')).toBeGreaterThan(0);
  });

  it('compares strings with numeric awareness', () => {
    expect(compareValues('key-2', 'key-10', 'asc')).toBeLessThan(0);
  });
});

describe('sortRows', () => {
  const rows = [
    { id: 'a', spend: 3 },
    { id: 'b', spend: 1 },
    { id: 'c', spend: 1 },
    { id: 'd', spend: null as number | null },
  ];

  it('does not mutate the input', () => {
    const copy = rows.slice();
    sortRows(rows, (r) => r.spend, 'desc');
    expect(rows).toEqual(copy);
  });

  it('sorts descending with null last and is stable on ties', () => {
    const out = sortRows(rows, (r) => r.spend, 'desc').map((r) => r.id);
    expect(out).toEqual(['a', 'b', 'c', 'd']); // b before c (stable tie), null d last
  });

  it('sorts ascending with null still last', () => {
    const out = sortRows(rows, (r) => r.spend, 'asc').map((r) => r.id);
    expect(out).toEqual(['b', 'c', 'a', 'd']);
  });
});
