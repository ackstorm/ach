// format.test.ts — vitest unit suite for the pure display formatters in
// format.ts. Runs in the default (jsdom) environment but uses no DOM — every
// formatter is pure (no DOM/network refs). Ported verbatim from
// src/ui/format.test.js.
//
// The locked display strings come from 10-UI-SPEC §Typography
// ("Table number formatting" + "Metric-tile number sizing"): currency to 2
// decimals with thousands separators, integers thousands-grouped, abbreviated
// 2.45M, MMM DD, YYYY dates, sk-…last4 masks, and the em-dash "—" (U+2014) for
// every null/undefined input.
import { describe, it, expect } from 'vitest';
import {
  formatCurrency,
  formatInt,
  abbreviate,
  formatDate,
  formatLocalDate,
  formatLocalDateTime,
  formatLocalTime,
  formatShortDay,
  formatCurrencyCompact,
  maskKey,
  formatPricePerMillion,
} from './format';

const EM_DASH = '—'; // — (U+2014)

describe('formatPricePerMillion — per-1M-token USD price (×1e6)', () => {
  it('scales a per-token cost to a per-1M currency string', () => {
    expect(formatPricePerMillion(1.5e-7)).toBe('$0.15');
    expect(formatPricePerMillion(6e-7)).toBe('$0.60');
    expect(formatPricePerMillion(3e-6)).toBe('$3.00');
  });
  it('renders a genuine 0 cost as $0.00 (free), not a dash', () => {
    expect(formatPricePerMillion(0)).toBe('$0.00');
  });
  it('returns the em-dash for null/undefined/non-numeric', () => {
    expect(formatPricePerMillion(null)).toBe(EM_DASH);
    expect(formatPricePerMillion(undefined)).toBe(EM_DASH);
  });
});

describe('formatCurrency — USD, 2 decimals, thousands separators', () => {
  it('formats a fractional value to 2 decimals with grouping', () => {
    expect(formatCurrency(1249.5)).toBe('$1,249.50');
  });

  it('formats zero as $0.00', () => {
    expect(formatCurrency(0)).toBe('$0.00');
  });

  it('rounds to 2 decimals', () => {
    expect(formatCurrency(1249.499)).toBe('$1,249.50');
  });

  it('returns the em-dash for null/undefined', () => {
    expect(formatCurrency(null)).toBe(EM_DASH);
    expect(formatCurrency(undefined)).toBe(EM_DASH);
  });

  it('does not throw on a non-numeric input', () => {
    expect(() => formatCurrency('x')).not.toThrow();
    expect(formatCurrency('x')).toBe(EM_DASH);
  });
});

describe('formatInt — thousands-grouped integers', () => {
  it('groups millions with separators', () => {
    expect(formatInt(1000000)).toBe('1,000,000');
  });

  it('formats zero as 0', () => {
    expect(formatInt(0)).toBe('0');
  });

  it('returns the em-dash for null/undefined', () => {
    expect(formatInt(null)).toBe(EM_DASH);
    expect(formatInt(undefined)).toBe(EM_DASH);
  });

  it('does not throw on a non-numeric input', () => {
    expect(() => formatInt('x')).not.toThrow();
    expect(formatInt('x')).toBe(EM_DASH);
  });
});

describe('abbreviate — K/M/B suffixes, up to 2 decimals trimmed', () => {
  it('abbreviates millions', () => {
    expect(abbreviate(2450000)).toBe('2.45M');
  });

  it('abbreviates thousands', () => {
    expect(abbreviate(1500)).toBe('1.5K');
  });

  it('leaves values below 1000 unabbreviated', () => {
    expect(abbreviate(950)).toBe('950');
  });

  it('abbreviates billions', () => {
    expect(abbreviate(2450000000)).toBe('2.45B');
  });

  it('trims trailing zeros (whole thousands)', () => {
    expect(abbreviate(2000)).toBe('2K');
  });

  it('returns the em-dash for null/undefined', () => {
    expect(abbreviate(null)).toBe(EM_DASH);
    expect(abbreviate(undefined)).toBe(EM_DASH);
  });

  it('does not throw on a non-numeric input', () => {
    expect(() => abbreviate('x')).not.toThrow();
    expect(abbreviate('x')).toBe(EM_DASH);
  });
});

// LiteLLM resets a budget at local midnight in ITS timezone, so the instant
// is the day before in UTC east of Greenwich: production's Nov 1 reset in
// Europe/Madrid arrives as 2026-10-31T23:00:00Z.
describe('formatLocalDate — MMM DD, YYYY in the viewer\'s zone', () => {
  it('renders the instant on the viewer\'s calendar, not UTC\'s', () => {
    expect(formatLocalDate('2026-10-31T23:00:00Z', 'Europe/Madrid')).toBe('Nov 01, 2026');
    expect(formatLocalDate('2026-10-31T23:00:00Z', 'UTC')).toBe('Oct 31, 2026');
  });

  it('returns the em-dash for null/undefined/unparseable (never throws)', () => {
    expect(formatLocalDate(null)).toBe(EM_DASH);
    expect(formatLocalDate(undefined)).toBe(EM_DASH);
    expect(formatLocalDate('not-a-date')).toBe(EM_DASH);
  });
});

