// Mcp.tsx — the #/mcp page (nav: KEYS · STATS · MODELS · MCP · HOW-TO).
//
// Read-only list of the Model Context Protocol servers wired into the gateway,
// sourced from GET /platform/console/capabilities?scope=personal (mcp_servers, a
// PUBLIC projection — no credentials/env/headers). Per server it shows name +
// description, status pill, endpoint URL, transport + auth-type chips, the exposed
// tools, and access groups.
//
// States: per-page error+retry card (useMcp throws on non-200); a calm
// "provisioning" card while the personal credential is still being set up
// (provisioning:true); and an empty "no servers configured" state. The page is
// presentational — the hook owns the fetch.

import { useState } from 'react';
import { Boxes, ExternalLink } from 'lucide-react';

import { Skeleton } from '@/components/ui/skeleton';
import { TableSearch, matchesSearch } from '@/components/ui/table-search';
import { useMcp } from '@/hooks/use-capabilities';
import type { McpServerRow } from '@/lib/api-types';
import { cn } from '@/lib/utils';

const EM_DASH = '—';

const PAGE_TITLE = 'MCP';
const PAGE_SUB =
  'Model Context Protocol servers wired into the gateway — the tools your models can call.';
const ERR_HEADING = "Couldn't load MCP servers";
const ERR_BODY =
  "We couldn't reach the MCP gateway. Check your connection and retry.";
const UNAVAIL_HEADING = 'MCP gateway not enabled';
const UNAVAIL_BODY =
  'This deployment has no MCP gateway configured. Once servers are added, they will appear here.';
const PROVISIONING_HEADING = 'Access is being provisioned';
const PROVISIONING_BODY =
  'Your personal catalog appears after the next Environment sync.';

// Status pill: healthy=green, unhealthy=destructive, unknown/other=neutral.
function StatusPill({ status }: { status: string | null }) {
  const s = (status ?? 'unknown').toLowerCase();
  const tone =
    s === 'healthy'
      ? 'border-primary/40 bg-primary/10 text-primary'
      : s === 'unhealthy'
        ? 'border-destructive/40 bg-destructive/10 text-destructive'
        : 'border-border bg-surface-elevated text-text-secondary';
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wider',
        tone,
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          'size-1.5 rounded-full',
          s === 'healthy'
            ? 'bg-primary'
            : s === 'unhealthy'
              ? 'bg-destructive'
              : 'bg-text-tertiary',
        )}
      />
      {status ?? 'unknown'}
    </span>
  );
}

// A neutral meta chip (transport / auth type).
function MetaChip({ label, value }: { label: string; value: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-md border border-border px-1.5 py-0.5 font-mono text-[10px] tracking-wide text-text-secondary">
      <span className="text-text-tertiary">{label}</span>
      <span className="font-semibold text-text-primary">{value}</span>
    </span>
  );
}

