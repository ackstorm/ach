// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// Traces.tsx — the caller's own traces: list, detail drawer and full view.
import { useEffect, useState } from 'react';
import { EVIDENCE_LIMIT_NOTICE, sourceUnavailable, useEvidenceWindow } from '@/hooks/use-evidence-reports';
import { Link, useSearchParams } from 'react-router';
import { TraceWaterfall, sessionSpans } from '@ee/components/traces/TraceWaterfall';
import { DetailDrawer } from '@/components/observability/DetailDrawer';
import { DEFAULT_TRACE_SORT, TRACE_PAGE_SIZE, useTrace, useTraces, type TraceSort, type TraceStatusFilter } from '@ee/hooks/use-traces';
import { DEFAULT_SESSION_SORT, useSession, useSessions, type SessionSort } from '@ee/hooks/use-sessions';
import { DataTable, Pager, type DataTableColumn } from '@/components/ui/data-table';
import type { SessionSummary, SessionTurn, TraceClassification, TraceSummary } from '@/lib/api-types';
import { workClass, workLabel } from '@ee/lib/work-labels';
import { useCopyFeedback } from '@/hooks/use-copy-feedback';
import { formatLocalDate, formatLocalTime, formatTokens, formatDecimal, formatSeconds, formatUsdSmall, formatInt, formatDuration, formatLocalDateTime } from '@/lib/format';
import { StatusPill } from '@/components/ui/StatusPill';
import { Tag } from '@/components/ui/Tag';
import { COMING_SOON, traceOutcome } from '@/lib/status';
import { RangePicker } from '@/components/analytics/RangePicker';
import { Segmented, SkelRows } from '@/components/analytics/ReportParts';

function elapsed(start: string | null, end: string | null): string {
  if (!start || !end) return '—';
  const seconds = (Date.parse(end) - Date.parse(start)) / 1000;
  return Number.isFinite(seconds) && seconds >= 0 ? `${formatDecimal(seconds, 1)} s` : '—';
}

const DASH = '—';
const clip = (text: string | null | undefined, n: number) => !text ? DASH : text.length > n ? `${text.slice(0, n)}…` : text;
const duration = (ms: number | undefined) => ms == null ? DASH : formatSeconds(ms);
const cost = (c: number | null | undefined) => c == null ? DASH : formatUsdSmall(c);

// The session's classifier label (taxonomy v3); the trace detail always answers.
function WorkCategory({ c }: { c: TraceClassification | null | undefined }) {
  if (c?.state === 'classified' && c.category) return <p>{c.category === 'trivial' ? <Tag dim>{workLabel(c.category)}</Tag> : <Tag><i className={`wc-swatch ${workClass(c.category, c.domain)}`} />{workLabel(c.category)}</Tag>}{c.confidence != null && <span className="muted"> confidence {formatDecimal(c.confidence, 2)}</span>}</p>;
  if (c?.state === 'not_classified') return <><StatusPill tone="neutral">Not classified yet</StatusPill><p className="muted">Sessions are classified after 30 min idle.</p></>;
  if (c?.state === 'no_session') return <><StatusPill tone="neutral">No session</StatusPill><p className="muted">This trace has no session id, so it is not classified.</p></>;
  return <p className="muted">Classification unavailable</p>;
}

function RequestId({ id, short = true, className = '' }: { id: string | null | undefined; short?: boolean; className?: string }) {
  const { copied, copy } = useCopyFeedback();
  if (!id) return <span className={className}>{DASH}</span>;
  return <span className={`mono req-id ${className}`}><span title={id}>{short ? clip(id, 14) : id}</span><button type="button" className="btn sm req-copy" aria-label="Copy request ID" onClick={(event) => { event.stopPropagation(); void copy(id); }}>{copied ? 'copied' : 'copy'}</button></span>;
}

// A server-paged cursor that returns to the first page whenever `key` (window, filters, sort) changes.
function useCursor(key: string): [number, (cursor: number) => void] {
  const [page, setPage] = useState({ key, cursor: 0 });
  useEffect(() => setPage({ key, cursor: 0 }), [key]);
  return [page.key === key ? page.cursor : 0, (cursor) => setPage({ key, cursor })];
}

