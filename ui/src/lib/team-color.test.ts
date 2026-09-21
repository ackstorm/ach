import { describe, it, expect } from 'vitest';
import { teamColorVar } from './team-color';

const PALETTE = [
  'var(--cat-1)',
  'var(--cat-2)',
  'var(--cat-3)',
  'var(--cat-4)',
  'var(--cat-5)',
];

describe('teamColorVar', () => {
  it('returns the neutral token for a negative index (team not found)', () => {
    expect(teamColorVar(-1)).toBe('var(--text-tertiary)');
  });

  it('maps the first five indices to distinct palette colors', () => {
    const colors = [0, 1, 2, 3, 4].map(teamColorVar);
    expect(colors).toEqual(PALETTE);
    expect(new Set(colors).size).toBe(5); // all distinct
  });

  it('wraps past the palette length', () => {
    expect(teamColorVar(5)).toBe(teamColorVar(0));
    expect(teamColorVar(6)).toBe(teamColorVar(1));
  });
});
