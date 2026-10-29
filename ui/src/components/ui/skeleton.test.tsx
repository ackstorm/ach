// skeleton.test.tsx — vitest suite for the Skeleton placeholder primitive.
//
// Verifies: it renders with the shimmer (animate-pulse) class; each variant
// carries its footprint class and data-variant; a free-form className is merged
// (size/shape via className); table-rows renders the requested row count; and
// every node is aria-hidden (decorative).

import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';

import { Skeleton } from './skeleton';

describe('Skeleton', () => {
  it('renders a card block with the shimmer (animate-pulse) class by default', () => {
    const { container } = render(<Skeleton />);
    const el = container.querySelector('[data-slot="skeleton"]')!;
    expect(el).toHaveAttribute('data-variant', 'card');
    expect(el).toHaveClass('animate-pulse');
    expect(el).toHaveClass('h-24'); // 96px card footprint
    expect(el).toHaveAttribute('aria-hidden', 'true');
  });

  it('applies the chart footprint for variant="chart"', () => {
    const { container } = render(<Skeleton variant="chart" />);
    const el = container.querySelector('[data-slot="skeleton"]')!;
    expect(el).toHaveAttribute('data-variant', 'chart');
    expect(el).toHaveClass('h-60'); // 240px chart footprint
  });

  it('applies the bar footprint (pill) for variant="bar"', () => {
    const { container } = render(<Skeleton variant="bar" />);
    const el = container.querySelector('[data-slot="skeleton"]')!;
    expect(el).toHaveClass('h-3.5');
    expect(el).toHaveClass('rounded-full');
  });

  it('merges a free-form className for one-off sizing', () => {
    const { container } = render(<Skeleton className="h-8 w-32" />);
    const el = container.querySelector('[data-slot="skeleton"]')!;
    expect(el).toHaveClass('w-32');
    // twMerge keeps the explicit height over the default h-24.
    expect(el).toHaveClass('h-8');
    expect(el).not.toHaveClass('h-24');
  });

  it('renders the requested number of rows for variant="table-rows"', () => {
    const { container } = render(<Skeleton variant="table-rows" rows={3} />);
    const wrapper = container.querySelector('[data-variant="table-rows"]')!;
    expect(wrapper).toHaveAttribute('aria-hidden', 'true');
    const rowBars = wrapper.querySelectorAll('.animate-pulse');
    expect(rowBars).toHaveLength(3);
  });

  it('falls back to 5 rows when rows is invalid', () => {
    const { container } = render(<Skeleton variant="table-rows" rows={0} />);
    const wrapper = container.querySelector('[data-variant="table-rows"]')!;
    expect(wrapper.querySelectorAll('.animate-pulse')).toHaveLength(5);
  });
});
