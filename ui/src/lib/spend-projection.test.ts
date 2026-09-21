import { describe, it, expect } from 'vitest';
import { isMonthlyDuration, projectMonthEnd, budgetPctLabel } from './spend-projection';

describe('isMonthlyDuration', () => {
  it('recognises monthly durations', () => {
    for (const d of ['30d', '1mo', 'monthly', '1month']) expect(isMonthlyDuration(d)).toBe(true);
  });
  it('rejects sub-monthly / null', () => {
    for (const d of ['24h', '7d', null, undefined]) expect(isMonthlyDuration(d)).toBe(false);
  });
});

describe('projectMonthEnd', () => {
  it('extrapolates MTD spend across the month', () => {
    // July has 31 days; on the 9th, $15 MTD -> 15/9*31 = 51.66...
    const now = new Date('2026-07-09T12:00:00Z');
    expect(projectMonthEnd(15, now)).toBeCloseTo((15 / 9) * 31, 2);
  });
});

describe('budgetPctLabel', () => {
  it('formats percent used, null when no budget', () => {
    expect(budgetPctLabel(16, 400)).toBe('4% used');
    expect(budgetPctLabel(16, null)).toBeNull();
    expect(budgetPctLabel(16, 0)).toBeNull();
  });
});