function ServerCard({ server }: { server: McpServerRow }) {
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-border bg-surface p-5">
      {/* Head: name + status */}
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2.5">
          <span className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-border">
            <Boxes className="size-4 text-primary" aria-hidden="true" />
          </span>
          <span className="font-sans text-sm font-semibold text-text-primary">
            {server.name ?? server.id ?? EM_DASH}
          </span>
        </div>
        <StatusPill status={server.status} />
      </div>

      {/* Description */}
      {server.description && (
        <p className="font-sans text-sm leading-relaxed text-text-secondary">
          {server.description}
        </p>
      )}

      {/* Endpoint URL */}
      {server.url && (
        <div className="flex items-center gap-1.5 break-all font-mono text-[11px] text-text-tertiary">
          <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
          {server.url}
        </div>
      )}

      {/* Meta chips: transport + auth */}
      <div className="flex flex-wrap gap-1.5">
        {server.transport && <MetaChip label="via" value={server.transport} />}
        {server.auth_type && (
          <span
            title="Auth between the gateway and this server. Your gateway access is always key-authed."
            className="inline-flex items-center gap-1 rounded-md border border-border px-1.5 py-0.5 font-mono text-[10px] tracking-wide text-text-secondary"
          >
            <span className="text-text-tertiary">server auth</span>
            <span className="font-semibold text-text-primary">{server.auth_type}</span>
          </span>
        )}
        {server.tool_count > 0 ? (
          <span className="inline-flex items-center gap-1 rounded-md border border-primary/40 bg-primary/10 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-primary">
            {server.tool_count} {server.tool_count === 1 ? 'tool' : 'tools'}
          </span>
        ) : (
          // A zero count here is indistinguishable from LiteLLM's non-admin
          // endpoint simply not reporting tools — don't claim "0 tools".
          <span className="inline-flex items-center gap-1 rounded-md border border-border px-1.5 py-0.5 font-mono text-[11px] text-text-tertiary">
            tools not listed
          </span>
        )}
      </div>

      {/* Tools — cap the visible chips so a chatty server doesn't dominate. */}
      {server.tools.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {server.tools.slice(0, 6).map((t) => (
            <span
              key={t}
              className="inline-flex items-center rounded-md border border-border bg-surface-elevated px-1.5 py-0.5 font-mono text-[10px] text-text-secondary"
            >
              {t}
            </span>
          ))}
          {server.tools.length > 6 && (
            <span className="inline-flex items-center rounded-md px-1.5 py-0.5 font-mono text-[10px] text-text-tertiary">
              +{server.tools.length - 6} more
            </span>
          )}
        </div>
      )}

      {/* LiteLLM access groups (NOT ACH Environments — a separate LiteLLM-side
          grouping the server is wired into). */}
      {server.access_groups.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 border-t border-border pt-3">
          <span className="font-mono text-[10px] font-semibold uppercase tracking-wider text-text-tertiary">
            LiteLLM access groups
          </span>
          {server.access_groups.map((g) => (
            <span
              key={g}
              className="inline-flex items-center rounded-md border border-border px-1.5 py-0.5 font-mono text-[10px] text-text-secondary"
            >
              {g}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

// A centered state card (unavailable / empty), shared shape.
function StateCard({ heading, body }: { heading: string; body: string }) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-xl border border-border bg-surface p-12 text-center">
      <div className="font-sans text-lg font-semibold text-text-primary">
        {heading}
      </div>
      <div className="max-w-md font-sans text-sm text-text-secondary">{body}</div>
    </div>
  );
}

export function Mcp() {
  // ponytail: the alitellm-auth default-key gate had no ACH equivalent (env
  // keys are the scoping unit, not a "default" key) — always fetch.
  const query = useMcp();
  const [search, setSearch] = useState('');
  const header = (
    <div>
      <h1 className="font-sans text-2xl font-semibold leading-snug text-text-primary">
        {PAGE_TITLE}
      </h1>
      <p className="mt-1 max-w-2xl font-sans text-sm text-text-secondary">
        {PAGE_SUB}
      </p>
    </div>
  );

  // Error (502 / network).
  if (query.isError) {
    return (
      <div className="flex flex-col gap-8">
        {header}
        <div className="flex flex-col items-center gap-3 rounded-xl border border-border bg-surface p-12 text-center">
          <div className="font-sans text-2xl font-semibold text-text-primary">
            {ERR_HEADING}
          </div>
          <div className="max-w-md font-sans text-sm text-text-secondary">
            {ERR_BODY}
          </div>
          <button
            type="button"
            onClick={() => void query.refetch()}
            className="inline-flex cursor-pointer items-center justify-center rounded-lg border border-destructive px-4 py-2 font-mono text-xs font-semibold uppercase tracking-wide text-destructive transition-colors hover:bg-destructive/10"
          >
            retry
          </button>
        </div>
      </div>
    );
  }

  const data = query.data;
  const servers = data?.servers ?? [];
  const visible = servers.filter((s) => matchesSearch(search, s.name, s.description));

  return (
    <div className="flex flex-col gap-8">
      {header}

      {query.isPending ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <Skeleton variant="card" />
          <Skeleton variant="card" />
        </div>
      ) : servers.length === 0 && data?.provisioning ? (
        <StateCard heading={PROVISIONING_HEADING} body={PROVISIONING_BODY} />
      ) : servers.length === 0 ? (
        <StateCard heading={UNAVAIL_HEADING} body={UNAVAIL_BODY} />
      ) : (
        <div className="flex flex-col gap-4">
          <div className="flex flex-wrap items-center gap-3">
            <TableSearch value={search} onChange={setSearch} placeholder="Search servers…" />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            {visible.map((s, i) => (
              <ServerCard key={s.id ?? i} server={s} />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
