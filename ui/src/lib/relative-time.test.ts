import { describe, it, expect } from 'vitest';
import { relativeTime, isStale } from './relative-time';

const NOW = new Date('2026-07-09T12:00:00Z');

describe('relativeTime', () => {
  it('returns "Never used" for null/undefined', () => {
    expect(relativeTime(null, NOW)).toBe('Never used');
    expect(relativeTime(undefined, NOW)).toBe('Never used');
  });
  it('returns "—" for an unparseable date', () => {
    expect(relativeTime('not-a-date', NOW)).toBe('—');
  });
  it('formats sub-minute as "just now"', () => {
    expect(relativeTime('2026-07-09T11:59:30Z', NOW)).toBe('just now');
  });
  it('formats hours and days', () => {
    expect(relativeTime('2026-07-09T10:00:00Z', NOW)).toBe('2h ago');
    expect(relativeTime('2026-07-06T12:00:00Z', NOW)).toBe('3 days ago');
  });
});

describe('isStale', () => {
  it('is true for null (never used) and for old dates', () => {
    expect(isStale(null, NOW)).toBe(true);
    expect(isStale('2026-05-01T12:00:00Z', NOW)).toBe(true);
  });
  it('is false for recent dates', () => {
    expect(isStale('2026-07-08T12:00:00Z', NOW)).toBe(false);
  });
});
