import { describe, expect, it } from 'vitest';
import { cacheStatus, inputStatus, ratioStatus } from './observability-targets';

describe('observability targets', () => {
  it('ratio: 9.9 and 10 acceptable, 10.1 red', () => {
    expect(ratioStatus(9.9)).toBe('ok');
    expect(ratioStatus(10)).toBe('ok');
    expect(ratioStatus(10.1)).toBe('over');
  });
  it('cache: 94.9% red, 95% and 95.1% acceptable', () => {
    expect(cacheStatus(0.949)).toBe('over');
    expect(cacheStatus(0.95)).toBe('ok');
    expect(cacheStatus(0.951)).toBe('ok');
  });
  it('input: 150K acceptable, above it red', () => {
    expect(inputStatus(150_000)).toBe('ok');
    expect(inputStatus(150_001)).toBe('over');
  });
  it('statuses are independent: 40:1 red with 95% cache acceptable', () => {
    expect([ratioStatus(40), cacheStatus(0.95)]).toEqual(['over', 'ok']);
  });
  it('missing or undefined measurements carry no status', () => {
    expect(ratioStatus(null)).toBeNull();
    expect(ratioStatus(Infinity)).toBeNull();
    expect(cacheStatus(undefined)).toBeNull();
    expect(inputStatus(undefined)).toBeNull();
  });
});
