import { describe, it, expect } from 'vitest';
import { matchesSearch } from './table-search';

describe('matchesSearch', () => {
  it('matches case-insensitive substrings, empty = all', () => {
    expect(matchesSearch('git', 'mcp-GitLab', null)).toBe(true);
    expect(matchesSearch('slack', 'mcp-gitlab')).toBe(false);
    expect(matchesSearch('', 'anything')).toBe(true);
  });

  it('ignores null/undefined fields and trims the term', () => {
    expect(matchesSearch('  lab ', 'mcp-gitlab', undefined)).toBe(true);
    expect(matchesSearch('x', null, undefined)).toBe(false);
  });
});
