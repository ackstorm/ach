// format-guard.test.ts — numbers and dates are formatted only in lib/format.ts
// (one configurable locale). Any other source file that calls toFixed,
// toLocale*String or Intl.Number/DateTimeFormat fails here.
import { expect, it } from 'vitest';

const sources = import.meta.glob(['/src/**/*.{ts,tsx}', '!/src/lib/format.ts', '!/src/**/*.test.*'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>;
const FORBIDDEN = /\.toFixed\(|\.toLocale\w*String\(|\bIntl\.(Number|DateTime)Format\b/;

it('formats numbers and dates only through lib/format.ts', () => {
  expect(Object.keys(sources).length).toBeGreaterThan(50);
  expect(Object.keys(sources).filter((file) => FORBIDDEN.test(sources[file]))).toEqual([]);
});
