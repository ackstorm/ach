import { afterEach, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { usePersistedChoice } from './use-persisted-choice';

const OPTIONS = ['Cost', 'Tokens', 'Calls'] as const;
afterEach(() => { vi.restoreAllMocks(); window.localStorage.clear(); });

it('restores a stored choice and writes a new one', () => {
  window.localStorage.setItem('console.select.x', 'Tokens');
  const { result } = renderHook(() => usePersistedChoice('x', OPTIONS, 'Cost'));
  expect(result.current[0]).toBe('Tokens');
  act(() => result.current[1]('Calls'));
  expect(result.current[0]).toBe('Calls');
  expect(window.localStorage.getItem('console.select.x')).toBe('Calls');
});

it('an invalid stored value falls back to the default', () => {
  window.localStorage.setItem('console.select.x', 'Bogus');
  expect(renderHook(() => usePersistedChoice('x', OPTIONS, 'Cost')).result.current[0]).toBe('Cost');
});

it('storage that throws falls back to the default and still switches', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked'); });
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked'); });
  const { result } = renderHook(() => usePersistedChoice('x', OPTIONS, 'Cost'));
  expect(result.current[0]).toBe('Cost');
  act(() => result.current[1]('Tokens'));
  expect(result.current[0]).toBe('Tokens');
});
