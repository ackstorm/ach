// RangePicker.tsx — the console-wide activity period, in each observability
// page header: a button with the active period that opens a popover with the
// presets and a hand-built Custom calendar (no native date input, no extra
// dependency). One shared store (stores/console-range), so the choice follows
// the user from page to page.
import { useState } from 'react';
import { Popover } from 'radix-ui';
import { CalendarDays } from 'lucide-react';
import { MAX_RANGE_DAYS, PRESETS, rangeDays, toUTCDateString } from '@/lib/stats-presets';
import { formatShortDay } from '@/lib/format';
import { useConsoleDateRange, useConsoleRange, type DayRange } from '@/stores/console-range';
import './reports.css';

const LABELS: Record<string, string> = { '7d': 'Last 7 days', '30d': 'Last 30 days', '90d': 'Last 90 days' };
export const presetLabel = (preset: string) => LABELS[preset] ?? preset;
const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
const WEEKDAYS = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];

/** A UTC month's day cells: nulls for the weekday offset, then 1..days. */
export function buildMonthGrid(year: number, month: number): (number | null)[] {
  const offset = new Date(Date.UTC(year, month, 1)).getUTCDay();
  const days = new Date(Date.UTC(year, month + 1, 0)).getUTCDate();
  return [...Array<null>(offset).fill(null), ...Array.from({ length: days }, (_, i) => i + 1)];
}

/** Why a custom range cannot be applied, or null (the stats endpoint caps it at 366 days). */
export function customRangeError(range: DayRange, today: string): string | null {
  if (range.end > today) return 'The end date cannot be in the future.';
  if (rangeDays(range) > MAX_RANGE_DAYS) return `Choose at most ${MAX_RANGE_DAYS} days.`;
  return null;
}

// Two clicks pick start and end (normalised so start <= end); one click is a one-day range.
function Calendar({ onApply, onCancel }: { onApply: (range: DayRange) => void; onCancel: () => void }) {
  const [view, setView] = useState(() => { const now = new Date(); return { year: now.getUTCFullYear(), month: now.getUTCMonth() }; });
  const [start, setStart] = useState<string | null>(null);
  const [end, setEnd] = useState<string | null>(null);
  const move = (delta: number) => { const d = new Date(Date.UTC(view.year, view.month + delta, 1)); setView({ year: d.getUTCFullYear(), month: d.getUTCMonth() }); };
  const pick = (day: string) => { if (!start || end) { setStart(day); setEnd(null); } else if (day < start) { setEnd(start); setStart(day); } else setEnd(day); };
  const range = start ? { start, end: end ?? start } : null;
  const error = range && customRangeError(range, toUTCDateString(new Date()));
  return <div className="range-cal">
    <div className="range-cal-head"><button type="button" aria-label="Previous month" onClick={() => move(-1)}>‹</button><b>{MONTHS[view.month]} {view.year}</b><button type="button" aria-label="Next month" onClick={() => move(1)}>›</button></div>
    <div className="range-cal-grid">{WEEKDAYS.map((w, i) => <span key={`w${i}`} aria-hidden="true">{w}</span>)}{buildMonthGrid(view.year, view.month).map((n, i) => {
      if (n == null) return <span key={i} />;
      const day = toUTCDateString(new Date(Date.UTC(view.year, view.month, n)));
      const on = day === range?.start || day === range?.end;
      return <button type="button" key={i} aria-pressed={on} className={on ? 'on' : range && day > range.start && day < range.end ? 'in' : ''} onClick={() => pick(day)}>{n}</button>;
    })}</div>
    {error && <p className="range-error" role="alert">{error}</p>}
    <div className="range-cal-actions"><button type="button" className="btn" onClick={onCancel}>Cancel</button><button type="button" className="btn primary" disabled={!range || Boolean(error)} onClick={() => range && onApply(range)}>Apply</button></div>
  </div>;
}

export function RangePicker() {
  const preset = useConsoleRange((state) => state.preset);
  const setPreset = useConsoleRange((state) => state.setPreset);
  const setCustom = useConsoleRange((state) => state.setCustom);
  const range = useConsoleDateRange();
  const [open, setOpen] = useState(false);
  const [calendar, setCalendar] = useState(false);
  const dates = range.start === range.end ? formatShortDay(range.start) : `${formatShortDay(range.start)} – ${formatShortDay(range.end)}`;
  const close = () => { setOpen(false); setCalendar(false); };
  return <Popover.Root open={open} onOpenChange={(next) => next ? setOpen(true) : close()}>
    <Popover.Trigger className="btn range-trigger" title={`${range.start} – ${range.end} UTC`}><CalendarDays aria-hidden="true" />{presetLabel(preset)} · {dates}</Popover.Trigger>
    {/* Not portaled: the popover stays inside .console-ledger and keeps its theme tokens. */}
    <Popover.Content className="range-pop" align="end" sideOffset={6} collisionPadding={16} aria-label="Activity period">
      {calendar ? <Calendar onApply={(custom) => { setCustom(custom); close(); }} onCancel={() => setCalendar(false)} /> :
        <div className="range-presets">{PRESETS.map((p) => <button type="button" key={p} aria-pressed={preset === p} onClick={() => { if (p === 'Custom') setCalendar(true); else { setPreset(p); close(); } }}>{presetLabel(p)}{p === 'Custom' && <span aria-hidden="true">›</span>}</button>)}</div>}
    </Popover.Content>
  </Popover.Root>;
}
