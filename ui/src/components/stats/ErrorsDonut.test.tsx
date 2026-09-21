// ErrorsDonut.test.tsx — vitest suite for the request-outcome donut (jsdom).
// Recharts ResponsiveContainer is mocked so the donut paints an SVG under jsdom.

import { cloneElement, isValidElement } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';

vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    ResponsiveContainer: ({ children }: { children: React.ReactNode }) =>
      isValidElement(children)
        ? cloneElement(
            children as React.ReactElement<{ width?: number; height?: number }>,
            { width: 400, height: 200 },
          )
        : children,
  };
});

import type { LatencyResponse } from '@/lib/api-types';
import { ErrorsDonut } from './ErrorsDonut';

function makeLatency(overrides: Partial<LatencyResponse> = {}): LatencyResponse {
  return {
    available: true,
    sampled: false,
    row_count: 280,
    window: { start: '2026-07-05', end: '2026-07-11', days: 7 },
    latency: null,
    outcomes: [
      { status: 'success', count: 260 },
      { status: 'failure', count: 20 },
    ],
    by_model: [],
    ...overrides,
  };
}

afterEach(cleanup);

describe('ErrorsDonut', () => {
  it('renders the unavailable state when available is false', () => {
    render(<ErrorsDonut data={makeLatency({ available: false })} isError={false} />);
    expect(screen.getByText(/not available/i)).toBeInTheDocument();
  });

  it('renders the empty state when there are no outcomes', () => {
    render(<ErrorsDonut data={makeLatency({ outcomes: [] })} isError={false} />);
    expect(screen.getByText(/no requests in this range/i)).toBeInTheDocument();
  });

  it('centers the failed count and lists both outcomes with percentages', () => {
    render(<ErrorsDonut data={makeLatency()} isError={false} />);
    expect(screen.getByText('FAILED')).toBeInTheDocument();
    // "20" appears in the center AND the legend row — assert presence, not count.
    expect(screen.getAllByText('20').length).toBeGreaterThan(0);
    expect(screen.getByText('success')).toBeInTheDocument();
    expect(screen.getByText('failure')).toBeInTheDocument();
    expect(screen.getByText('92.9%')).toBeInTheDocument();
    expect(screen.getByText('7.1%')).toBeInTheDocument();
  });
});
