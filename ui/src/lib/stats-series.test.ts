// stats-series.test.ts — vitest unit suite for the PURE `seriesToRecharts`
// transform in stats-series.ts. Runs in the default node env (no DOM, no chart
// lib) because the transform is pure — mirrors format.test.js / charts.test.js.
//
// Contract (ported from src/ui/charts.test.js, adapted to ROW OBJECTS):
//   - series[] is the Phase-12 [{date, spend, requests}] contract.
//   - seriesToRecharts(series) -> [{date, spend, requests}, ...] in input order.
//   - seriesToRecharts([] | null | undefined) -> [] (the "no data" shape) so the
//     chart renders its empty state instead of an empty grid.
//   - a REAL zero stays 0 (never dropped, never null) for BOTH spend and requests.
//   - PURE: never mutates the input.
import { describe, it, expect } from 'vitest';
import { seriesHasActivity, seriesToRecharts } from './stats-series';
import type { StatsSeriesPoint } from './api-types';

// A small, deterministic fixture in the locked contract shape (ported from
// charts.test.js). Typed as StatsSeriesPoint[] so the literals satisfy the
// contract (date string, spend/requests number) with no casts.
const SERIES: StatsSeriesPoint[] = [
  { date: '2026-03-01', spend: 1.5, requests: 10, tokens: 1234, input_tokens: 1000, output_tokens: 234, failed: 2 },
  { date: '2026-03-02', spend: 2.25, requests: 20, tokens: 5678, input_tokens: 4000, output_tokens: 1678, failed: 0 },
  { date: '2026-03-03', spend: 0, requests: 0, tokens: 0, input_tokens: 0, output_tokens: 0, failed: 0 },
];

describe('seriesToRecharts — row-object mapping', () => {
  it('maps to row objects aligned and IN ORDER', () => {
    expect(seriesToRecharts(SERIES)).toEqual([
      { date: '2026-03-01', spend: 1.5, requests: 10, tokens: 1234, inputTokens: 1000, outputTokens: 234, failed: 2, success: 8 },
      { date: '2026-03-02', spend: 2.25, requests: 20, tokens: 5678, inputTokens: 4000, outputTokens: 1678, failed: 0, success: 20 },
      { date: '2026-03-03', spend: 0, requests: 0, tokens: 0, inputTokens: 0, outputTokens: 0, failed: 0, success: 0 },
    ]);
  });

  it('keeps a real zero as 0 for spend/requests/tokens (not dropped, not null)', () => {
    const rows = seriesToRecharts(SERIES);
    expect(rows[2].spend).toBe(0);
    expect(rows[2].requests).toBe(0);
    expect(rows[2].tokens).toBe(0);
  });

  it('clamps failed to requests and derives success = requests - failed', () => {
    const rows = seriesToRecharts([
      { date: '2026-03-04', spend: 0, requests: 5, tokens: 0, input_tokens: 0, output_tokens: 0, failed: 99 },
    ]);
    expect(rows[0].failed).toBe(5); // clamped to requests
    expect(rows[0].success).toBe(0); // never negative
  });
});

describe('seriesToRecharts — empty / no-data shape', () => {
  it('returns [] for an empty series', () => {
    expect(seriesToRecharts([])).toEqual([]);
  });

  it('returns [] for a null/undefined series', () => {
    expect(seriesToRecharts(null)).toEqual([]);
    expect(seriesToRecharts(undefined)).toEqual([]);
  });
});

describe('seriesHasActivity', () => {
  it('is false for empty / null / undefined', () => {
    expect(seriesHasActivity([])).toBe(false);
    expect(seriesHasActivity(null)).toBe(false);
    expect(seriesHasActivity(undefined)).toBe(false);
  });

  it('is false for a zero-filled (inactive) window', () => {
    // The server zero-fills an empty range — non-empty but all-zero requests.
    expect(
      seriesHasActivity([
        { date: '2026-03-01', spend: 0, requests: 0, tokens: 0, input_tokens: 0, output_tokens: 0, failed: 0 },
        { date: '2026-03-02', spend: 0, requests: 0, tokens: 0, input_tokens: 0, output_tokens: 0, failed: 0 },
      ]),
    ).toBe(false);
  });

  it('is true when any day has requests', () => {
    expect(
      seriesHasActivity([
        { date: '2026-03-01', spend: 0, requests: 0, tokens: 0, input_tokens: 0, output_tokens: 0, failed: 0 },
        { date: '2026-03-02', spend: 0, requests: 3, tokens: 0, input_tokens: 0, output_tokens: 0, failed: 0 },
      ]),
    ).toBe(true);
  });
});

describe('seriesToRecharts — purity', () => {
  it('does not mutate the input series', () => {
    const input: StatsSeriesPoint[] = [
      { date: '2026-03-01', spend: 1, requests: 2, tokens: 3, input_tokens: 2, output_tokens: 1, failed: 1 },
    ];
    const snapshot = JSON.stringify(input);
    seriesToRecharts(input);
    expect(JSON.stringify(input)).toBe(snapshot);
  });
});
