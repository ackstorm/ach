// BudgetPanel.test.tsx — jsdom smoke/contract suite for the STATS-08 budget panel.
//
// Asserts the locked copy + a representative value, the no-budget state, and the
// over-budget color switch (mirrors the dashboard BudgetBar logic).

import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';

import type { StatsBudget } from '@/lib/api-types';
import { BudgetPanel } from './BudgetPanel';

describe('BudgetPanel', () => {
  it('renders the section label + the "of" figure when a budget is set', () => {
    const budget: StatsBudget = {
      current: 12.5,
      max_budget: 50,
      budget_duration: '30d',
      source: 'user',
      pct: 0.25, // contract pct is a 0..1 FRACTION (12.5/50)
      has_budget: true,
    };
    const { getByText, container } = render(<BudgetPanel budget={budget} />);
    expect(getByText('ACCOUNT BUDGET')).toBeInTheDocument();
    // The figure carries the budget PERIOD ("/ 30d") plus the "% used" and a
    // (clock-dependent) monthly projection in child spans, so match on the
    // amount span's leading textContent.
    expect(
      getByText(
        (_content, el) =>
          el?.textContent?.startsWith('$12.50 of $50.00 / 30d') ?? false,
      ),
    ).toBeInTheDocument();
    expect(getByText(/25% used/)).toBeInTheDocument();
    // Under 80% -> neutral grey fill (not primary, not warning/destructive).
    const fill = container.querySelector('[data-slot="budget-fill"]');
    expect(fill?.className).toContain('bg-text-secondary');
    // 12.5/50 -> a 25%-wide bar (regression: was rendered as 0.25% sliver).
    expect((fill as HTMLElement).style.width).toBe('25%');
    expect(container.querySelector('[data-slot="budget-panel"]')).toHaveAttribute(
      'data-state',
      'ok'
    );
  });

  it('renders the no-budget state when has_budget is false', () => {
    const budget: StatsBudget = {
      current: 8,
      max_budget: null,
      budget_duration: null,
      source: 'unknown',
      pct: null,
      has_budget: false,
    };
    const { getByText, container } = render(<BudgetPanel budget={budget} />);
    expect(getByText('ACCOUNT BUDGET')).toBeInTheDocument();
    expect(getByText('$8.00 spent · no budget set')).toBeInTheDocument();
    // No bar in the no-budget state.
    expect(container.querySelector('[data-slot="budget-fill"]')).toBeNull();
    expect(container.querySelector('[data-slot="budget-panel"]')).toHaveAttribute(
      'data-state',
      'none'
    );
  });

  it('switches the fill to warning orange in [80%, 90%)', () => {
    const budget: StatsBudget = {
      current: 42, // 42/50 = 84%
      max_budget: 50,
      budget_duration: '30d',
      source: 'user',
      pct: 0.84,
      has_budget: true,
    };
    const { container } = render(<BudgetPanel budget={budget} />);
    expect(
      container.querySelector('[data-slot="budget-fill"]')?.className,
    ).toContain('bg-warning');
  });

  it('switches the fill to destructive at/above 90%', () => {
    const budget: StatsBudget = {
      current: 46, // 46/50 = 92%
      max_budget: 50,
      budget_duration: '30d',
      source: 'user',
      pct: 0.92,
      has_budget: true,
    };
    const { container } = render(<BudgetPanel budget={budget} />);
    expect(
      container.querySelector('[data-slot="budget-fill"]')?.className,
    ).toContain('bg-destructive');
  });

  it('switches the fill to destructive when over budget', () => {
    const budget: StatsBudget = {
      current: 120,
      max_budget: 50,
      budget_duration: '30d',
      source: 'user',
      pct: 2.4, // 120/50 as a 0..1-scale fraction
      has_budget: true,
    };
    const { container } = render(<BudgetPanel budget={budget} />);
    const fill = container.querySelector('[data-slot="budget-fill"]');
    expect(fill?.className).toContain('bg-destructive');
    // Over budget -> width clamped to 100% (ratio 2.4 clamps to 1).
    expect((fill as HTMLElement).style.width).toBe('100%');
  });

  it('renders the no-budget state for a null budget prop', () => {
    const { getByText } = render(<BudgetPanel budget={null} />);
    expect(getByText(/no budget set/)).toBeInTheDocument();
  });

  it('shows percent used and a monthly projection', () => {
    render(
      <BudgetPanel
        budget={{
          current: 16,
          max_budget: 400,
          budget_duration: '30d',
          source: 'team_member',
          pct: 0.04,
          has_budget: true,
        }}
      />,
    );
    expect(screen.getByText(/4% used/)).toBeInTheDocument();
    expect(screen.getByText(/projected/)).toBeInTheDocument();
  });

  it('omits the projection for a non-monthly duration', () => {
    render(
      <BudgetPanel
        budget={{
          current: 16,
          max_budget: 400,
          budget_duration: '24h',
          source: 'team_member',
          pct: 0.04,
          has_budget: true,
        }}
      />,
    );
    expect(screen.getByText(/4% used/)).toBeInTheDocument();
    expect(screen.queryByText(/projected/)).not.toBeInTheDocument();
  });
});
