// ErrorCard.test.tsx — unit coverage for the error card + carry-forward C1
// (retry disabled while retrying).

import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ErrorCard } from './ErrorCard';

afterEach(() => cleanup());

describe('ErrorCard', () => {
  it('renders the locked ERROR / Service Unavailable copy', () => {
    render(<ErrorCard onRetry={() => {}} />);
    expect(screen.getByText('ERROR')).toBeInTheDocument();
    expect(screen.getByText('Service Unavailable')).toBeInTheDocument();
    expect(
      screen.getByText('Unable to reach ACH. Try refreshing the page.'),
    ).toBeInTheDocument();
  });

  it('retry click calls onRetry', () => {
    const onRetry = vi.fn();
    render(<ErrorCard onRetry={onRetry} />);
    fireEvent.click(screen.getByRole('button', { name: 'retry' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('C1: retry is disabled when retrying=true', () => {
    render(<ErrorCard onRetry={() => {}} retrying />);
    expect(screen.getByRole('button', { name: 'retry' })).toBeDisabled();
  });

  it('C1: retry is enabled when retrying=false (default)', () => {
    render(<ErrorCard onRetry={() => {}} />);
    expect(screen.getByRole('button', { name: 'retry' })).not.toBeDisabled();
  });
});
