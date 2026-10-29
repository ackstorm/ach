// theme.test.ts — the tri-state theme helpers (dark / light / pastel).

import { afterEach, describe, expect, it } from 'vitest';

import {
  type Theme,
  applyThemeClass,
  nextTheme,
  readStoredTheme,
  THEME_STORAGE_KEY,
} from './theme';

afterEach(() => {
  localStorage.clear();
  document.documentElement.classList.remove('dark', 'light', 'pastel', 'red');
});

describe('nextTheme', () => {
  it('cycles dark -> light -> pastel -> red -> dark', () => {
    expect(nextTheme('dark')).toBe('light');
    expect(nextTheme('light')).toBe('pastel');
    expect(nextTheme('pastel')).toBe('red');
    expect(nextTheme('red')).toBe('dark');
  });
});

describe('readStoredTheme', () => {
  it('accepts pastel as a valid stored value', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'pastel');
    expect(readStoredTheme()).toBe('pastel');
  });

  it('returns null for an unknown value', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'neon');
    expect(readStoredTheme()).toBeNull();
  });
});

describe('applyThemeClass', () => {
  it.each<Theme>(['dark', 'light', 'pastel', 'red'])(
    'puts exactly the %s class on <html>',
    (theme) => {
      // Seed a different class to prove the others are removed.
      document.documentElement.classList.add('dark', 'light', 'pastel', 'red');
      applyThemeClass(theme);
      const cls = document.documentElement.classList;
      expect(cls.contains(theme)).toBe(true);
      expect(
        ['dark', 'light', 'pastel', 'red'].filter((t) => cls.contains(t))
      ).toEqual([theme]);
    }
  );
});
