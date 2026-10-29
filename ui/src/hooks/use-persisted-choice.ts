// use-persisted-choice.ts — a panel selector's choice kept in localStorage
// (`console.select.<name>`). Blocked storage or a stale value → the default.
import { useState } from 'react';

export function usePersistedChoice<T extends string>(name: string, options: readonly T[], fallback: T): [T, (value: T) => void] {
  const key = `console.select.${name}`;
  const [value, setValue] = useState<T>(() => { try { const saved = window.localStorage.getItem(key); return options.includes(saved as T) ? saved as T : fallback; } catch { return fallback; } });
  return [value, (next) => { setValue(next); try { window.localStorage.setItem(key, next); } catch { /* storage blocked */ } }];
}
