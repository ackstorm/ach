// relative-time.ts — relative "time ago" for the KEYS "Last used" column.
// Pure: `now` is injectable so it is unit-testable without a clock.
const EM_DASH = '—';
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

export function relativeTime(
  iso: string | null | undefined,
  now: Date = new Date(),
): string {
  if (iso === null || iso === undefined) return 'Never used';
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return EM_DASH;
  const delta = Math.max(0, now.getTime() - then);
  if (delta < MIN) return 'just now';
  if (delta < HOUR) return `${Math.floor(delta / MIN)}m ago`;
  if (delta < DAY) return `${Math.floor(delta / HOUR)}h ago`;
  const days = Math.floor(delta / DAY);
  return days === 1 ? '1 day ago' : `${days} days ago`;
}

// A key is "stale" when never used, or unused for >= `days` (default 30).
export function isStale(
  iso: string | null | undefined,
  now: Date = new Date(),
  days = 30,
): boolean {
  if (iso === null || iso === undefined) return true;
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return false;
  return now.getTime() - then >= days * DAY;
}
