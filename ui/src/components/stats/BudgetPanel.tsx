// BudgetPanel.tsx — the Budget status panel for the #/stats Usage & Spend page
// (STATS-08). React + Tailwind port of the old Preact leaf src/ui/stats-budget.js.
//
// REUSES the dashboard BudgetBar logic (Dashboard.tsx) VERBATIM in shape so the
// page never reintroduces the FID-03 `$0.00 of $0.00` full-green 100%-bar defect
// (CONTEXT D-09):
//   • No budget (budget.has_budget === false / max_budget null) -> HIDE the bar
//     and show `${current} spent · no budget set`. NEVER divide by zero, NEVER a
//     full-green bar with no budget.
//   • Has budget -> a single fill clamped to [0,100]% of an 8px track, colored
//     primary under budget and switched to destructive once current > max_budget.
//     The fill width is the spend ratio; the over-budget state is signalled by
//     color (summing two flex children misrepresented the magnitude — WR-01).
//
// PURE presentational leaf — no fetch, no data ownership. The container passes
// the Phase-12 `budget` slice. NEVER a raw hex — token classes only.
//
// SECURITY (T-13-06, info disclosure — ACCEPTED): the budget figures are
// session-scoped, server-shaped; the browser never touches the master key or
// `sk-`. Numbers pass through format.ts and render as React text children.

import * as React from 'react';

import { BudgetMeter } from '@/components/ui/budget-meter';
import type { StatsBudget } from '@/lib/api-types';
import { formatCurrency } from '@/lib/format';
import { budgetPctLabel, isMonthlyDuration, projectMonthEnd } from '@/lib/spend-projection';

// "ACCOUNT BUDGET" matches the Dashboard BudgetBar label verbatim (the bar is
// the same enforced per-member budget) — was "BUDGET STATUS", the wording drift
// flagged in the design review.
const SECTION_LABEL = 'ACCOUNT BUDGET';

export interface BudgetPanelProps {
  /** The Phase-12 `budget` slice {current, max_budget, source, pct, has_budget}. */
  budget: StatsBudget | null | undefined;
}

// Mirrors Dashboard.tsx BudgetBar in shape (re-namespaced for the stats page).
export function BudgetPanel({ budget }: BudgetPanelProps): React.ReactElement {
  const b = budget ?? null;
  const current = b && typeof b.current === 'number' ? b.current : 0;
  const maxBudget =
    b && typeof b.max_budget === 'number' ? b.max_budget : null;
  // has_budget is the authority; a null max_budget is also "no budget".
  const hasBudget = b?.has_budget === true && maxBudget !== null;

  // No budget set: hide the bar, show the spent-no-budget copy. This is the exact
  // FID-03 defect-avoidance the dashboard already ships — never a divide-by-zero,
  // never a full-green 100% bar (D-09).
  if (!hasBudget) {
    return (
      <div
        data-slot="budget-panel"
        data-state="none"
        className="flex flex-col gap-2 rounded-xl border border-border bg-surface p-5"
      >
        <div className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
          {SECTION_LABEL}
        </div>
        <div className="font-sans text-sm text-text-secondary">
          {formatCurrency(current)} spent · no budget set
        </div>
      </div>
    );
  }

  // Budget present. Derive the fill from current/max_budget (the divide-by-zero
  // guard makes a 0 max a full bar), EXACTLY mirroring the dashboard BudgetBar.
  // The server also sends `budget.pct`, but it is the SAME ratio as a 0..1
  // FRACTION — using it as a 0..100 percent rendered a sliver (15.94/50 -> a
  // 0.32%-wide bar). The fill is a SINGLE width clamped to [0,100]%; over-budget
  // is signalled by color.
  const over = current > maxBudget;

  return (
    <div
      data-slot="budget-panel"
      data-state={over ? 'over' : 'ok'}
      className="flex flex-col gap-2 rounded-xl border border-border bg-surface p-5"
    >
      <div className="flex items-baseline justify-between gap-3">
        <span className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
          {SECTION_LABEL}
        </span>
        <span className="font-mono text-xs text-text-primary">
          {formatCurrency(current)} of {formatCurrency(maxBudget)}
          {b?.budget_duration ? (
            // The budget is per-period; show it so "$X of $Y" isn't ambiguous.
            <span className="text-text-tertiary"> / {b.budget_duration}</span>
          ) : null}
          {budgetPctLabel(current, maxBudget) ? (
            <span className="text-text-tertiary"> · {budgetPctLabel(current, maxBudget)}</span>
          ) : null}
          {isMonthlyDuration(b?.budget_duration) ? (
            <span className="text-text-tertiary">
              {' '}· projected {formatCurrency(projectMonthEnd(current))}
            </span>
          ) : null}
        </span>
      </div>
      <BudgetMeter
        current={current}
        maxBudget={maxBudget}
        duration={b?.budget_duration}
      />
    </div>
  );
}