// Idle time between the previous turn's end and this turn's start.
function gap(turn: SessionTurn, prev: SessionTurn | undefined): string {
  if (!prev) return DASH;
  const ms = Date.parse(turn.started_at) - Date.parse(prev.started_at) - prev.duration_ms;
  return Number.isFinite(ms) ? formatDuration(Math.max(0, ms)) : DASH;
}

const VIEWS = ['Sessions', 'Calls'] as const;

// A long session's waterfall stays readable; the Turns table lists them all.
const WATERFALL_TURNS = 30;

export function Traces() {
  const [params, setParams] = useSearchParams();
  const selected = params.get('trace');
  const project = params.get('project');
  const sessionId = params.get('session');
  const full = params.get('view') === 'full';
  const view = params.get('view') === 'calls' ? 'Calls' : 'Sessions';
  const setView = (next: (typeof VIEWS)[number]) => { const p = new URLSearchParams(params); p.set('view', next.toLowerCase()); setParams(p); };
  const { range, tooLong } = useEvidenceWindow();
  const [search, setSearch] = useState('');
  const [querySearch, setQuerySearch] = useState('');
  useEffect(() => {
    const timer = setTimeout(() => setQuerySearch(search), 300);
    return () => clearTimeout(timer);
  }, [search]);
  const [status, setStatus] = useState<TraceStatusFilter>('all');
  const [sort, setSort] = useState<TraceSort>(DEFAULT_TRACE_SORT);
  const [cursor, setCursor] = useCursor(JSON.stringify([range.start, range.end, querySearch, status, sort]));
  const [sessionSort, setSessionSort] = useState<SessionSort>(DEFAULT_SESSION_SORT);
  const [sessionCursor, setSessionCursor] = useCursor(JSON.stringify([range.start, range.end, sessionSort]));
  const list = useTraces(cursor, status, querySearch, sort);
  const sessions = useSessions(sessionCursor, sessionSort);
  const detail = useTrace(selected, project);
  const session = useSession(sessionId, project);
  // Closing a trace opened from a session returns to that session.
  const close = () => { const next = new URLSearchParams(params); next.delete('trace'); if (!next.has('session')) next.delete('project'); if (full) next.delete('view'); setParams(next); };
  const closeSession = () => { const next = new URLSearchParams(params); next.delete('session'); next.delete('project'); setParams(next); };
  const openSession = (id: string, projectId: string) => { const next = new URLSearchParams(params); next.set('session', id); next.set('project', projectId); next.delete('trace'); setParams(next); };
  const listData = list.isError ? undefined : list.data;
  const rows = listData?.traces ?? [];
  const listAvailable = Boolean(listData && !sourceUnavailable(listData.provenance));
  const listNotice = tooLong ? <p className="report-notice">{EVIDENCE_LIMIT_NOTICE}</p> : list.isPending ? <SkelRows /> : list.isError || !listData ? <p role="alert" className="report-notice">Trace list could not be loaded. <button type="button" className="btn sm" disabled={list.isFetching} onClick={() => void list.refetch()}>Retry trace list</button></p> : null;
  const timedSpans = (detail.data?.spans ?? []).flatMap((span) => {
    const start = Date.parse(span.start_time ?? '');
    const end = Date.parse(span.end_time ?? '');
    return Number.isFinite(start) && Number.isFinite(end) && end >= start ? [{ start, end }] : [];
  });
  const detailExtent = timedSpans.length ? elapsed(
    new Date(Math.min(...timedSpans.map((span) => span.start))).toISOString(),
    new Date(Math.max(...timedSpans.map((span) => span.end))).toISOString(),
  ) : '—';
  const detailOutcome = detail.data?.status ?? 'unknown';
  const select = (trace: string, projectId: string, fullView = false) => { const next = new URLSearchParams(params); next.set('trace', trace); next.set('project', projectId); if (fullView) next.set('view', 'full'); else if (full) next.delete('view'); setParams(next); };
  // Every column sorts on the server (the list is cursor-paged over the whole window).
  const sortable = (row: TraceSummary) => row.trace_id;
  const traceColumns: DataTableColumn<TraceSummary>[] = [
    { key: 'started_at', header: 'Time', className: 'c-time', sortAccessor: sortable, cell: (t) => <button type="button" className="trace-open" onClick={(event) => { event.stopPropagation(); select(t.trace_id, t.project_id); }} aria-label={`Open trace ${t.trace_id}`}><b>{t.started_at ? formatLocalDate(t.started_at) : 'Unknown date'}</b><small>{formatLocalTime(t.started_at)}</small></button> },
    { key: 'kind', header: 'Type', className: 'c-type', sortAccessor: sortable, cell: (t) => t.kind ? <Tag>{t.kind.toUpperCase()}</Tag> : DASH },
    { key: 'spans', header: 'Spans', className: 'mono c-spans', sortAccessor: sortable, cell: (t) => formatInt(t.span_count) },
    { key: 'status', header: 'Status', className: 'c-status', sortAccessor: sortable, cell: (t) => <StatusPill tone={traceOutcome(t.status).tone}>{traceOutcome(t.status).label}</StatusPill> },
    { key: 'request_id', header: 'Request ID', className: 'c-req', sortAccessor: sortable, cell: (t) => <RequestId id={t.request_id} short={false} /> },
    { key: 'model', header: 'Model', className: 'mono c-model', sortAccessor: sortable, cell: (t) => <span title={t.model ?? undefined}>{t.model ?? DASH}</span> },
    { key: 'duration', header: 'Duration', className: 'mono c-dur', sortAccessor: sortable, cell: (t) => duration(t.duration_ms) },
    { key: 'tokens', header: 'Tokens', className: 'mono c-tok', sortAccessor: sortable, cell: (t) => formatTokens(t.input_tokens, t.output_tokens) },
    { key: 'cost', header: 'Cost', className: 'mono c-cost', sortAccessor: sortable, cell: (t) => cost(t.cost) },
    { key: 'key', header: 'Key', className: 'mono c-key', sortAccessor: sortable, cell: (t) => <span title={t.key_alias ?? undefined}>{t.key_alias ?? DASH}</span> },
    { key: 'session', header: 'Session', className: 'mono c-session', cell: (t) => t.session_id ? <button type="button" className="trace-open trace-link" title={t.session_id} aria-label={`Open session ${t.session_id}`} onClick={(event) => { event.stopPropagation(); openSession(t.session_id!, t.project_id); }}>{clip(t.session_id, 8)}</button> : DASH },
  ];
  const sessionData = sessions.isError ? undefined : sessions.data;
  const sessionRows = sessionData?.sessions ?? [];
  const sessionNotice = tooLong ? <p className="report-notice">{EVIDENCE_LIMIT_NOTICE}</p> : sessions.isPending ? <SkelRows /> : sessions.isError || !sessionData ? <p role="alert" className="report-notice">Session list could not be loaded. <button type="button" className="btn sm" disabled={sessions.isFetching} onClick={() => void sessions.refetch()}>Retry session list</button></p> : null;
  const bySession = (row: SessionSummary) => row.session_id;
  const sessionColumns: DataTableColumn<SessionSummary>[] = [
    { key: 'started_at', header: 'Started', className: 'c-time', sortAccessor: bySession, cell: (s) => <button type="button" className="trace-open" onClick={(event) => { event.stopPropagation(); openSession(s.session_id, s.project_id); }} aria-label={`Open session ${s.session_id}`}><b>{formatLocalDate(s.started_at)}</b><small>{formatLocalTime(s.started_at)}</small></button> },
    { key: 'last_activity_at', header: 'Last activity', className: 'mono', sortAccessor: bySession, cell: (s) => formatLocalDateTime(s.last_activity_at) },
    { key: 'duration', header: 'Duration', className: 'mono c-dur', sortAccessor: bySession, cell: (s) => formatDuration(s.duration_ms) },
    { key: 'turns', header: 'Turns', className: 'mono', sortAccessor: bySession, cell: (s) => formatInt(s.turns) },
    { key: 'tokens', header: 'Tokens', className: 'mono c-tok', sortAccessor: bySession, cell: (s) => formatTokens(s.input_tokens, s.output_tokens) },
    { key: 'cost', header: 'Cost', className: 'mono c-cost', sortAccessor: bySession, cell: (s) => cost(s.cost) },
    { key: 'models', header: 'Models', className: 'mono c-model', cell: (s) => <span title={s.models.join(', ')}>{s.models.length ? s.models.join(', ') : DASH}</span> },
    { key: 'status', header: 'Status', className: 'c-status', cell: (s) => <StatusPill tone={traceOutcome(s.status).tone}>{traceOutcome(s.status).label}</StatusPill> },
  ];
  const turns = session.data?.turns_list ?? [];
  const turnColumns: DataTableColumn<SessionTurn>[] = [
    { key: 'time', header: 'Time', className: 'c-time', cell: (t) => <button type="button" className="trace-open" onClick={(event) => { event.stopPropagation(); select(t.trace_id, session.data!.project_id); }} aria-label={`Open trace ${t.trace_id}`}><b>{formatLocalTime(t.started_at)}</b></button> },
    { key: 'model', header: 'Model', className: 'mono c-model', cell: (t) => <span title={t.model ?? undefined}>{t.model ?? DASH}</span> },
    { key: 'tokens', header: 'Tokens', className: 'mono', cell: (t) => formatTokens(t.input_tokens, t.output_tokens, true) },
    { key: 'duration', header: 'Duration', className: 'mono', cell: (t) => formatSeconds(t.duration_ms) },
    { key: 'gap', header: 'Gap', className: 'mono', cell: (t) => gap(t, turns[turns.indexOf(t) - 1]) },
    { key: 'status', header: 'Status', className: 'c-status', cell: (t) => <StatusPill tone={traceOutcome(t.status).tone}>{traceOutcome(t.status).label}</StatusPill> },
  ];
  const s = session.data;
  const sessionContent = !sessionId ? null : session.isPending ? <p className="report-notice">Loading session evidence…</p> : session.isError || !s ? <p className="report-notice">This session is unavailable or was deleted.</p> : <>
    <section><h3>Session result</h3><div className="scoregrid"><div><b>{formatInt(s.turns)}</b><span>Turns</span></div><div><b title={formatTokens(s.input_tokens, s.output_tokens)}>{formatTokens(s.input_tokens, s.output_tokens, true)}</b><span>Tokens</span></div><div><b>{cost(s.cost)}</b><span>Total cost</span></div><div><b>{formatDuration(s.duration_ms)}</b><span>Duration</span></div></div></section>
    <section><h3>Work category</h3><WorkCategory c={s.classification} /></section>
    <section><h3>Execution waterfall</h3><TraceWaterfall spans={sessionSpans(turns.slice(0, WATERFALL_TURNS))} /><p className="report-source-note">{turns.length > WATERFALL_TURNS && `First ${WATERFALL_TURNS} of ${formatInt(turns.length)} turns. `}Model bars are the session's turns; tool bars are the tools each turn asked for, run by your client before the next turn (that gap also holds any wait for you).</p></section>
    <section><h3>Turns</h3>{s.turns_truncated && <p className="report-notice">Showing the first {formatInt(turns.length)} of {formatInt(s.turns)} turns.</p>}<DataTable<SessionTurn> className="trace-table turn-table" columns={turnColumns} rows={turns} getRowId={(t) => t.trace_id} onRowClick={(t) => select(t.trace_id, s.project_id)} empty="No turns recorded." /></section>
  </>;
  const content = !selected ? null : detail.isPending ? <p className="report-notice">Loading trace evidence…</p> : detail.isError || !detail.data ? <p className="report-notice">This trace is unavailable or was deleted.</p> : <>
    <section><h3>Request ID</h3><RequestId id={detail.data.request_id} short={false} /></section>
    <section><h3>Request result</h3><div className="scoregrid"><div><b>{detail.data.cost == null ? 'Unavailable' : cost(detail.data.cost)}</b><span>{detail.data.cost == null ? 'Total cost · no model call' : 'Total cost'}</span></div><div><b>{detailExtent}</b><span>Observed span extent</span></div><div><b>{detail.data.spans.length}</b><span>Observed spans</span></div><div><b>{traceOutcome(detailOutcome).label}</b><span>Source outcome</span></div></div></section>
    <section><h3>Source coverage</h3><p>{detail.data.provenance.sampled ? 'The selected trace reached the source cap; its spans may be incomplete.' : 'Only your observed spans within the selected period are included.'}</p></section>
    <section><h3>Execution waterfall</h3><TraceWaterfall spans={detail.data.spans} /></section>
    <section><h3>Context composition <StatusPill tone="info">Instrumentation required</StatusPill></h3><p className="muted">Context segments are not recorded for this trace.</p><div className="context-legend">{['History', 'Tool results', 'Skills + schemas', 'Current input'].map((label, i) => <div key={label}><i className="dot" style={{background:['var(--blue)','var(--amber)','var(--purple)','var(--green)'][i]}} />{label}<b>—</b></div>)}</div></section>
    <section><h3>Observed pattern</h3><div className="callout"><b>{detail.data.spans.filter((span) => span.type === 'GENERATION').length} model generations in the retained projection</b><p>Tool execution results remain unverified. Success means the request completed with no recorded error, not that the task succeeded. Timing shows observed span intervals.</p></div></section>
    <section><h3>Work category</h3><WorkCategory c={detail.data.classification} /></section>
  </>;
  return <>
    <div className="head"><div><h1>{full && selected ? `Trace ${selected}` : 'Traces'}</h1><p>{full ? 'Scoped span detail · task outcomes remain unverified.' : view === 'Sessions' ? 'Your sessions in the selected period; each turn is one call.' : 'Recent execution spans in your selected period.'}</p></div><div className="actions">{full ? <button className="btn" onClick={close}>Back to traces</button> : <><Segmented label="Traces view" options={VIEWS} value={view} onChange={setView} /><RangePicker /></>}</div></div>
    {full ? <><div className="trace-detail-grid"><div><section className="panel hero-metric"><div className="hero-top"><div><h2>Context growth by turn <StatusPill tone={COMING_SOON.tone}>Context segments · {COMING_SOON.label}</StatusPill></h2><p>Context composition requires the context callback.</p></div></div><div className="empty"><b>Context growth is not instrumented</b><p>History, tool results, skill instructions, and current input cannot be separated from span timing alone.</p></div></section><section className="panel trace-full" style={{marginTop:13}}><div className="panel-title"><h2>Execution timeline</h2><span className="right label">Bounded source projection</span></div><div className="drawer-body">{content}</div></section></div><aside><section className="panel"><div className="panel-title"><h2>Other recent traces</h2></div><div className="similar">{listNotice}{rows.filter((row) => row.trace_id !== selected || row.project_id !== project).slice(0,5).map((row) => <button className="similar-row entity-open" key={`${row.project_id}:${row.trace_id}`} onClick={() => select(row.trace_id,row.project_id,true)}><b>{row.trace_id}</b><StatusPill tone={traceOutcome(row.status).tone}>{traceOutcome(row.status).label}</StatusPill><span className="mini">{row.span_count} spans · no similarity classification</span></button>)}</div></section></aside></div><div className="metric-grid"><div className="panel metric"><span className="label">Model calls</span><div className="value">{detail.data?.spans.filter(span => span.type === 'GENERATION').length ?? '—'}</div><p>Observed generation spans</p></div><div className="panel metric"><span className="label">MCP + tool calls</span><div className="value">{detail.data?.spans.filter(span => span.type === 'TOOL').length ?? '—'}</div><p>Observed tool spans · completion not inferred</p></div><div className="panel metric"><span className="label">Cache reads</span><div className="value">—</div><p>Trace cache attribution unavailable</p></div></div></> : <>
      {view === 'Sessions' ? <section className="panel"><div className="panel-title"><h2>Sessions</h2><p>Turns, tokens and outcome per session.</p></div>
        {sessionNotice ?? <DataTable<SessionSummary> className="trace-table session-table" columns={sessionColumns} rows={sessionRows} getRowId={(r) => `${r.project_id}:${r.session_id}`} onRowClick={(r) => openSession(r.session_id, r.project_id)} sort={sessionSort} onSort={(next) => setSessionSort(next as SessionSort)}
          empty={sessionData && sourceUnavailable(sessionData.provenance) ? 'Session data is not connected or could not be read.' : 'No sessions in this period.'}
          footer={<p className="report-source-note">{range.start} – {range.end} · UTC{sessionData?.provenance.sampled ? ' · bounded sample; sessions may be incomplete' : ''}{sessionData?.sessionless_traces ? ` · ${formatInt(sessionData.sessionless_traces)} calls without a session are under Calls` : ''}</p>}
          pager={sessionData?.total_count != null && sessionRows.length > 0 ? <Pager start={sessionCursor} count={sessionRows.length} total={sessionData.total_count} onPrev={sessionCursor > 0 ? () => setSessionCursor(Math.max(0, sessionCursor - TRACE_PAGE_SIZE)) : undefined} onNext={sessionData.next_cursor != null ? () => setSessionCursor(sessionData.next_cursor!) : undefined} /> : undefined} />}
      </section> : <>
      <div className="panel filtersbar trace-filters"><label className="filter"><span>Client</span><select aria-label="Trace client" disabled><option>Attribution unavailable</option></select></label><label className="filter"><span>Model</span><select aria-label="Trace model" disabled><option>Open trace for models</option></select></label><label className="filter"><span>Status</span><select aria-label="Trace status" value={status} onChange={(event) => setStatus(event.target.value as TraceStatusFilter)}><option value="all">All outcomes</option><option value="success">Success</option><option value="error">Error observed</option><option value="unknown">Unknown outcome</option></select></label><button className="btn primary apply" onClick={() => { setSearch(''); setStatus('all'); }}>Reset filters</button></div>
      <div className="metric-grid"><div className="panel metric"><span className="label">Traces</span><div className="value">{listAvailable ? formatInt(listData?.total_count ?? rows.length) : '—'}</div><p>{listData?.total_count != null ? 'Matching traces across the selected period.' : 'Traces on this page.'}</p></div><div className="panel metric"><span className="label">High context</span><div className="value">—</div><p>Input usage is not part of the trace-list contract.</p></div><div className="panel metric"><span className="label">Errors on this page</span><div className="value">{listAvailable ? rows.filter((row) => row.status === 'error').length : '—'}</div><p>Observed errors; other outcomes remain unknown.</p></div></div>
      <div className="toolbar" style={{marginTop:13}}><input className="search" aria-label="Search traces" placeholder="Search trace ID" maxLength={200} value={search} onChange={(event) => setSearch(event.target.value)} /><div className="tabs" aria-label="Trace result filters"><button className={status === 'all' ? 'on' : ''} onClick={() => setStatus('all')}>All</button><button disabled title="Input token attribution unavailable">High context</button><button disabled title="Trace cache attribution unavailable">Uncached</button><button className={status === 'error' ? 'on' : ''} onClick={() => setStatus('error')}>Failed</button></div></div>
      <section className="panel"><div className="panel-title"><h2>Recent executions</h2><p>Observed spans, duration, and coarse outcomes.</p></div>
        {listNotice ?? <DataTable<TraceSummary> className="trace-table" columns={traceColumns} rows={rows} getRowId={(t) => `${t.project_id}:${t.trace_id}`} onRowClick={(t) => select(t.trace_id, t.project_id)} sort={sort} onSort={(next) => setSort(next as TraceSort)}
          empty={listData && sourceUnavailable(listData.provenance) ? 'Trace data is not connected or could not be read.' : search || status !== 'all' ? 'No traces match these filters.' : 'No personal traces in this period.'}
          footer={<p className="report-source-note">{range.start} – {range.end} · UTC{listData?.provenance.sampled ? ' · bounded sample; traces may be incomplete' : ''}</p>}
          pager={listData?.total_count != null && rows.length > 0 ? <Pager start={cursor} count={rows.length} total={listData.total_count} onPrev={cursor > 0 ? () => setCursor(Math.max(0, cursor - TRACE_PAGE_SIZE)) : undefined} onNext={listData.next_cursor != null ? () => setCursor(listData.next_cursor!) : undefined} /> : undefined} />}
      </section></>}
      <DetailDrawer open={Boolean(sessionId) && !selected} onClose={closeSession} title={sessionId || 'Session detail'} description="Scoped personal session evidence">{sessionContent}</DetailDrawer>
      <DetailDrawer open={Boolean(selected)} onClose={close} title={selected || 'Trace detail'} description="Scoped personal trace evidence">{content}{selected && <Link className="btn primary trace-full-link" to={`/analytics/traces?trace=${encodeURIComponent(selected)}${project ? `&project=${encodeURIComponent(project)}` : ''}&view=full`}>Open full trace →</Link>}</DetailDrawer>
    </>}
  </>;
}
