// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { cleanup, render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router';
import { Traces } from './Traces';

const controls = vi.hoisted(() => ({ sessionSorts: [] as unknown[], sessionless: 10851 as number | null, truncated: false, sorts: [] as unknown[], emptyList: false, paginated: false, errorSpan: false, detailCapped: false, period: '30d', listFailed: false, nullUsage: false, classification: undefined as unknown }));
vi.mock('@/stores/console-range', () => ({ useConsoleDateRange: () => ({ start: controls.period === '30d' ? '2026-09-01' : '2026-09-24', end: '2026-09-30' }), useConsoleRange: (sel: (s: unknown) => unknown) => sel({ preset: controls.period, custom: null, setPreset: () => {}, setCustom: () => {} }) }));
const trace = { trace_id: 'trace-a', project_id: 'project-a', started_at: '2026-09-20 12:00:00.000000', ended_at: '2026-09-20 12:00:02.000000', span_count: 2, status: 'unknown' as const, kind: 'llm' as const, request_id: 'xvS9arLREKeXvdIP6LKnmQ', model: 'gemini.gemini-flash-latest', duration_ms: 2339, input_tokens: 1041, output_tokens: 388, cost: 0.00223575, key_alias: 'lk-80cffcc549141094', session_id: '128db405-aaaa-bbbb' as string | null };
const CALLS = '/analytics/traces?view=calls';
const session = { session_id: '128db405-aaaa-bbbb', project_id: 'project-a', started_at: '2026-09-20 12:00:00.000000', last_activity_at: '2026-09-20 14:00:00.000000', duration_ms: 7_184_241, turns: 253, generations: 253, input_tokens: 50_000, output_tokens: 1_000, cost: 1.23, models: ['claude-opus-5-5', 'gpt-x'], status: 'unknown' as const };
const turnRow = (trace_id: string, started_at: string, duration_ms: number) => ({ trace_id, started_at, model: 'claude-opus-5-5', input_tokens: 12_000, output_tokens: 450, cost: 0, duration_ms, status: 'unknown' as const });
vi.mock('@ee/hooks/use-sessions', () => ({
  DEFAULT_SESSION_SORT: { key: 'started_at', dir: 'desc' },
  useSessions: (_cursor = 0, sort?: unknown) => (controls.sessionSorts.push(sort), { isPending: false, isError: false, refetch: vi.fn(), data: { window: { start: '', end: '', timezone: 'UTC' }, sessions: [session], total_count: 383, next_cursor: null, sessionless_traces: controls.sessionless, provenance: { state: 'available', reason: null, sampled: false } } }),
  useSession: (id: string | null) => ({ isPending: false, isError: false, data: id && { ...session, models: undefined, classification: { session_id: session.session_id, category: 'technology.software_engineering', domain: 'technology', confidence: 0.95, state: 'classified' }, turns_list: [turnRow('turn-1', '2026-09-20T12:00:00Z', 4_000), turnRow('turn-2', '2026-09-20T12:01:04Z', 2_339)], turns_truncated: controls.truncated, provenance: { state: 'available', reason: null, sampled: false } } }),
}));
vi.mock('@ee/hooks/use-traces', () => ({
  TRACE_PAGE_SIZE: 25,
  DEFAULT_TRACE_SORT: { key: 'started_at', dir: 'desc' },
  useTraces: (cursor = 0, status = 'all', search = '', sort?: unknown) => (controls.sorts.push(sort), { isPending: false, isError: controls.listFailed, refetch: vi.fn(), data: { window: { start: '', end: '', timezone: 'UTC' }, traces: controls.emptyList || (controls.period === '7d' && cursor > 0) || (search && search !== 'older') || (status === 'error' && cursor > 0) ? [] : [search === 'older' ? {...trace, trace_id: 'older-trace', status: 'error'} : controls.nullUsage ? {...trace, kind: null, request_id: null, model: null, input_tokens: null, output_tokens: null, cost: null, key_alias: null, session_id: null} : trace], total_count: 2000, next_cursor: controls.paginated && cursor === 0 ? 25 : null, provenance: { state: 'available', reason: null, sampled: false } } }),
  useTrace: () => ({ isPending: false, isError: false, data: { trace_id: 'trace-a', request_id: 'xvS9arLREKeXvdIP6LKnmQ', status: controls.errorSpan ? 'error' : 'success', cost: controls.nullUsage ? null : 0.00223575, project_id: 'project-a', classification: controls.classification, spans: [{ span_id: 'root', parent_span_id: null, type: 'SPAN', name: 'chat', start_time: '2026-09-20T12:00:00Z', end_time: '2026-09-20T12:00:02Z', status: controls.errorSpan ? 'error' : 'unknown', model: null }], provenance: { state: controls.detailCapped ? 'partial' : 'available', reason: controls.detailCapped ? 'row_cap' : null, sampled: controls.detailCapped } } }),
}));

