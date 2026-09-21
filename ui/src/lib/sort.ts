// sort.ts — tiny shared comparison helper for client-side table sorting.
//
// Used by the generic DataTable (opt-in column sort) and the hand-rolled
// TopKeys grid, so both tables order identically and the rule lives in one place.

export type SortDir = 'asc' | 'desc';

/**
 * Compare two cell values for sorting.
 *
 * `null` / `undefined` always sort LAST, regardless of direction — a missing
 * figure is never treated as the largest OR smallest value (mirrors the table
 * convention that `null` ≠ `0`). Numbers compare numerically; non-finite numbers
 * (NaN/±Infinity) are treated as missing. Everything else compares as a locale
 * string with numeric awareness so `key-2` sorts before `key-10`.
 */
export function compareValues(
  a: number | string | null | undefined,
  b: number | string | null | undefined,
  dir: SortDir
): number {
  const aMissing = a == null || (typeof a === 'number' && !Number.isFinite(a));
  const bMissing = b == null || (typeof b === 'number' && !Number.isFinite(b));
  if (aMissing && bMissing) return 0;
  if (aMissing) return 1; // a after b
  if (bMissing) return -1; // b after a

  let c: number;
  if (typeof a === 'number' && typeof b === 'number') {
    c = a < b ? -1 : a > b ? 1 : 0;
  } else {
    c = String(a).localeCompare(String(b), undefined, { numeric: true });
  }
  return dir === 'asc' ? c : -c;
}

/**
 * Stable sort of `rows` by `accessor` in `dir`. Returns a new array; the input
 * is never mutated. Ties keep their original relative order (index tiebreak).
 */
export function sortRows<T>(
  rows: readonly T[],
  accessor: (row: T) => number | string | null | undefined,
  dir: SortDir
): T[] {
  return rows
    .map((row, i) => [row, i] as const)
    .sort(([a, ai], [b, bi]) => {
      const c = compareValues(accessor(a), accessor(b), dir);
      return c !== 0 ? c : ai - bi;
    })
    .map(([row]) => row);
}
