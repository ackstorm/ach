// team-color.ts — a STABLE categorical color for an item, by its position in a
// caller-supplied list. Uses the same --cat-1..5 palette as the usage donut, so
// a given item reads as the same hue wherever it appears. Originally sized for
// the alitellm-auth team list; the dashboard's Environments pill list is a
// smaller, order-preserving list built the same way, so the same index-based
// contract applies unchanged.
//
// Index (not a string hash) so the first 5 items are guaranteed DISTINCT — a
// hash collides (two items → same color) even with few items, which defeats
// the point. Returns a CSS var string (never a raw hex) for inline `style`.

const CAT_VARS = [
  'var(--cat-1)',
  'var(--cat-2)',
  'var(--cat-3)',
  'var(--cat-4)',
  'var(--cat-5)',
] as const;

/**
 * Palette color for the team at `index` in the teams list. A negative index
 * (team not found in the list) → the neutral token. Wraps past 5 teams.
 */
export function teamColorVar(index: number): string {
  if (!Number.isInteger(index) || index < 0) return 'var(--text-tertiary)';
  return CAT_VARS[index % CAT_VARS.length];
}
