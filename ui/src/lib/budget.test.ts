// budget.test.ts — node-env unit tests for the budget-bar threshold colors.
import { describe, expect, it } from 'vitest';

import { budgetFillClass } from './budget';

describe('budgetFillClass', () => {
  it('is neutral grey below 80%', () => {
    expect(budgetFillClass(0)).toBe('bg-text-secondary');
    expect(budgetFillClass(0.5)).toBe('bg-text-secondary');
    expect(budgetFillClass(0.799)).toBe('bg-text-secondary');
  });

  it('is warning orange in [80%, 90%)', () => {
    expect(budgetFillClass(0.8)).toBe('bg-warning');
    expect(budgetFillClass(0.89)).toBe('bg-warning');
  });

  it('is destructive red at/above 90% and over budget', () => {
    expect(budgetFillClass(0.9)).toBe('bg-destructive');
    expect(budgetFillClass(1)).toBe('bg-destructive');
    expect(budgetFillClass(2.4)).toBe('bg-destructive');
  });

  it('falls back to grey for non-finite / missing input', () => {
    expect(budgetFillClass(NaN)).toBe('bg-text-secondary');
    expect(budgetFillClass(null)).toBe('bg-text-secondary');
    expect(budgetFillClass(undefined)).toBe('bg-text-secondary');
  });
});
