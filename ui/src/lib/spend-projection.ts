// spend-projection.ts — pure spend-budget derivations for the budget panels.
// `now` is injectable so month-end projection is unit-testable.
export function isMonthlyDuration(d: string | null | undefined): boolean {
  if (!d) return false;
  const s = d.toLowerCase();
  return s === '30d' || s === '1mo' || s === 'monthly' || s === '1month';
}

// Linear month-end projection from month-to-date spend. Naive run-rate — the
// figure is labelled "projected" at the call site.
export function projectMonthEnd(currentSpend: number, now: Date = new Date()): number {
  const day = now.getUTCDate();
  const daysInMonth = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 0)).getUTCDate();
  if (day <= 0) return currentSpend;
  return (currentSpend / day) * daysInMonth;
}

// "4% used" (rounded to a whole percent), or null when no positive budget.
export function budgetPctLabel(current: number, maxBudget: number | null): string | null {
  if (maxBudget === null || maxBudget <= 0) return null;
  return `${Math.round((current / maxBudget) * 100)}% used`;
}