describe('formatDate — MMM DD, YYYY', () => {
  it('formats an ISO timestamp to MMM DD, YYYY (zero-padded day)', () => {
    expect(formatDate('2026-03-01T10:00:00+00:00')).toBe('Mar 01, 2026');
  });

  it('zero-pads single-digit days', () => {
    expect(formatDate('2026-12-05T00:00:00+00:00')).toBe('Dec 05, 2026');
  });

  it('returns the em-dash for null/undefined', () => {
    expect(formatDate(null)).toBe(EM_DASH);
    expect(formatDate(undefined)).toBe(EM_DASH);
  });

  it('returns the em-dash for an unparseable date (never throws)', () => {
    expect(() => formatDate('not-a-date')).not.toThrow();
    expect(formatDate('not-a-date')).toBe(EM_DASH);
  });
});

describe('maskKey — first4…last4', () => {
  it('masks an sk- key as first4…last4', () => {
    expect(maskKey('sk-abcd1234wxyz')).toBe('sk-a…wxyz');
  });

  it('keeps the REAL prefix (first 4 chars) instead of fabricating sk- (WR-02)', () => {
    // A key-... id keeps its own first 4 chars, never shown as a fake sk-secret.
    expect(maskKey('key-abc123')).toBe('key-…c123');
  });

  it('masks a hash id as first4…last4', () => {
    expect(maskKey('088s12340000004fe6')).toBe('088s…4fe6');
  });

  it('returns the em-dash for null/undefined', () => {
    expect(maskKey(null)).toBe(EM_DASH);
    expect(maskKey(undefined)).toBe(EM_DASH);
  });

  it('returns short (<= 8 char) values verbatim, with no fabricated ellipsis', () => {
    expect(maskKey('')).toBe('');
    expect(maskKey('ab')).toBe('ab');
    expect(maskKey('abcd')).toBe('abcd');
    expect(maskKey('abcd1234')).toBe('abcd1234');
  });

  it('does not throw on a short string', () => {
    expect(() => maskKey('ab')).not.toThrow();
  });
});

describe('formatLocalDateTime / formatLocalTime / formatShortDay / formatCurrencyCompact', () => {
  it('renders the instant on the viewer\'s clock with the day', () => {
    expect(formatLocalDateTime('2026-10-28T07:13:00Z', 'Europe/Madrid')).toBe('Oct 28, 08:13');
    expect(formatLocalTime('2026-10-28T07:13:05Z', 'Europe/Madrid')).toBe('08:13:05');
    expect(formatLocalDateTime(null)).toBe(EM_DASH);
    expect(formatLocalTime('nope')).toBe(EM_DASH);
  });
  it('formats a UTC day short and compacts currency from $1k', () => {
    expect(formatShortDay('2026-10-01')).toBe('Oct 01');
    expect(formatShortDay(null)).toBe(EM_DASH);
    expect(formatCurrencyCompact(12.5)).toBe('$12.50');
    expect(formatCurrencyCompact(1234)).toBe('$1.23K');
  });
});

describe('locale', () => {
  // LOCALE is read once at import, so each case loads a fresh module.
  async function load(locale: string) {
    vi.resetModules();
    vi.stubEnv('VITE_CONSOLE_LOCALE', locale);
    try {
      return await import('./format');
    } finally {
      vi.unstubAllEnvs();
    }
  }

  it('formats European style when the build sets es-ES', async () => {
    // Intl separates the amount from the symbol with a no-break space.
    const f = await load('es-ES');
    expect(f.formatCurrency(1249.5)).toBe('1.249,50\u00a0$');
    expect(f.formatInt(1234)).toBe('1.234'); // four digits are grouped too
    expect(f.formatDecimal(12.345, 1)).toBe('12,3');
    expect(f.formatPercent(0.985)).toBe('98,5\u00a0%');
    expect(f.formatSeconds(1234)).toBe('1,2 s');
    expect(f.formatDate('2026-03-01T10:00:00Z')).toBe('01 mar 2026');
  });

  it('pins tests to en-US and falls back to the browser for an invalid tag', async () => {
    expect((await load('en-US')).formatDecimal(1234.5, 1)).toBe('1,234.5');
    expect((await load('not a locale!')).LOCALE).toBeUndefined();
  });

  it('covers the helpers that replaced ad-hoc toFixed', async () => {
    const f = await load('en-US');
    expect(f.formatSeconds(850)).toBe('850 ms');
    expect(f.formatSeconds(1234, 2)).toBe('1.23 s');
    expect(f.formatDuration(2339)).toBe('2.3 s');
    expect(f.formatDuration(754_000)).toBe('12 min 34 s');
    expect(f.formatDuration(7_184_241)).toBe('1 h 59 min');
    expect(f.formatDuration(null)).toBe('—');
    expect(f.formatUsdSmall(0.0042)).toBe('$0.0042');
    expect(f.formatUsdSmall(12.5)).toBe('$12.50');
    expect(f.formatPercent(null)).toBe('—');
  });
});
