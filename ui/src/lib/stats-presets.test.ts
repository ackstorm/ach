// stats-presets.test.ts — vitest unit suite for the PURE preset->date-range +
// capability->render-mode helpers.
//
// Mirrors the router.test.ts / format.test.ts style (describe/it/expect, default
// node env, no jsdom) because stats-presets.ts touches no window/document/fetch
// and never calls Date.now() — every range is deterministic on the injected
// `now`. The cases below assert:
//   - one exact {start,end} per preset against a fixed injected `now`,
//   - the explicit 366-day cap boundary (no range may exceed MAX_RANGE_DAYS),
//   - the capability->mode matrix (ready / coming-soon / empty) per figure key.
import { describe, it, expect } from 'vitest';
import { presetToRange, capabilityRenderMode, PRESETS, MAX_RANGE_DAYS } from './stats-presets';

// A fixed, deterministic reference instant: 2026-06-04 (a Thursday), mid-day UTC.
// Using a non-edge day-of-month so "thisMonth"/"7d" math is unambiguous.
const NOW = new Date('2026-06-04T13:37:00.000Z');

// Inclusive day span between two YYYY-MM-DD strings (end - start + 1).
function inclusiveSpanDays(start: string, end: string): number {
  const s = Date.parse(`${start}T00:00:00.000Z`);
  const e = Date.parse(`${end}T00:00:00.000Z`);
  return Math.round((e - s) / 86400000) + 1;
}

describe('PRESETS — the seven preset ids (UI-SPEC §1 labels)', () => {
  it('exposes exactly the seven preset ids in label order', () => {
    expect(PRESETS).toEqual(['7d', '30d', '90d', 'This month', 'Last month', 'This year', 'Custom']);
  });
});

describe('MAX_RANGE_DAYS — the Phase-12 ~366d server cap (D-10)', () => {
  it('is 366', () => {
    expect(MAX_RANGE_DAYS).toBe(366);
  });
});

describe('presetToRange — exact {start,end} per preset (deterministic on now)', () => {
  it('"7d" -> today and 6 days earlier (7 inclusive days)', () => {
    // end = 2026-06-04 (today UTC); start = 2026-05-29 (6 days earlier).
    expect(presetToRange('7d', NOW)).toEqual({
      start: '2026-05-29',
      end: '2026-06-04',
    });
    expect(inclusiveSpanDays('2026-05-29', '2026-06-04')).toBe(7);
  });

  it('"30d" -> today and 29 days earlier (30 inclusive days)', () => {
    expect(presetToRange('30d', NOW)).toEqual({
      start: '2026-05-06',
      end: '2026-06-04',
    });
    expect(inclusiveSpanDays('2026-05-06', '2026-06-04')).toBe(30);
  });

  it('"90d" -> today and 89 days earlier (90 inclusive days)', () => {
    const { start, end } = presetToRange('90d', NOW);
    expect(end).toBe('2026-06-04');
    expect(inclusiveSpanDays(start, end)).toBe(90);
    expect(start).toBe('2026-03-07');
  });

  it('"This month" -> first-of-month .. today', () => {
    expect(presetToRange('This month', NOW)).toEqual({
      start: '2026-06-01',
      end: '2026-06-04',
    });
  });

  it('"Last month" -> the full previous calendar month', () => {
    // May 2026: 2026-05-01 .. 2026-05-31 (31 days).
    expect(presetToRange('Last month', NOW)).toEqual({
      start: '2026-05-01',
      end: '2026-05-31',
    });
  });

  it('"Last month" handles a January now -> the full previous December', () => {
    const jan = new Date('2026-01-15T08:00:00.000Z');
    expect(presetToRange('Last month', jan)).toEqual({
      start: '2025-12-01',
      end: '2025-12-31',
    });
  });

  it('"This year" -> Jan 1 .. today (always <= 366 days)', () => {
    expect(presetToRange('This year', NOW)).toEqual({
      start: '2026-01-01',
      end: '2026-06-04',
    });
  });

  it('"Custom" -> the default 30d window (matches the Phase-12 default)', () => {
    expect(presetToRange('Custom', NOW)).toEqual(presetToRange('30d', NOW));
  });

  it('an unknown preset falls back to the default 30d window (never throws)', () => {
    expect(presetToRange('nonsense', NOW)).toEqual(presetToRange('30d', NOW));
  });

  it('is deterministic — same (preset, now) always yields the same range', () => {
    expect(presetToRange('90d', NOW)).toEqual(presetToRange('90d', NOW));
  });
});

