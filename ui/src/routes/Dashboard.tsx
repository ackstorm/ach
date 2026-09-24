// Dashboard.tsx — the #/ authenticated content CONTAINER, ported from
// src/ui/dashboard.js (DASH-01 + the numeric half of DASH-06). Preact + htm ->
// React + Tailwind tokens.
//
// Composes the Phase-3 pieces into the live dashboard:
//   • the top row (greeting + endpoint chip, DASH-01),
//   • the four-tile metric header (DASH-06),
//   • the API KEYS section with the `+ New Key` CTA (which opens the SAME
//     store-driven create modal as the sidebar shortcut), and the KeysTable,
//   • the prop-driven DeleteKeyModal (the dashboard owns `keyToDelete`).
//
// The CREATE modal + Toaster are mounted in AppShell (so the sidebar can also
// open the create modal); the dashboard only triggers openModal().
//
// SECURITY INVARIANTS (threat register 10-05):
//   • T-10-14 (XSS): me.name / me.endpoint render as React text
//     children (auto-escaped). No dangerouslySetInnerHTML anywhere.
//   • T-10-16 (info disclosure, endpoint Copy): the Copy button writes only
//     me.endpoint (a public base URL), on an explicit user click.

import { useMemo, useState } from 'react';
import { Key } from 'lucide-react';

import { DeleteKeyModal } from '@/components/keys/DeleteKeyModal';
import { KeysTable } from '@/components/keys/KeysTable';
import { KpiRow } from '@/components/stats/KpiRow';
import { useCopyFeedback } from '@/hooks/use-copy-feedback';
import { useEnvironments } from '@/hooks/use-environments';
import { useKeys } from '@/hooks/use-keys';
import { useStats } from '@/hooks/use-stats';
import type { KeyRow, SessionMe } from '@/lib/api-types';
import { formatInt } from '@/lib/format';
import { selectKeyRows } from '@/lib/keys';
import { presetToRange } from '@/lib/stats-presets';
import { teamColorVar } from '@/lib/team-color';
import { cn } from '@/lib/utils';
import { useCreateKeyModalStore } from '@/stores/create-key-modal';

// The em-dash placeholder (matches format.ts EM_DASH, U+2014). The two
// stats-derived figures degrade to it; Active keys reads it while the keys
// query is pending/errored so we never show a misleading 0.
const EM_DASH = '—';

export interface DashboardProps {
  me: SessionMe;
}

// ── EndpointChip ──────────────────────────────────────────────────────────────
// DASH-01 right block: `endpoint` caption + the API base URL + a Copy button.
// Copy writes ONLY the public endpoint URL on an explicit click; the button text
// flips to `copied!` for 2s (useCopyFeedback).
function EndpointChip({ endpoint }: { endpoint: string }) {
  const { copied, copy } = useCopyFeedback();
  return (
    <div className="flex items-center gap-3 rounded-xl border border-border bg-surface px-4 py-2">
      <div className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-tertiary">
        endpoint
      </div>
      <div className="break-all font-mono text-xs text-text-primary">
        {endpoint || EM_DASH}
      </div>
      <button
        type="button"
        onClick={() => endpoint && void copy(endpoint)}
        className={cn(
          'cursor-pointer rounded-lg border px-2 py-1 font-mono text-[11px] font-semibold lowercase tracking-wide transition-colors',
          copied
            ? // While "copied!" is showing: FILLED green background + green border/text.
              'border-primary bg-primary/15 text-primary'
            : // Idle: neutral, with the green hover affordance returning once the
              // 2s feedback expires.
              'border-border text-text-tertiary hover:border-primary hover:text-primary'
        )}
      >
        {copied ? 'copied!' : 'Copy'}
      </button>
    </div>
  );
}

// ── KeysEnvironmentsTile ─────────────────────────────────────────────────────
// The 4th KPI-row cell on the KEYS tab (swaps in for AVG COST). Shares the
// KpiCard card chrome (see stats/KpiRow) so it reads as the same component: a
// tinted Key accent-chip + 11px caption, the active-key COUNT as the 24px value,
// and — below — every Environment the caller can access (GET
// /platform/environments, already filtered server-side), NOT just the ones
// their keys point at. The first MAX_ENV_PILLS render as colored pills; the
// rest collapse into a "+N" pill whose hover title lists them. Count is
// EM_DASH until the keys query resolves; pills fall back to EM_DASH when the
// caller has no Environment.
const MAX_ENV_PILLS = 6;

const PILL_CLASS =
  'inline-flex items-center gap-1.5 rounded-md border border-border bg-surface-elevated px-2 py-0.5 font-sans text-xs font-medium text-text-secondary';

