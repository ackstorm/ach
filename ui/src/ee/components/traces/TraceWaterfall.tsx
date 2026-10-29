// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// TraceWaterfall.tsx — the trace's spans on one time axis: round ticks with
// gridlines, each bar placed by its start offset, children indented under
// their parent (start order among siblings), colour by span type with errors
// in the danger tone. A 0 ms span is a tick mark, never invisible.
import type { CSSProperties } from 'react';
import type { SessionTurn, TraceSpan } from '@/lib/api-types';
import { formatSeconds } from '@/lib/format';

const ms = (iso: string | null) => (iso ? Date.parse(iso) : NaN);
const timed = (span: TraceSpan) => Number.isFinite(ms(span.start_time)) && ms(span.end_time) >= ms(span.start_time);

function duration(span: TraceSpan): string {
  return timed(span) ? formatSeconds(ms(span.end_time) - ms(span.start_time), 2) : 'Timing unavailable';
}

// 1-2-5 step giving four or five intervals across the extent.
function ticks(extent: number): { at: number; label: string }[] {
  const raw = extent / 5;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 5, 10].map((m) => m * mag).find((s) => s >= raw)!;
  const out = [];
  for (let at = 0; at <= extent; at += step) out.push({ at, label: formatSeconds(at, step % 1000 === 0 ? 0 : 1) });
  return out;
}

// Depth-first order from parent_span_id, start order among siblings; a span
// whose parent is not in the trace is a root.
function tree(spans: TraceSpan[]): { span: TraceSpan; depth: number }[] {
  const ids = new Set(spans.map((s) => s.span_id));
  const out = new Map<TraceSpan, number>();
  const walk = (parent: string | null, depth: number) => {
    for (const span of spans) if (!out.has(span) && (span.parent_span_id && ids.has(span.parent_span_id) ? span.parent_span_id : null) === parent) { out.set(span, depth); walk(span.span_id, depth + 1); }
  };
  walk(null, 0);
  for (const span of spans) if (!out.has(span)) out.set(span, 0); // a parent cycle still lists its spans
  return [...out].map(([span, depth]) => ({ span, depth }));
}

const tone = (span: TraceSpan) => span.status === 'error' ? 't-error' : span.type === 'GENERATION' ? 't-gen' : span.type === 'TOOL' ? 't-tool' : 't-span';

export function TraceWaterfall({ spans }: { spans: TraceSpan[] }) {
  if (!spans.length) return <p className="text-sm text-text-secondary">No spans are available for this trace.</p>;
  const withTime = spans.filter(timed);
  const first = Math.min(...withTime.map((s) => ms(s.start_time)));
  const extent = Math.max(...withTime.map((s) => ms(s.end_time))) - first;
  const axis = extent > 0 ? ticks(extent) : [];
  const pct = (v: number) => `${v / extent * 100}%`;
  const grid = { '--tick': axis.length > 1 ? pct(axis[1].at) : '100%' } as CSSProperties;
  return <>
    <ol aria-label="Trace spans" className="waterfall">
      {axis.length > 0 && <li className="wf wf-axis" aria-hidden="true"><span /><span className="lane">{axis.map((t) => <small key={t.at} style={{ left: pct(t.at), transform: `translateX(-${t.at / extent * 100}%)` }}>{t.label}</small>)}</span><span /></li>}
      {tree(spans).map(({ span, depth }) => {
        const start = ms(span.start_time) - first;
        const length = ms(span.end_time) - ms(span.start_time);
        const placed = timed(span) && extent > 0;
        return <li key={span.span_id} className="wf" title={`${span.type} · ${span.model || 'Model unavailable'} · status ${span.status}${timed(span) ? ` · ${span.start_time} → ${span.end_time}` : ''}`}>
          <span className="wf-name" style={{ paddingLeft: depth * 12 }}>{span.name}<small>{span.model || 'Model unavailable'} · status {span.status}</small></span>
          {placed ? <span className="lane" style={grid} role="img" aria-label={`${span.name}: ${duration(span)}, starts ${formatSeconds(start, 2)} into trace`}><i className={`${tone(span)}${length === 0 ? ' instant' : ''}`} style={{ marginLeft: pct(start), width: pct(length) }} /></span> : <span className="lane" aria-hidden="true" />}
          <span className="v">{placed ? `+${formatSeconds(start)} · ${duration(span)}` : duration(span)}</span>
        </li>;
      })}
    </ol>
    <div className="wf-legend">{[['t-gen', 'Generation'], ['t-span', 'Span'], ['t-tool', 'Tool'], ['t-error', 'Error']].map(([cls, label]) => <span key={cls}><i className={cls} />{label}</span>)}</div>
  </>;
}

// A session as a waterfall: each turn is the model's generation, and the time
// until the next turn is the client running the tools that turn asked for
// (plus any wait for the user, which the gateway cannot tell apart).
export function sessionSpans(turns: SessionTurn[]): TraceSpan[] {
  return turns.flatMap((t, i) => {
    const model: TraceSpan = { span_id: t.trace_id, parent_span_id: null, type: 'GENERATION', name: `Model · ${t.model ?? 'unknown'}`, start_time: t.started_at, end_time: t.ended_at ?? null, status: t.status === 'error' ? 'error' : 'unknown', model: t.model };
    const next = turns[i + 1];
    if (!t.tools?.length || !next || !t.ended_at) return [model];
    const names = [...new Set(t.tools)];
    return [model, { span_id: `${t.trace_id}:tools`, parent_span_id: null, type: 'TOOL', name: `Tools · ${names.slice(0, 3).join(', ')}${names.length > 3 ? ` +${names.length - 3}` : ''}`, start_time: t.ended_at, end_time: next.started_at, status: 'unknown', model: null }];
  });
}
