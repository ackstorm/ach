import { useState } from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { FilterChips, toggled } from './FilterChips';

const OPTIONS = Array.from({ length: 13 }, (_, i) => ({ key: `g${i}`, label: `g${i}` }));
function Harness() {
  const [active, setActive] = useState<Set<string>>(new Set());
  return <FilterChips label="Access groups" allLabel="All access groups" options={OPTIONS} active={active} onToggle={(k) => setActive(toggled(active, k))} onClear={() => setActive(new Set())} max={5} />;
}
const chips = () => screen.getAllByRole('button').map((b) => b.textContent);

afterEach(cleanup);

describe('FilterChips', () => {
  it('shows 5 of 13 options plus "…", expands to all and collapses with "less"', () => {
    render(<Harness />);
    expect(chips()).toEqual(['All', 'g0', 'g1', 'g2', 'g3', 'g4', '…']);
    fireEvent.click(screen.getByRole('button', { name: 'Show 8 more access groups' }));
    expect(chips()).toHaveLength(1 + 13 + 1);
    expect(chips().at(-1)).toBe('less');
    fireEvent.click(screen.getByRole('button', { name: 'Show fewer access groups' }));
    expect(chips()).toHaveLength(7);
  });

  it('keeps a selected option beyond the first 5 visible when collapsed', () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: /more access groups/ }));
    fireEvent.click(screen.getByRole('button', { name: 'g9' }));
    fireEvent.click(screen.getByRole('button', { name: 'Show fewer access groups' }));
    expect(screen.getByRole('button', { name: 'g9' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'All access groups' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: 'Show 7 more access groups' })).toBeInTheDocument();
  });
});
