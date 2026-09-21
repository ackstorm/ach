// stats-presets.ts — PURE preset->date-range + capability->render-mode helpers
// for the alitellm-auth Usage & Spend page (#/stats).
//
// Ported verbatim (behavior + semantics) from src/ui/stats-presets.js. This
// module is the ONE place all stats date arithmetic lives (UI-SPEC §"Left to the
// planner": keep date math out of the components). It mirrors the router.ts /
// format.ts discipline:
//   - PURE: no DOM, no window/document/fetch, no Date.now() — every range is
//     deterministic on the injected `now` so it unit-tests like resolveRoute.
//   - NEVER throws: an unknown preset / null capabilities object falls back to
//     a safe default rather than crashing the page.
//
// Two exports drive the page:
//   presetToRange(preset, now)       -> {start, end} YYYY-MM-DD (UTC, tz-stable)
//   capabilityRenderMode(caps, k, h) -> "ready" | "coming-soon" | "empty"
//
// Why the clamp matters (D-10 / Phase-12): the server 422s any range wider than
// ~366 inclusive days. presetToRange NEVER emits a span > MAX_RANGE_DAYS, so the
// page can never submit a preset the server would reject (defense in depth — the
// server stays the authority and re-validates).

// The seven preset ids, in UI-SPEC §1 label order (these strings ARE the labels).
export const PRESETS = ['7d', '30d', '90d', 'This month', 'Last month', 'This year', 'Custom'] as const;

// The Phase-12 ~366d server cap (D-10). No emitted range may exceed this many
// inclusive days. 366 covers a full leap year (Jan 1 .. Dec 31).
export const MAX_RANGE_DAYS = 366;

const MS_PER_DAY = 86400000;

// Format a Date's UTC parts as "YYYY-MM-DD" (tz-stable, mirrors format.ts
// which reads getUTC* so the literal is identical regardless of host timezone).
function toUTCDateString(d: Date): string {
  const year = d.getUTCFullYear();
  const month = String(d.getUTCMonth() + 1).padStart(2, '0');
  const day = String(d.getUTCDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

// Truncate any instant to UTC midnight of that calendar day.
function utcMidnight(d: Date): Date {
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()));
}

// Inclusive day span between two UTC-midnight Dates (end - start + 1).
function inclusiveSpan(startDate: Date, endDate: Date): number {
  return Math.round((endDate.getTime() - startDate.getTime()) / MS_PER_DAY) + 1;
}

// Clamp a {startDate, endDate} pair (UTC-midnight Dates) so its inclusive span
// never exceeds MAX_RANGE_DAYS. The end is the anchor we preserve (the user
// always wants "up to today / period end"); an over-cap span pulls `start`
// forward to exactly MAX_RANGE_DAYS inclusive days. Used by EVERY branch so no
// preset can ever produce a 422-triggering span.
function clampRange(startDate: Date, endDate: Date): { start: string; end: string } {
  if (inclusiveSpan(startDate, endDate) > MAX_RANGE_DAYS) {
    const clampedStart = new Date(endDate.getTime() - (MAX_RANGE_DAYS - 1) * MS_PER_DAY);
    return { start: toUTCDateString(clampedStart), end: toUTCDateString(endDate) };
  }
  return { start: toUTCDateString(startDate), end: toUTCDateString(endDate) };
}

// A rolling N-inclusive-day window ending today: start = today - (n-1) days.
function rollingDays(n: number, today: Date): { start: string; end: string } {
  const start = new Date(today.getTime() - (n - 1) * MS_PER_DAY);
  return clampRange(start, today);
}

// presetToRange(preset, now) -> {start, end} as YYYY-MM-DD UTC strings.
//
// Deterministic on `now` (no Date.now() inside). Unknown presets and "Custom"
// both resolve to the default 30d window (the Phase-12 default), so the function
// never throws and always returns a server-safe range.
export function presetToRange(preset: string, now: Date): { start: string; end: string } {
  const today = utcMidnight(now);

  switch (preset) {
    case '7d':
      return rollingDays(7, today);
    case '30d':
      return rollingDays(30, today);
    case '90d':
      return rollingDays(90, today);
    case 'This month': {
      const start = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1));
      return clampRange(start, today);
    }
    case 'Last month': {
      // First day of the previous calendar month .. last day of that month.
      const firstOfThisMonth = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1));
      const lastOfPrevMonth = new Date(firstOfThisMonth.getTime() - MS_PER_DAY);
      const firstOfPrevMonth = new Date(
        Date.UTC(lastOfPrevMonth.getUTCFullYear(), lastOfPrevMonth.getUTCMonth(), 1),
      );
      return clampRange(firstOfPrevMonth, lastOfPrevMonth);
    }
    case 'This year': {
      const start = new Date(Date.UTC(today.getUTCFullYear(), 0, 1));
      return clampRange(start, today);
    }
    case 'Custom':
      // Custom is not a fixed range — open with the Phase-12 default (30d).
      return rollingDays(30, today);
    default:
      // Unknown preset -> safe default; never throws.
      return rollingDays(30, today);
  }
}

// The render mode for a single figure/panel, derived from the contract's
// `capabilities` map plus whether real data exists (UI-SPEC §10 / D-15).
//
//   capabilities[figureKey] === false        -> "coming-soon" (figure unavailable)
//   capability available  + hasData === false -> "empty"       (real zero / no usage)
//   capability available  + hasData === true  -> "ready"
//
// A missing capability entry is treated as AVAILABLE (not explicitly false), so
// it falls through to the hasData distinction — only an explicit `false` yields
// "coming-soon". Never throws on a null/undefined capabilities object.
export function capabilityRenderMode(
  capabilities: Record<string, boolean> | null | undefined,
  figureKey: string,
  hasData: boolean,
): 'ready' | 'coming-soon' | 'empty' {
  const caps = capabilities || {};
  if (caps[figureKey] === false) {
    return 'coming-soon';
  }
  return hasData ? 'ready' : 'empty';
}
