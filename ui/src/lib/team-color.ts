// team-color.ts — a STABLE categorical color for a team, by its position in the
// (shared) teams list. Uses the same --cat-1..5 palette as the usage donut, so a
// given team reads as the same hue wherever it appears (dashboard Team tile, keys
// table) — both call sites read the SAME useTeams() array, so equal indices agree.
//
// Index (not a string hash) so the first 5 teams are guaranteed DISTINCT — a hash
// collides (two teams → same color) even with few teams, which defeats the point.
// Returns a CSS var string (never a raw hex) for inline `style`.

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