function KeysEnvironmentsTile({
  keyRows,
  environments,
  fallback,
}: {
  keyRows: KeyRow[] | null;
  environments: string[];
  fallback: string;
}) {
  const active = keyRows ? keyRows.filter((k) => k.status !== 'revoked') : null;
  const count = active ? formatInt(active.length) : EM_DASH;
  const shown = environments.slice(0, MAX_ENV_PILLS);
  const hidden = environments.slice(MAX_ENV_PILLS);
  return (
    <div className="flex flex-col gap-2 rounded-xl border border-border bg-surface p-5">
      <div className="flex items-center gap-2">
        <span className="inline-flex size-[26px] shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Key className="size-[15px]" aria-hidden="true" />
        </span>
        <div className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
          KEYS &amp; ENVIRONMENTS
        </div>
      </div>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <span className="font-sans text-2xl font-semibold leading-tight text-text-primary">
          {count}
        </span>
        {active ? (
          <span className="font-mono text-[10px] text-text-tertiary">
            {active.length === 1 ? 'key' : 'keys'}
          </span>
        ) : null}
      </div>
      {environments.length > 0 ? (
        <div className="flex flex-wrap gap-1.5">
          {shown.map((name, i) => (
            <span key={name} data-slot="environment-pill" className={PILL_CLASS}>
              <span
                className="size-2 shrink-0 rounded-full"
                style={{ background: teamColorVar(i) }}
                aria-hidden="true"
              />
              {name}
            </span>
          ))}
          {hidden.length > 0 ? (
            <span data-slot="environment-more" className={PILL_CLASS} title={hidden.join(', ')}>
              +{hidden.length}
            </span>
          ) : null}
        </div>
      ) : (
        <div className="break-words font-sans text-xs text-text-secondary">
          {fallback}
        </div>
      )}
    </div>
  );
}

export function Dashboard({ me }: DashboardProps) {
  const query = useKeys();
  const { data: environments = [] } = useEnvironments();
  const openModal = useCreateKeyModalStore((s) => s.openModal);

  // The dashboard owns the delete target; KeysTable's per-row revoke action
  // sets it via onDelete, and DeleteKeyModal is prop-driven by it.
  const [keyToDelete, setKeyToDelete] = useState<KeyRow | null>(null);

  // ── Metric tiles (DASH-06) ──────────────────────────────────────────────────
  // The top row is the STATS KPI cards (requests/tokens/spend — with deltas +
  // sub-notes) over the month-to-date range, plus a combined keys+teams tile in
  // the 4th slot. The range is computed once on mount (a fresh `new Date()` each
  // render would thrash the query key). Pending/errored figures degrade to
  // EM_DASH inside KpiCard automatically, so no per-tile guards are needed here.
  const mtdRange = useMemo(() => presetToRange('This month', new Date()), []);
  const stats = useStats(mtdRange);

  return (
    <div className="flex flex-col gap-8">
      {/* DASH-01: greeting + endpoint chip (full width) */}
      <div className="flex flex-wrap items-start justify-between gap-5">
        <div className="font-sans text-2xl font-semibold leading-snug text-text-primary">
          Welcome back, <span className="text-primary">{me.name || EM_DASH}</span>
        </div>
        <EndpointChip endpoint={me.endpoint} />
      </div>

      {/* DASH-06: metric header — the STATS KPI cards (requests/tokens/spend,
          with deltas) over MTD, with a combined keys+environments tile in the
          4th slot in place of AVG COST. The Stats page defaults to a rolling
          7d window instead — this caption exists so the two don't read as
          contradictory. */}
      <div className="flex flex-col gap-2">
        <p className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-tertiary">
          This month
        </p>
        <KpiRow
          totals={stats.isSuccess ? stats.data?.totals : undefined}
          series={stats.isSuccess ? stats.data?.series : undefined}
          fourthCard={
            <KeysEnvironmentsTile
              keyRows={
                query.isSuccess && query.data ? selectKeyRows(query.data) : null
              }
              environments={environments.map((e) => e.name)}
              fallback={EM_DASH}
            />
          }
        />
      </div>

      {/* DASH-02: API KEYS section — full width (the dashboard has no sidebar) */}
      <div className="flex min-w-0 flex-col gap-5">
        <div className="flex flex-wrap items-end justify-between gap-5">
          <div>
            <h1 className="font-sans text-2xl font-semibold leading-snug text-text-primary">
              API Keys
            </h1>
            <p className="mt-1 max-w-2xl font-sans text-sm text-text-secondary">
              Create and manage your ACH environment keys.
            </p>
          </div>
          <button
            type="button"
            onClick={openModal}
            className="inline-flex cursor-pointer items-center justify-center rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground transition-opacity hover:opacity-90"
          >
            + New Key
          </button>
        </div>
        <KeysTable onDelete={setKeyToDelete} />
      </div>

      <DeleteKeyModal keyToDelete={keyToDelete} onClose={() => setKeyToDelete(null)} />
    </div>
  );
}
