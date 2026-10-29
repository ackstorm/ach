import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import type { StatsBudget } from '@/lib/api-types';
import { AllowancePanel } from './AllowancePanel';

afterEach(cleanup);
const day = (offset: number) => new Date(Date.now() + offset * 86_400_000).toISOString().slice(0, 10);
const budget = (spend: number | null, series?: { date: string; spend: number }[]): StatsBudget => ({
  current: 50, max_budget: 100, budget_duration: '30d', reset_at: null, source: 'tag', pct: 0.5, has_budget: true, cycle_series: series,
  projection: { basis: 'linear_cycle_rate', cycle_start: `${day(-10)}T00:00:00Z`, horizon_end: `${day(20)}T00:00:00Z`, spend, pct: null, reason: null },
});
const projection = (c: HTMLElement) => c.querySelector('.desktop-chart .cycle-projection')!;

describe('AllowancePanel', () => {
  it('plots the cycle from budget.cycle_series and starts the projection at budget.current', () => {
    const { container } = render(<AllowancePanel budget={budget(57, [{ date: day(-10), spend: 20 }, { date: day(-1), spend: 30 }])} />);
    const line = projection(container);
    const top = container.querySelector('.desktop-chart .actual-line')!.getAttribute('d')!.split(' ').at(-1);
    expect(line.getAttribute('y1')).toBe(top); // the cumulative line ends where the projection starts
    expect(Number(line.getAttribute('y2'))).toBeLessThanOrEqual(Number(line.getAttribute('y1')));
  });

  it('a projection below the current spend stays flat, never slopes down', () => {
    const { container } = render(<AllowancePanel budget={budget(40, [{ date: day(-5), spend: 50 }])} />);
    const line = projection(container);
    expect(line.getAttribute('y2')).toBe(line.getAttribute('y1'));
  });

  it('without cycle_series the chart reads as unavailable', () => {
    render(<AllowancePanel budget={budget(57)} />);
    expect(screen.getByText('Allowance cycle spend could not be loaded.')).toBeInTheDocument();
  });

  it('hovering a past day shows its cumulative spend, a future day the projection, both with the cap', () => {
    const { container } = render(<AllowancePanel budget={budget(57, [{ date: day(-10), spend: 20 }, { date: day(-1), spend: 30 }])} />);
    const svg = container.querySelector('svg.desktop-chart')!;
    svg.getBoundingClientRect = () => ({ left: 0, top: 0, width: 820, height: 190, right: 820, bottom: 190, x: 0, y: 0, toJSON: () => ({}) });
    fireEvent.mouseMove(svg, { clientX: 48, clientY: 60 }); // first day of the cycle
    expect(container.querySelector('.chart-tip')).toHaveTextContent('Cumulative spend$20.00Allowance cap$100.00');
    fireEvent.mouseMove(svg, { clientX: 808, clientY: 60 }); // last day: the projection's end
    expect(container.querySelector('.chart-tip')).toHaveTextContent('Projected$57.00Allowance cap$100.00');
  });
});
