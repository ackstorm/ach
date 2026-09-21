// budget.ts — usage-threshold fill color for the account budget bar (shared by
// the Dashboard BudgetBar and the Stats BudgetPanel).
//
// Neutral grey UNDER 80% (calm, and — importantly — it never clashes with the
// red theme, where the accent itself is red, so a danger color stays legible),
// WARNING orange in [80%, 90%), DESTRUCTIVE red at/over 90% (which also covers
// the over-budget case, ratio > 1). Theme-token classes ONLY — never a raw hex.
// Missing / non-finite input falls through to the neutral class.
export function budgetFillClass(ratio: number | null | undefined): string {
  if (typeof ratio === 'number' && Number.isFinite(ratio)) {
    if (ratio >= 0.9) return 'bg-destructive';
    if (ratio >= 0.8) return 'bg-warning';
  }
  return 'bg-text-secondary';
}
