// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { TraceWaterfall, sessionSpans } from './TraceWaterfall';

describe('TraceWaterfall', () => {
  it('renders hierarchy, duration, and unavailable model labels', () => {
    render(<TraceWaterfall spans={[{ span_id: 'root', parent_span_id: null, type: 'SPAN', name: 'chat', start_time: '2026-09-20T12:00:00Z', end_time: '2026-09-20T12:00:01Z', status: 'unknown', model: null }]} />);
    expect(screen.getByRole('list', { name: 'Trace spans' })).toBeInTheDocument();
    expect(screen.getByText('chat')).toBeInTheDocument();
    expect(screen.getByText(/Model unavailable/)).toBeInTheDocument();
    expect(screen.getByText(/1\.00 s/)).toBeInTheDocument();
  });
});


it('does not interpret missing timing as active execution', () => {
  render(<TraceWaterfall spans={[{ span_id: 'missing', parent_span_id: null, type: 'TOOL', name: 'Old invocation', start_time: null, end_time: null, status: 'unknown', model: null }]} />);
  expect(screen.getByText(/Timing unavailable/)).toBeInTheDocument();
  expect(screen.queryByText('In progress')).not.toBeInTheDocument();
});

it('positions spans on their shared observed timeline', () => {
  render(<TraceWaterfall spans={[
    { span_id: 'root', parent_span_id: null, type: 'SPAN', name: 'root', start_time: '2026-09-20T12:00:00Z', end_time: '2026-09-20T12:00:04Z', status: 'unknown', model: null },
    { span_id: 'child', parent_span_id: 'root', type: 'GENERATION', name: 'child', start_time: '2026-09-20T12:00:01Z', end_time: '2026-09-20T12:00:03Z', status: 'unknown', model: null },
  ]} />);
  expect(screen.getByRole('img', { name: 'child: 2.00 s, starts 1.00 s into trace' }).firstElementChild).toHaveStyle({ marginLeft: '25%', width: '50%' });
});

const at = (offsetMs: number) => new Date(Date.parse('2026-09-20T12:00:00Z') + offsetMs).toISOString();
const span = (id: string, parent: string | null, start: number, end: number, over: Record<string, unknown> = {}) => ({ span_id: id, parent_span_id: parent, type: 'SPAN', name: id, start_time: at(start), end_time: at(end), status: 'unknown' as const, model: null, ...over });

describe('TraceWaterfall timeline', () => {
  const spans = [
    span('POST /v1/messages', null, 0, 9935),
    span('auth', 'POST /v1/messages', 15, 23),
    span('guard', 'auth', 44, 44, { status: 'error' }),
    span('chat', 'POST /v1/messages', 164, 9907, { type: 'GENERATION' }),
  ];

  it('places a span by its start offset and shows "+start · duration"', () => {
    render(<TraceWaterfall spans={spans} />);
    const bar = screen.getByRole('img', { name: /^chat:/ }).firstElementChild as HTMLElement;
    expect(parseFloat(bar.style.marginLeft)).toBeCloseTo(164 / 9935 * 100, 2);
    expect(bar).toHaveClass('t-gen');
    expect(screen.getByText('+164 ms · 9.74 s')).toBeInTheDocument();
  });

  it('draws a 0 ms span as a marker, in the danger tone when it failed', () => {
    render(<TraceWaterfall spans={spans} />);
    const bar = screen.getByRole('img', { name: /^guard:/ }).firstElementChild!;
    expect(bar).toHaveClass('instant', 't-error');
  });

  it('indents children by depth from parent_span_id', () => {
    render(<TraceWaterfall spans={spans} />);
    const indent = (name: string) => (screen.getByText(name, { selector: '.wf-name' }) as HTMLElement).style.paddingLeft;
    expect(indent('POST /v1/messages')).toBe('0px');
    expect(indent('auth')).toBe('12px');
    expect(indent('guard')).toBe('24px');
  });

  it('renders round axis ticks and a type legend', () => {
    const { container } = render(<TraceWaterfall spans={spans} />);
    expect([...container.querySelectorAll('.wf-axis small')].map((t) => t.textContent)).toEqual(['0 ms', '2 s', '4 s', '6 s', '8 s']);
    expect(container.querySelector('.wf-legend')).toHaveTextContent('GenerationSpanToolError');
  });
});

it('draws a session as model turns with the tools run between them', () => {
  const turn = (id: string, start: string, end: string, tools: string[]) => ({ trace_id: id, started_at: start, ended_at: end, model: 'ackstorm.smart', input_tokens: 1, output_tokens: 1, cost: 0, duration_ms: 1, status: 'unknown' as const, tools });
  const spans = sessionSpans([turn('t1', '2026-09-20T12:00:00Z', '2026-09-20T12:00:02Z', ['Read', 'Read', 'Bash', 'Edit', 'Grep']), turn('t2', '2026-09-20T12:00:05Z', '2026-09-20T12:00:06Z', ['Bash'])]);
  // The last turn's tools have no next turn to bound them, so no tool bar.
  expect(spans.map((s) => [s.type, s.name, s.start_time, s.end_time])).toEqual([
    ['GENERATION', 'Model · ackstorm.smart', '2026-09-20T12:00:00Z', '2026-09-20T12:00:02Z'],
    ['TOOL', 'Tools · Read, Bash, Edit +1', '2026-09-20T12:00:02Z', '2026-09-20T12:00:05Z'],
    ['GENERATION', 'Model · ackstorm.smart', '2026-09-20T12:00:05Z', '2026-09-20T12:00:06Z'],
  ]);
});
