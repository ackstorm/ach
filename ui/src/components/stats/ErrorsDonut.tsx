// ErrorsDonut.tsx — request-outcome (success vs failure) donut for #/stats.
//
// The feasible analogue of the reference dashboard's "Errors" donut: LiteLLM
// /spend/logs carries a `status` ("success" | "failure"), not HTTP codes, so this
// splits requests by OUTCOME, with the failure count centered (mirrors the
// reference's "Total Errors" center). Sourced from GET /api/session/latency
// (outcomes[]), same fixed 7-day window as LatencyPanel.
//
// PURE presentational leaf (no fetch). Recharts <PieChart>, matching UsageDonut's
// geometry. success → accent green, failure → destructive red, any other status →
// neutral. Renders a calm state when unavailable / empty.

import { Cell, Pie, PieChart, ResponsiveContainer } from 'recharts';

import type { LatencyResponse } from '@/lib/api-types';
import { formatInt } from '@/lib/format';

const EM_DASH = '—';
const UNAVAILABLE_COPY = 'Outcome data is not available for this deployment.';
const EMPTY_COPY = 'No requests in this range.';
const CENTER_CAPTION = 'FAILED';

const DONUT_HEIGHT = 200;
const INNER_RADIUS = 56;
const OUTER_RADIUS = 80;

const FILL_SUCCESS = 'var(--primary)';
const FILL_FAILURE = 'var(--destructive)';
const FILL_OTHER = 'var(--text-tertiary)';

function outcomeFill(status: string): string {
  const s = status.toLowerCase();
  if (s === 'success') return FILL_SUCCESS;
  if (s === 'failure') return FILL_FAILURE;
  return FILL_OTHER;
}

function StatePanel({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-40 flex-col items-center justify-center gap-2 rounded-xl border border-border bg-surface p-5 text-center">
      <div className="font-sans text-sm text-text-secondary">{children}</div>
    </div>
  );
}

export interface ErrorsDonutProps {
  data: LatencyResponse | undefined;
  isError: boolean;
}

export function ErrorsDonut({
  data,
  isError,
}: ErrorsDonutProps): React.ReactElement {
  if (isError || !data || !data.available) {
    return <StatePanel>{UNAVAILABLE_COPY}</StatePanel>;
  }
  const outcomes = data.outcomes ?? [];
  const total = outcomes.reduce((acc, o) => acc + o.count, 0);
  if (total === 0) {
    return <StatePanel>{EMPTY_COPY}</StatePanel>;
  }

  const failed = outcomes
    .filter((o) => o.status.toLowerCase() !== 'success')
    .reduce((acc, o) => acc + o.count, 0);

  return (
    <div className="flex flex-wrap items-center gap-8 rounded-xl border border-border bg-surface p-5">
      <div className="relative h-50 w-50 shrink-0">
        <ResponsiveContainer width="100%" height={DONUT_HEIGHT}>
          <PieChart>
            <Pie
              data={outcomes}
              dataKey="count"
              nameKey="status"
              cx="50%"
              cy="50%"
              innerRadius={INNER_RADIUS}
              outerRadius={OUTER_RADIUS}
              startAngle={90}
              endAngle={-270}
              stroke="none"
              isAnimationActive={false}
            >
              {outcomes.map((o) => (
                <Cell key={o.status} fill={outcomeFill(o.status)} />
              ))}
            </Pie>
          </PieChart>
        </ResponsiveContainer>
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-1">
          <div className="font-sans text-2xl font-semibold leading-tight text-text-primary tabular-nums">
            {formatInt(failed)}
          </div>
          <div className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
            {CENTER_CAPTION}
          </div>
        </div>
      </div>
      <ul className="flex flex-1 basis-50 flex-col gap-2">
        {outcomes.map((o) => {
          const pct = total > 0 ? ((o.count / total) * 100).toFixed(1) : null;
          return (
            <li
              key={o.status}
              className="grid grid-cols-[auto_1fr_auto_auto] items-center gap-2 rounded-lg px-2 py-1"
            >
              <span
                className="size-2.5 shrink-0 rounded-full"
                style={{ background: outcomeFill(o.status) }}
                aria-hidden="true"
              />
              <span className="truncate font-mono text-xs capitalize text-text-primary">
                {o.status}
              </span>
              <span className="font-mono text-xs text-text-primary tabular-nums">
                {formatInt(o.count)}
              </span>
              <span className="font-mono text-xs text-text-secondary tabular-nums">
                {pct === null ? EM_DASH : `${pct}%`}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
