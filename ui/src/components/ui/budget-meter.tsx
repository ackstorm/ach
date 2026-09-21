// budget-meter.tsx — the shared account-budget meter bar, used by BOTH the
// Dashboard BudgetBar and the Stats BudgetPanel so the two never drift (the old
// duplicated `<div class="flex h-2 …">` track lived verbatim in both files).
//
// Renders the spend fill (width = clamped spend ratio, threshold-colored via
// budgetFillClass — deliberately neutral under 80% so it stays calm and never
// clashes with the red theme) PLUS a run-rate projection: a SOFTER (translucent)
// continuation of the SAME fill color that extends from the actual spend up to
// the month-end-projected position, so "you are here → on track to reach here"
// reads at a glance. The ghost only shows for monthly durations (projectMonthEnd
// is a month-run-rate) and only when projected > current.
//
// PURE presentational — token classes only, never a raw hex.

import { budgetFillClass } from '@/lib/budget';
import { formatCurrency } from '@/lib/format';
import { isMonthlyDuration, projectMonthEnd } from '@/lib/spend-projection';

export interface BudgetMeterProps {
  current: number;
  /** Caller guarantees > 0 (the no-budget case renders copy, not a meter). */
  maxBudget: number;
  duration?: string | null;
}

// The projection "ghost" — the SAME threshold color as the actual-spend fill
// (keyed on the CURRENT ratio, per the design ask) but softened to /25 opacity.
// Literal strings (never templated) so Tailwind's scanner emits them.
function ghostFillClass(ratio: number | null | undefined): string {
  if (typeof ratio !== 'number' || !Number.isFinite(ratio)) {
    return 'bg-text-secondary/25';
  }
  if (ratio >= 0.9) return 'bg-destructive/25';
  if (ratio >= 0.8) return 'bg-warning/25';
  return 'bg-text-secondary/25';
}

export function BudgetMeter({ current, maxBudget, duration }: BudgetMeterProps) {
  const ratio = maxBudget > 0 ? current / maxBudget : 1;
  const fillPct = Math.max(0, Math.min(1, ratio)) * 100;

  const projected = isMonthlyDuration(duration) ? projectMonthEnd(current) : null;
  const projectedRatio =
    projected !== null && maxBudget > 0 ? projected / maxBudget : null;
  const projectedPct =
    projectedRatio !== null ? Math.max(0, Math.min(1, projectedRatio)) * 100 : null;

  return (
    <div className="relative flex h-3 overflow-hidden rounded-full border border-border bg-background">
      {/* Projection ghost — behind the actual fill, same hue softened, reaching
          the run-rate month-end position. Only when it extends past current. */}
      {projectedPct !== null && projectedPct > fillPct ? (
        <div
          data-slot="budget-projection"
          title={`Projected month-end: ${formatCurrency(projected as number)}`}
          className={`absolute inset-y-0 left-0 rounded-full ${ghostFillClass(ratio)}`}
          style={{ width: `${projectedPct}%` }}
        />
      ) : null}
      {/* Actual spend fill — opaque, on top of the ghost. */}
      <div
        data-slot="budget-fill"
        className={`relative h-full transition-[width] duration-500 ease-out ${budgetFillClass(ratio)}`}
        style={{ width: `${fillPct}%` }}
      />
    </div>
  );
}