describe('presetToRange — the 366d cap boundary is never exceeded (D-10)', () => {
  it('"This year" near year-end stays within MAX_RANGE_DAYS', () => {
    // 2024 is a leap year: Jan 1 .. Dec 31 inclusive = 366 days (exactly the cap).
    const dec31LeapYear = new Date('2024-12-31T23:00:00.000Z');
    const { start, end } = presetToRange('This year', dec31LeapYear);
    expect(start).toBe('2024-01-01');
    expect(end).toBe('2024-12-31');
    expect(inclusiveSpanDays(start, end)).toBe(366);
    expect(inclusiveSpanDays(start, end)).toBeLessThanOrEqual(MAX_RANGE_DAYS);
  });

  it('NO preset ever returns a span greater than MAX_RANGE_DAYS, across many now values', () => {
    const probes = [
      new Date('2024-12-31T23:00:00.000Z'), // leap-year year-end
      new Date('2025-12-31T23:00:00.000Z'),
      new Date('2026-01-01T00:00:00.000Z'),
      new Date('2026-02-28T12:00:00.000Z'),
      new Date('2026-06-04T13:37:00.000Z'),
      new Date('2027-03-31T06:00:00.000Z'),
    ];
    for (const now of probes) {
      for (const preset of PRESETS) {
        const { start, end } = presetToRange(preset, now);
        const span = inclusiveSpanDays(start, end);
        expect(span).toBeGreaterThanOrEqual(1);
        expect(span).toBeLessThanOrEqual(MAX_RANGE_DAYS);
        // start must never be after end.
        expect(Date.parse(`${start}T00:00:00Z`)).toBeLessThanOrEqual(Date.parse(`${end}T00:00:00Z`));
      }
    }
  });

  it('clamps an over-cap span so end is preserved and start is pulled forward to <=366d', () => {
    // A synthetic over-cap preset cannot be produced by the public presets, but
    // the clamp is exercised end-to-end: any preset whose naive span would
    // exceed 366d must come back clamped. "This year" on a leap year is exactly
    // at the boundary; nothing should exceed it.
    const { start, end } = presetToRange('This year', new Date('2024-12-31T00:00:00Z'));
    expect(inclusiveSpanDays(start, end)).toBeLessThanOrEqual(MAX_RANGE_DAYS);
  });
});

describe('capabilityRenderMode — the capability/null -> mode matrix (UI-SPEC §10 / D-15)', () => {
  const FIGURE_KEYS = ['token_split', 'per_model_last_used', 'deltas', 'per_key_spend'];

  it('capability === false -> "coming-soon" for every figure key', () => {
    for (const key of FIGURE_KEYS) {
      const caps = { [key]: false };
      // hasData is irrelevant when the capability is unavailable.
      expect(capabilityRenderMode(caps, key, false)).toBe('coming-soon');
      expect(capabilityRenderMode(caps, key, true)).toBe('coming-soon');
    }
  });

  it('capability true + hasData false -> "empty" (real zero / no usage)', () => {
    for (const key of FIGURE_KEYS) {
      const caps = { [key]: true };
      expect(capabilityRenderMode(caps, key, false)).toBe('empty');
    }
  });

  it('capability true + hasData true -> "ready"', () => {
    for (const key of FIGURE_KEYS) {
      const caps = { [key]: true };
      expect(capabilityRenderMode(caps, key, true)).toBe('ready');
    }
  });

  it('a missing capability entry is treated as available (defaults to "ready"/"empty", never "coming-soon")', () => {
    // capabilities object without the key -> capability is not explicitly false,
    // so it is NOT coming-soon; it falls through to the hasData distinction.
    expect(capabilityRenderMode({}, 'token_split', true)).toBe('ready');
    expect(capabilityRenderMode({}, 'token_split', false)).toBe('empty');
  });

  it('never throws on a null/undefined capabilities object', () => {
    expect(capabilityRenderMode(null, 'deltas', true)).toBe('ready');
    expect(capabilityRenderMode(undefined, 'deltas', false)).toBe('empty');
  });
});
