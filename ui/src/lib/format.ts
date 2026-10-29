// format.ts — the console's ONLY display formatters. Components never call
// toFixed()/toLocaleString() themselves: every number, amount and date goes
// through here so one setting changes all of them.
//
// LOCALE is VITE_CONSOLE_LOCALE when the build sets it (e.g. "es-ES" for
// everyone), otherwise the viewer's browser locale. Examples below are en-US;
// es-ES gives "1.249,50 $", "1.000.000", "2,45 M" and "1 mar 2026".
//
//   formatCurrency(1249.5)                       -> "$1,249.50"
//   formatInt(1000000)                           -> "1,000,000"
//   formatDecimal(12.345, 1)                     -> "12.3"
//   formatPercent(0.985)                         -> "98.5%"
//   abbreviate(2450000)                          -> "2.45M"
//   formatDate("2026-03-01T10:00:00+00:00")      -> "Mar 01, 2026"
//   maskKey("sk-abcd1234wxyz")                    -> "sk-a…wxyz"  (first4…last4)
//
// EVERY formatter returns the em-dash "—" (U+2014) for null/undefined (and any
// otherwise-unformattable input) and NEVER throws — null cells render as a
// neutral dash, not a crash.

const EM_DASH = '—'; // — (U+2014), the UI-SPEC null/empty placeholder
const ELLIPSIS = '…'; // … (U+2026), the mask ellipsis

// Coerce to a finite number, or null if the input is null/undefined/NaN.
function asNumber(n: number | string | null | undefined): number | null {
  if (n === null || n === undefined) return null;
  const num = typeof n === 'number' ? n : Number(n);
  return Number.isFinite(num) ? num : null;
}

// undefined = the browser's locale. An invalid build value falls back to it
// rather than breaking every page.
export const LOCALE: string | undefined = (() => {
  const want = import.meta.env.VITE_CONSOLE_LOCALE as string | undefined;
  if (!want) return undefined;
  try {
    return Intl.getCanonicalLocales(want)[0];
  } catch {
    return undefined;
  }
})();

// 'always' groups four-digit numbers too (es-ES would otherwise print 1234).
// ES2023 Intl typings know it; this project targets ES2022 lib.
type NumberOpts = Omit<Intl.NumberFormatOptions, 'useGrouping'> & { useGrouping?: boolean | 'always' | 'auto' | 'min2' };
const numberFormat = (opts: NumberOpts) => new Intl.NumberFormat(LOCALE, opts as Intl.NumberFormatOptions);
const _currencyFmt = numberFormat({
  style: 'currency',
  currency: 'USD',
  currencyDisplay: 'narrowSymbol',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
  useGrouping: 'always',
});

const _intFmt = numberFormat({
  maximumFractionDigits: 0,
  useGrouping: 'always',
});

const _decimalFmts = new Map<string, Intl.NumberFormat>();
function decimalFmt(min: number, max: number): Intl.NumberFormat {
  const key = `${min}:${max}`;
  let f = _decimalFmts.get(key);
  if (!f) {
    f = numberFormat({ minimumFractionDigits: min, maximumFractionDigits: max, useGrouping: 'always' });
    _decimalFmts.set(key, f);
  }
  return f;
}

// formatDecimal(12.345, 1) -> "12.3": exactly `digits` decimals.
export function formatDecimal(n: number | string | null | undefined, digits: number): string {
  const num = asNumber(n);
  if (num === null) return EM_DASH;
  return decimalFmt(digits, digits).format(num);
}

// formatPercent(0.985) -> "98.5%"; a ratio (0..1), `digits` decimals.
export function formatPercent(ratio: number | string | null | undefined, digits = 1): string {
  const num = asNumber(ratio);
  if (num === null) return EM_DASH;
  return new Intl.NumberFormat(LOCALE, { style: 'percent', minimumFractionDigits: digits, maximumFractionDigits: digits }).format(num);
}

// formatSeconds(1234) -> "1.2 s", formatSeconds(850) -> "850 ms": a duration
// in milliseconds, seconds with `digits` decimals from 1 s up.
export function formatSeconds(ms: number | string | null | undefined, digits = 1): string {
  const num = asNumber(ms);
  if (num === null) return EM_DASH;
  return Math.abs(num) >= 1000 ? `${formatDecimal(num / 1000, digits)} s` : `${formatInt(num)} ms`;
}

// formatDuration(7184241) -> "1 h 59 min", 754000 -> "12 min 34 s"; under a
// minute it is formatSeconds. For session-length spans.
export function formatDuration(ms: number | string | null | undefined): string {
  const num = asNumber(ms);
  if (num === null) return EM_DASH;
  if (Math.abs(num) < 60_000) return formatSeconds(num);
  const minutes = Math.floor(num / 60_000);
  return minutes < 60 ? `${formatInt(minutes)} min ${formatInt(Math.floor(num / 1000) % 60)} s` : `${formatInt(Math.floor(minutes / 60))} h ${formatInt(minutes % 60)} min`;
}

// formatUsdSmall(0.0042) -> "$0.0042": four decimals below a cent, so tiny
// per-request costs do not all read "$0.00"; formatCurrency from a cent up.
export function formatUsdSmall(n: number | string | null | undefined): string {
  const num = asNumber(n);
  if (num === null) return EM_DASH;
  if (Math.abs(num) >= 0.01 || num === 0) return _currencyFmt.format(num);
  return new Intl.NumberFormat(LOCALE, { style: 'currency', currency: 'USD', currencyDisplay: 'narrowSymbol', minimumFractionDigits: 4, maximumFractionDigits: 4 }).format(num);
}

