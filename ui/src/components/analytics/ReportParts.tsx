// ReportParts.tsx — shared building blocks for the observability report pages.
import { useState, type ReactNode } from 'react';
import type { StatsSeriesPoint } from '@/lib/api-types';
import { formatCurrency, formatCurrencyCompact, formatShortDay, formatInt, formatPercent } from '@/lib/format';
import { ChartFrame } from '@/components/charts/ChartFrame';
import { RangePicker } from './RangePicker';
import './reports.css';
import { StatusPill } from '@/components/ui/StatusPill';
import { Pager } from '@/components/ui/data-table';
// Every report page header carries the console-wide period picker.
export function ReportHead({ title, children, preview }: { title: string; children: ReactNode; preview?: boolean }) { return <div className="head"><div><h1>{title}{preview && <StatusPill tone="info">Preview</StatusPill>}</h1><p>{children}</p></div><div className="actions"><RangePicker /></div></div>; }
// Client pages for a non-table list (Bars); tables page through DataTable.
// The pager is always the panel's last row, bottom-right; `footer` (e.g. a
// hidden-count note) shares that row on the left.
export function Paged<T>({ rows, size = 10, footer, children }: { rows: T[]; size?: number; footer?: ReactNode; children: (page: T[]) => ReactNode }) {
  const [page, setPage] = useState(0);
  const last = Math.max(0, Math.ceil(rows.length / size) - 1);
  const current = Math.min(page, last);
  const start = current * size;
  const shown = rows.slice(start, start + size);
  const pager = rows.length > size && <Pager start={start} count={shown.length} total={rows.length} onPrev={current > 0 ? () => setPage(current - 1) : undefined} onNext={current < last ? () => setPage(current + 1) : undefined} />;
  return <>{children(shown)}{(footer || pager) && <div className="report-foot">{footer}{pager}</div>}</>;
}
// The period lives in the header picker; this bar only carries a page's own filter.
export function ReportFilters({ label, options = [], value = '', onChange }: { label: 'Client' | 'Model'; options?: string[]; value?: string; onChange?: (value: string) => void }) {
  if (!options.length) return null;
  return <div className="panel filtersbar report-filters"><label className="filter"><span>{label}</span><select aria-label={`${label} filter`} value={value} onChange={(event) => onChange?.(event.target.value)}><option value="">All {label.toLowerCase()}s</option>{options.map((option) => <option key={option}>{option}</option>)}</select></label></div>;
}
export function Metric({ label, value = '—', children }: { label: string; value?: ReactNode; children: ReactNode }) { return <div className="panel metric"><span className="label">{label}</span><div className="value">{value}</div><p>{children}</p></div>; }
export function Panel({ title, subtitle, action, children, className = '' }: { title: string; subtitle?: string; action?: ReactNode; children: ReactNode; className?: string }) { return <section className={`panel ${className}`}><div className="panel-title"><h2>{title}</h2>{subtitle && <p>{subtitle}</p>}{action}</div>{children}</section>; }
// One pattern for every bar list: right figure = `value · share%`, the share of
// `total` (default: the rows' sum; pass the whole list's sum when paged or top-N).
export function Bars({ rows, color = 'green', total, share = true }: { rows: { name: string; value: number; display?: string; title?: string }[]; color?: string; total?: number; share?: boolean }) { const max = Math.max(1, ...rows.map((r) => r.value)); const sum = total ?? rows.reduce((s, r) => s + r.value, 0); return <div className="hbars">{rows.map((row, i) => <div className="hrow" key={`${i}:${row.name}`}><span title={row.title ?? row.name}>{row.name}</span><span className="track"><i style={{ width: `${row.value / max * 100}%`, background: `var(--${color})` }} /></span><span className="n">{`${row.display ?? formatInt(row.value)}${share ? ` · ${formatPercent(sum ? row.value / sum : null)}` : ''}`}</span></div>)}{!rows.length && <p>No observations in this window.</p>}</div>; }
// A small segmented control (the .tabs style) for switching a panel's metric.
export function Segmented<T extends string>({ label, options, value, onChange }: { label: string; options: readonly T[]; value: T; onChange: (value: T) => void }) { return <div className="tabs segmented" role="group" aria-label={label}>{options.map((option) => <button type="button" key={option} aria-pressed={option === value} className={option === value ? 'on' : ''} onClick={() => onChange(option)}>{option}</button>)}</div>; }
// In-page loading: the page shell renders at once and each pending body is a
// skeleton (LoadingCard is the session bootstrap only).
export const Skel = ({ h = 12, w = '100%' }: { h?: number; w?: string }) => <span className="skel" style={{ height: h, width: w }} />;
export function SkelRows({ n = 5 }: { n?: number }) { return <div className="skel-body" role="status" aria-label="Loading">{Array.from({ length: n }, (_, i) => <Skel key={i} h={14} w={`${92 - i * 9}%`} />)}</div>; }
export function SkelChart() { return <div className="skel-body" role="status" aria-label="Loading"><Skel h={180} /></div>; }
export function SkelKpis({ n = 3, className = 'metric-grid', card = 'metric' }: { n?: number; className?: string; card?: string }) { return <div className={className}>{Array.from({ length: n }, (_, i) => <div key={i} className={`panel ${card}`} aria-hidden="true"><Skel h={9} w="45%" /><Skel h={22} w="55%" /><Skel h={9} w="80%" /></div>)}</div>; }
export function SkelPanel({ title, chart = false, className = 'report-gap' }: { title: string; chart?: boolean; className?: string }) { return <Panel title={title} className={className}>{chart ? <SkelChart /> : <SkelRows />}</Panel>; }
export function Unavailable({ children }: { children: ReactNode }) { return <p className="report-unavailable">{children}</p>; }
// A table and its donut, hover linked both ways: a row (data-row-id, i.e. the
// DataTable's getRowId) or a slice with the same id lights both, and the
// centre shows that slice's name, value and share. Slice colour comes from
// its class (--wc): .wc-s-<n> by rank or a fixed .wc-c-* category.
export type DonutSlice = { id: string; name: string; value: number; cls: string };
export const rankClass = (i: number) => `wc-s-${Math.min(i, 7)}`;
export function Swatch({ cls }: { cls: string }) { return <i className={`wc-swatch ${cls}`} />; }
export function TableDonut({ label, slices, format, noun, children }: { label: string; slices: DonutSlice[]; format: (n: number) => string; noun: string; children: (hover: string | null) => ReactNode }) {
  const [hover, setHover] = useState<string | null>(null);
  const total = slices.reduce((sum, s) => sum + s.value, 0);
  const share = (v: number) => total ? formatPercent(v / total) : '—';
  const on = slices.find((s) => s.id === hover);
  const C = 2 * Math.PI * 48;
  const gap = slices.filter((s) => s.value > 0).length > 1 ? 1.5 : 0;
  let at = 0;
  return <div className="wc-grid">
    <div onMouseOver={(e) => setHover((e.target as HTMLElement).closest<HTMLElement>('tbody tr')?.dataset.rowId ?? null)} onMouseLeave={() => setHover(null)}>{children(hover)}</div>
    <div className="wc-side"><svg className={`wc-svg${on ? ' hovering' : ''}`} onMouseLeave={() => setHover(null)} viewBox="0 0 120 120" role="img" aria-label={`${label}: ${format(total)} in total. ${slices.map((s) => `${s.name} ${format(s.value)} (${share(s.value)})`).join(', ')}`}>
      {!total && <circle cx="60" cy="60" r="48" className="wc-track" />}
      <g transform="rotate(-90 60 60)">{slices.map((s) => { const len = total ? s.value / total * C : 0; const offset = at; at += len; return len > 0 && <circle key={s.id} className={`wc-slice ${s.cls}${s.id === hover ? ' on' : ''}`} cx="60" cy="60" r="48" onMouseEnter={() => setHover(s.id)} strokeDasharray={`${Math.max(0.5, len - gap)} ${C}`} strokeDashoffset={-offset}><title>{`${s.name}: ${format(s.value)} · ${share(s.value)}`}</title></circle>; })}</g>
      {on ? <><text x="60" y="50" textAnchor="middle" className="wc-name">{on.name}</text><text x="60" y="66" textAnchor="middle" className="wc-total">{format(on.value)}</text><text x="60" y="80" textAnchor="middle" className="wc-noun">{share(on.value)} of {noun}</text></>
        : <><text x="60" y="58" textAnchor="middle" className="wc-total">{format(total)}</text><text x="60" y="73" textAnchor="middle" className="wc-noun">{noun}</text></>}
    </svg></div>
  </div>;
}
export function Insights({ title, rows }: { title: string; rows: { title: string; body: string }[] }) { return <Panel title={title} className="insights">{rows.map((row) => <div className="insight" key={row.title}><svg className="symbol" width="18" height="18" viewBox="0 0 20 20" fill="none" stroke="currentColor" aria-hidden="true"><circle cx="10" cy="10" r="6"/><path d="M10 6v5m0 2v1"/></svg><span><b>{row.title}</b><p>{row.body}</p></span></div>)}</Panel>; }
// Daily recorded spend, one bar per day of the returned series (not cumulative).
export function DailySpend({ series }: { series: StatsSeriesPoint[] }) {
  if (!series.length) return <Unavailable>No activity in this period.</Unavailable>;
  const total = series.reduce((sum, r) => sum + r.spend, 0);
  const max = Math.max(0.01, ...series.map((r) => r.spend));
  const n = series.length;
  return <div className="runway-chart" role="img" aria-label={`Daily recorded spend, ${n} days, ${formatCurrencyCompact(total)} in total`}><ChartFrame wide={1240} max={max} format={formatCurrencyCompact} xLabels={[formatShortDay(series[0].date), formatShortDay(series[n - 1].date)]} hover={{ n, at: (i) => (i + 0.5) / n, tip: (i) => ({ title: formatShortDay(series[i].date), rows: [{ label: 'Spend', value: formatCurrency(series[i].spend), color: 'var(--green)' }] }) }}>{(f) => series.map((r, i) => <rect key={r.date} x={f.x((i + 0.15) / n)} width={f.x(0.7 / n) - f.left} y={f.y(r.spend)} height={f.bottom - f.y(r.spend)} fill="var(--green)" opacity=".85" />)}</ChartFrame></div>;
}