beforeEach(() => Object.assign(controls, { sessionSorts: [], sessionless: 10851, truncated: false, sorts: [], emptyList: false, paginated: false, errorSpan: false, detailCapped: false, period: '30d', listFailed: false, nullUsage: false, classification: undefined }));
afterEach(cleanup);

describe('Traces', () => {
  it('renders a list and selected detail without implying request identity', () => {
    render(<MemoryRouter initialEntries={['/analytics/traces?view=calls&trace=trace-a']}><Traces /></MemoryRouter>);
    expect(screen.getByRole('dialog', { name: 'trace-a' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'trace-a' })).toBeInTheDocument();
    expect(screen.getByText('chat')).toBeInTheDocument();
    expect(screen.getByText(/Success means the request completed with no recorded error/)).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Success', hidden: true })).toBeInTheDocument();
  });
});

it('filters traces and restores focus after closing the detail drawer', async () => {
 render(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
 const search = screen.getByRole('textbox', { name: 'Search traces' });
 fireEvent.change(search,{target:{value:'missing'}});
 await waitFor(() => expect(screen.getByText('No traces match these filters.')).toBeInTheDocument());
 fireEvent.change(search,{target:{value:''}});
 const trigger = await screen.findByRole('button', { name: 'Open trace trace-a' });
 trigger.focus(); fireEvent.click(trigger);
 expect(screen.getByRole('dialog', { name: 'trace-a' })).toBeInTheDocument();
 fireEvent.keyDown(document,{key:'Escape'});
 expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
 await waitFor(() => expect(trigger).toHaveFocus());
});
it('opens full trace detail with the project preserved', async () => {
 render(<MemoryRouter initialEntries={['/analytics/traces?trace=trace-a&project=project-a']}><Traces /></MemoryRouter>);
 fireEvent.click(screen.getByRole('link', { name: 'Open full trace →' }));
 expect(screen.getByRole('heading', { name: 'Trace trace-a' })).toBeInTheDocument();
 expect(screen.getByRole('heading', { name: 'Execution timeline' })).toBeInTheDocument();
 expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});


it('derives direct-link extent and observed error from detail spans outside the list page', () => {
  controls.emptyList = true;
  controls.errorSpan = true;
  render(<MemoryRouter initialEntries={['/analytics/traces?trace=trace-a&project=project-a']}><Traces /></MemoryRouter>);
  const dialog = screen.getByRole('dialog');
  expect(within(dialog).getByText('2.0 s', { selector: 'b' })).toBeInTheDocument(); // the extent, not the axis tick
  const outcome = within(dialog).getByText('Source outcome').parentElement!;
  expect(within(outcome).getByText('Failed')).toBeInTheDocument();
});

it('shows selected-detail truncation even when the list is complete', () => {
  controls.detailCapped = true;
  render(<MemoryRouter initialEntries={['/analytics/traces?trace=trace-a&project=project-a&view=full']}><Traces /></MemoryRouter>);
  expect(screen.getByText(/selected trace.*cap.*incomplete/i)).toBeInTheDocument();
});

it('returns to the first page when the selected period changes', () => {
  controls.paginated = true;
  const view = render(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  expect(screen.getByRole('button', { name: 'Previous' })).toBeInTheDocument();
  controls.period = '7d';
  view.rerender(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  expect(screen.getByRole('button', { name: 'Open trace trace-a' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
  controls.period = '30d';
  view.rerender(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
});


it('searches the complete source after typing settles and resets an old page offset', async () => {
  controls.paginated = true;
  render(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  fireEvent.change(screen.getByRole('textbox', { name: 'Search traces' }), { target: { value: 'older' } });
  expect(screen.queryByRole('button', { name: 'Open trace older-trace' })).not.toBeInTheDocument();
  expect(await screen.findByRole('button', { name: 'Open trace older-trace' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
  expect(screen.getByText('2,000')).toBeInTheDocument();
});

it('resets pagination when changing the server outcome filter', () => {
  controls.paginated = true;
  render(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  fireEvent.change(screen.getByRole('combobox', { name: 'Trace status' }), { target: { value: 'error' } });
  expect(screen.getByRole('button', { name: 'Open trace trace-a' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
});

it.each(['', '&view=full'])('keeps direct-link detail available when its independent list fails (%s)', (view) => {
  controls.listFailed = true;
  render(<MemoryRouter initialEntries={['/analytics/traces?trace=trace-a&project=project-a' + view]}><Traces /></MemoryRouter>);
  expect(screen.getByText('chat')).toBeInTheDocument();
  expect(screen.getByText('2.0 s', { selector: 'b' })).toBeInTheDocument();
});

it('renders LiteLLM-style request log columns', () => {
  const { container } = render(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  expect(Array.from(container.querySelectorAll('.trace-table th')).map((e) => e.textContent)).toEqual(['Time', 'Type', 'Spans', 'Status', 'Request ID', 'Model', 'Duration', 'Tokens', 'Cost', 'Key', 'Session']);
  expect(screen.getByText('LLM')).toBeInTheDocument();
  expect(screen.getByText('1,429 (1,041+388)')).toBeInTheDocument();
  expect(container.querySelector('td.c-status')).toHaveTextContent('Unknown');
  expect(container.querySelector('td.c-status .pill')).toHaveClass('neutral');
  expect(screen.getByText('$0.0022')).toBeInTheDocument();
  expect(screen.getByText('2.3 s')).toBeInTheDocument();
  expect(screen.getByText('xvS9arLREKeXvdIP6LKnmQ')).toHaveAttribute('title', 'xvS9arLREKeXvdIP6LKnmQ');
  expect(screen.getByText('lk-80cffcc549141094')).toHaveAttribute('title', 'lk-80cffcc549141094');
  expect(screen.getByRole('button', { name: 'Copy request ID' })).toBeInTheDocument();
});

it('sorts on the server: Time newest first by default, a header click asks for that column and resets the page', () => {
  controls.paginated = true;
  const { container } = render(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  expect(controls.sorts.at(-1)).toEqual({ key: 'started_at', dir: 'desc' });
  expect(container.querySelector('th[aria-sort=descending]')).toHaveTextContent('Time');
  expect(screen.queryByText(/Newest first/i)).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  fireEvent.click(within(container.querySelector('th[data-col=cost]') as HTMLElement).getByRole('button'));
  expect(controls.sorts.at(-1)).toEqual({ key: 'cost', dir: 'desc' });
  expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
  fireEvent.click(within(container.querySelector('th[data-col=cost]') as HTMLElement).getByRole('button'));
  expect(controls.sorts.at(-1)).toEqual({ key: 'cost', dir: 'asc' });
});

it('ends the list with the standard footer: period note left, "1–1 of 2,000" pager right', () => {
  const { container } = render(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  const foot = container.querySelector('.trace-table .report-foot')!;
  expect(foot.firstElementChild).toHaveTextContent('2026-09-01 – 2026-09-30 · UTC');
  expect(foot.lastElementChild).toHaveClass('report-pager');
  expect(foot.lastElementChild).toHaveTextContent('1–1 of 2,000');
});

it('shows a dash for missing usage, kind, request id and cost', () => {
  controls.emptyList = false;
  controls.nullUsage = true;
  render(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  expect(screen.queryByText('LLM')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Copy request ID' })).not.toBeInTheDocument();
  expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(5);
});

it('shows the full request id with a copy button in the detail', () => {
  render(<MemoryRouter initialEntries={['/analytics/traces?trace=trace-a&project=project-a']}><Traces /></MemoryRouter>);
  const dialog = screen.getByRole('dialog');
  expect(within(dialog).getByText('xvS9arLREKeXvdIP6LKnmQ')).toBeInTheDocument();
  expect(within(dialog).getByRole('button', { name: 'Copy request ID' })).toBeInTheDocument();
});

it('shows the drawer total cost and success outcome from the detail, not a placeholder', () => {
  render(<MemoryRouter initialEntries={['/analytics/traces?trace=trace-a&project=project-a']}><Traces /></MemoryRouter>);
  const dialog = screen.getByRole('dialog');
  expect(within(within(dialog).getByText('Total cost').parentElement!).getByText('$0.0022')).toBeInTheDocument();
  expect(within(within(dialog).getByText('Source outcome').parentElement!).getByText('Success')).toBeInTheDocument();
});

it('says cost is unavailable when the detail has no model call', () => {
  controls.nullUsage = true;
  render(<MemoryRouter initialEntries={['/analytics/traces?trace=trace-a&project=project-a']}><Traces /></MemoryRouter>);
  expect(within(screen.getByRole('dialog')).getByText('Total cost · no model call')).toBeInTheDocument();
});

it('shows the start in local time and the period picker in the header', () => {
  const { container } = render(<MemoryRouter initialEntries={[CALLS]}><Traces /></MemoryRouter>);
  expect(container.querySelector('.head .actions .range-trigger')).toBeInTheDocument();
  const time = screen.getByRole('button', { name: 'Open trace trace-a' });
  expect(time).toHaveTextContent(/Sep 20, 2026/);
  expect(time).not.toHaveTextContent('UTC');
  expect(screen.queryByRole('combobox', { name: 'Trace range', hidden: true })).toBeNull();
});

it.each([
  [{ state: 'classified', session_id: 's1', category: 'technology.software_engineering', domain: 'technology', confidence: 0.82 }, 'Software engineering', 'confidence 0.82'],
  [{ state: 'classified', session_id: 's1', category: 'trivial', domain: null, confidence: null }, 'Trivial', null],
  [{ state: 'not_classified', session_id: 's1', category: null, domain: null, confidence: null }, 'Not classified yet', 'Sessions are classified after 30 min idle.'],
  [{ state: 'no_session', session_id: null, category: null, domain: null, confidence: null }, 'No session', 'This trace has no session id, so it is not classified.'],
  [{ state: 'unavailable', session_id: null, category: null, domain: null, confidence: null }, 'Classification unavailable', null],
])('drawer Work category shows the session classification (%#)', (classification, label, line) => {
  controls.classification = classification;
  render(<MemoryRouter initialEntries={['/analytics/traces?trace=trace-a']}><Traces /></MemoryRouter>);
  const section = screen.getByRole('heading', { name: 'Work category' }).closest('section')!;
  expect(within(section).getByText(label)).toBeInTheDocument();
  if (line) expect(section).toHaveTextContent(line);
  expect(section).not.toHaveTextContent(/coming soon|corrections/i);
  if (classification.category === 'technology.software_engineering') expect(section.querySelector('.tag .wc-swatch.wc-c-technology-software_engineering')).not.toBeNull();
  if (classification.category === 'trivial') expect(within(section).getByText('Trivial')).toHaveClass('tag', 'dim');
  if (classification.state === 'not_classified') expect(within(section).getByText(label)).toHaveClass('pill', 'neutral');
});

describe('Sessions', () => {
  it('is the default view: session columns, server sort, sessionless note in the footer', () => {
    const { container } = render(<MemoryRouter initialEntries={['/analytics/traces']}><Traces /></MemoryRouter>);
    expect(screen.getByRole('button', { name: 'Sessions' })).toHaveAttribute('aria-pressed', 'true');
    expect(Array.from(container.querySelectorAll('.session-table th')).map((e) => e.textContent)).toEqual(['Started', 'Last activity', 'Duration', 'Turns', 'Tokens', 'Cost', 'Models', 'Status']);
    expect(screen.getByText('253')).toBeInTheDocument();
    expect(screen.getByText('1 h 59 min')).toBeInTheDocument();
    expect(screen.getByText('claude-opus-5-5, gpt-x')).toBeInTheDocument();
    expect(controls.sessionSorts.at(-1)).toEqual({ key: 'started_at', dir: 'desc' });
    fireEvent.click(within(container.querySelector('th[data-col=turns]') as HTMLElement).getByRole('button'));
    expect(controls.sessionSorts.at(-1)).toEqual({ key: 'turns', dir: 'desc' });
    const foot = container.querySelector('.session-table .report-foot')!;
    expect(foot.firstElementChild).toHaveTextContent('2026-09-01 – 2026-09-30 · UTC · 10,851 calls without a session are under Calls');
    expect(foot.lastElementChild).toHaveTextContent('1–1 of 383');
    expect(container.querySelector('.trace-table:not(.session-table)')).toBeNull();
  });

  it('drops the sessionless note when the count is unknown', () => {
    controls.sessionless = null;
    const { container } = render(<MemoryRouter initialEntries={['/analytics/traces']}><Traces /></MemoryRouter>);
    expect(container.querySelector('.session-table .report-foot')).not.toHaveTextContent('without a session');
  });

  it('switches to Calls: today\'s list plus a Session link that opens that session', () => {
    const { container } = render(<MemoryRouter initialEntries={['/analytics/traces']}><Traces /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: 'Calls' }));
    expect(screen.getByRole('button', { name: 'Calls' })).toHaveAttribute('aria-pressed', 'true');
    expect(container.querySelector('.session-table')).toBeNull();
    expect(screen.getByRole('textbox', { name: 'Search traces' })).toBeInTheDocument();
    fireEvent.click(within(container.querySelector('td.c-session') as HTMLElement).getByRole('button', { name: 'Open session 128db405-aaaa-bbbb' }));
    expect(screen.getByRole('dialog', { name: '128db405-aaaa-bbbb' })).toBeInTheDocument();
  });

  it('a session row opens the detail: totals, category, turn timeline with gaps', () => {
    render(<MemoryRouter initialEntries={['/analytics/traces']}><Traces /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: 'Open session 128db405-aaaa-bbbb' }));
    const dialog = within(screen.getByRole('dialog', { name: '128db405-aaaa-bbbb' }));
    expect(within(dialog.getByText('Turns', { selector: 'span' }).parentElement!).getByText('253')).toBeInTheDocument();
    expect(within(dialog.getByText('Total cost').parentElement!).getByText('$1.23')).toBeInTheDocument();
    expect(dialog.getByText('Software engineering')).toBeInTheDocument();
    expect(dialog.getByText(/confidence 0.95/)).toBeInTheDocument();
    const rows = screen.getByRole('dialog').querySelectorAll('.turn-table tbody tr');
    expect(rows).toHaveLength(2);
    expect(rows[0].querySelector('[data-col=gap]')).toHaveTextContent('—');
    expect(rows[1].querySelector('[data-col=gap]')).toHaveTextContent('1 min 0 s'); // 64 s after the start, minus the 4 s first turn
    expect(rows[1].querySelector('[data-col=tokens]')).toHaveTextContent('12.45K');
    expect(dialog.queryByText(/first .* turns/)).toBeNull();
  });

  it('a turn opens the existing trace detail; closing it returns to the session', () => {
    render(<MemoryRouter initialEntries={['/analytics/traces?session=128db405-aaaa-bbbb&project=project-a']}><Traces /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: 'Open trace turn-2' }));
    expect(screen.getByRole('dialog', { name: 'turn-2' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Execution waterfall' })).toBeInTheDocument();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.getByRole('dialog', { name: '128db405-aaaa-bbbb' })).toBeInTheDocument();
  });

  it('says when the turn timeline is truncated', () => {
    controls.truncated = true;
    render(<MemoryRouter initialEntries={['/analytics/traces?session=128db405-aaaa-bbbb&project=project-a']}><Traces /></MemoryRouter>);
    expect(within(screen.getByRole('dialog')).getByText('Showing the first 2 of 253 turns.')).toBeInTheDocument();
  });
});
