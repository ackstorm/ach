import { describe, expect, it } from 'vitest';
import { cacheStatus, ratioStatus } from './observability-targets';

describe('observability targets', () => {
  it('ratio: 9.9 and 10 acceptable, 10.1 red', () => {
    expect(ratioStatus(9.9)).toBe('ok');
    expect(ratioStatus(10)).toBe('ok');
    expect(ratioStatus(10.1)).toBe('over');
  });
  it('cache: 79.9% red, 80% and 80.1% acceptable', () => {
    expect(cacheStatus(0.799)).toBe('over');
    expect(cacheStatus(0.8)).toBe('ok');
    expect(cacheStatus(0.801)).toBe('ok');
  });
  it('statuses are independent: 40:1 red with 95% cache acceptable', () => {
    expect([ratioStatus(40), cacheStatus(0.95)]).toEqual(['over', 'ok']);
  });
  it('missing or undefined measurements carry no status', () => {
    expect(ratioStatus(null)).toBeNull();
    expect(ratioStatus(Infinity)).toBeNull();
    expect(cacheStatus(undefined)).toBeNull();
  });
});