// formatCurrency(n) -> "$1,249.50"; null/undefined/non-numeric -> "—".
export function formatCurrency(n: number | string | null | undefined): string {
  const num = asNumber(n);
  if (num === null) return EM_DASH;
  return _currencyFmt.format(num);
}

// formatInt(n) -> "1,000,000"; null/undefined/non-numeric -> "—".
export function formatInt(n: number | string | null | undefined): string {
  const num = asNumber(n);
  if (num === null) return EM_DASH;
  return _intFmt.format(num);
}

// abbreviate(n) -> "2.45M" / "1.5K" / "950": compact notation, up to 2
// decimals, trailing zeros trimmed (es-ES: "2,45 M", "1,5 mil").
const _compactFmt = new Intl.NumberFormat(LOCALE, { notation: 'compact', maximumFractionDigits: 2 });
export function abbreviate(n: number | string | null | undefined): string {
  const num = asNumber(n);
  if (num === null) return EM_DASH;
  return _compactFmt.format(num);
}

// formatTokens(1041, 388) -> "1,429 (1,041+388)"; short -> "1.43K".
// "—" when either side is unknown (a partial sum would understate usage).
export function formatTokens(
  input: number | null | undefined,
  output: number | null | undefined,
  short = false,
): string {
  if (input == null || output == null) return EM_DASH;
  return short ? abbreviate(input + output) : `${formatInt(input + output)} (${formatInt(input)}+${formatInt(output)})`;
}

// formatPricePerMillion(costPerToken) -> "$0.15" — the per-1M-token price from a
// LiteLLM per-token cost (×1e6), as USD currency. null/undefined/non-numeric ->
// "—". A genuine 0 cost renders "$0.00" (free), NOT a dash.
export function formatPricePerMillion(
  costPerToken: number | string | null | undefined,
): string {
  const num = asNumber(costPerToken);
  if (num === null) return EM_DASH;
  return _currencyFmt.format(num * 1e6);
}

// formatDate(iso) -> "Mar 01, 2026" (short month, zero-padded day, UTC);
// null/undefined/unparseable -> "—". Never throws.
export function formatDate(iso: string | null | undefined): string {
  return formatLocalDate(iso, 'UTC');
}

// formatLocalDate(iso) -> "Nov 01, 2026" like formatDate, but on the
// viewer's calendar (timeZone defaults to the browser's). For instants that
// are a local midnight somewhere else — a budget reset at 00:00 Europe/Madrid
// is 23:00Z the day before, which formatDate would show as the wrong day.
const _LOCAL_DATE_OPTS: Intl.DateTimeFormatOptions = { month: 'short', day: '2-digit', year: 'numeric' };
export function formatLocalDate(iso: string | null | undefined, timeZone?: string): string {
  if (iso === null || iso === undefined) return EM_DASH;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return EM_DASH;
  return d.toLocaleDateString(LOCALE, { ..._LOCAL_DATE_OPTS, timeZone });
}

// formatLocalDateTime(iso) -> "Oct 28, 08:13" and formatLocalTime(iso) ->
// "08:13:05" on the viewer's clock (24h); same null/invalid -> "—" contract.
export function formatLocalDateTime(iso: string | null | undefined, timeZone?: string): string {
  const d = iso == null ? null : new Date(iso);
  if (!d || Number.isNaN(d.getTime())) return EM_DASH;
  return d.toLocaleString(LOCALE, { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone });
}
export function formatLocalTime(iso: string | null | undefined, timeZone?: string): string {
  const d = iso == null ? null : new Date(iso);
  if (!d || Number.isNaN(d.getTime())) return EM_DASH;
  return d.toLocaleTimeString(LOCALE, { hourCycle: 'h23', timeZone });
}

// formatShortDay("2026-10-01") -> "Oct 01": a YYYY-MM-DD UTC day (chart axes,
// the range picker), never shifted by the viewer's zone.
export function formatShortDay(day: string | null | undefined): string {
  if (!day) return EM_DASH;
  const d = new Date(`${day}T00:00:00Z`);
  return Number.isNaN(d.getTime()) ? EM_DASH : d.toLocaleDateString(LOCALE, { month: 'short', day: '2-digit', timeZone: 'UTC' });
}

// formatCurrencyCompact(1234) -> "$1.23K"; below $1k it is formatCurrency.
const _currencyCompactFmt = new Intl.NumberFormat(LOCALE, { style: 'currency', currency: 'USD', currencyDisplay: 'narrowSymbol', notation: 'compact', maximumFractionDigits: 2 });
export function formatCurrencyCompact(n: number | null | undefined): string {
  const num = asNumber(n);
  if (num === null) return EM_DASH;
  return Math.abs(num) >= 1000 ? _currencyCompactFmt.format(num) : formatCurrency(num);
}

// maskKey(s) -> "first4…last4" — the value's first 4 and last 4 characters
// around an ellipsis (e.g. "sk-abcd1234wxyz" -> "sk-a…wxyz",
// "key-abc123" -> "key-…c123", a "088s…4fe6"-style hash id). null/undefined ->
// "—"; a value too short to mask meaningfully (<= 8 chars, where first4+last4
// would meet/overlap) is returned verbatim rather than fabricating an ellipsis.
// Never throws.
export function maskKey(s: string | null | undefined): string {
  if (s === null || s === undefined) return EM_DASH;
  const str = String(s);
  if (str.length <= 8) return str; // too short to mask — first4…last4 would overlap
  return `${str.slice(0, 4)}${ELLIPSIS}${str.slice(-4)}`;
}
