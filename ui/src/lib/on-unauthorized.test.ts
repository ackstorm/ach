import { afterEach, describe, expect, test, vi } from 'vitest';
import { notifyUnauthorized, setUnauthorizedHandler } from './on-unauthorized';

afterEach(() => {
  // Clear the module-level handler so tests stay isolated.
  setUnauthorizedHandler(null);
});

describe('on-unauthorized handler registry', () => {
  test('notifyUnauthorized invokes the registered handler', () => {
    const fn = vi.fn();
    setUnauthorizedHandler(fn);
    notifyUnauthorized();
    expect(fn).toHaveBeenCalledTimes(1);
  });

  test('notifyUnauthorized is a no-op when no handler is registered', () => {
    expect(() => notifyUnauthorized()).not.toThrow();
  });

  test('setUnauthorizedHandler replaces the previous handler', () => {
    const first = vi.fn();
    const second = vi.fn();
    setUnauthorizedHandler(first);
    setUnauthorizedHandler(second);
    notifyUnauthorized();
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });
});
