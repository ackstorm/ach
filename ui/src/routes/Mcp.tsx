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
import { StatusPill } from '@/components/ui/StatusPill';
import { Tag } from '@/components/ui/Tag';
import { TableSearch, matchesSearch } from '@/components/ui/table-search';
import { FilterChips, toggled } from '@/components/ui/FilterChips';
import { useMcp } from '@/hooks/use-capabilities';
import { CatalogHead, CatalogState } from '@/components/catalog/Catalog';
import type { McpServerRow } from '@/lib/api-types';
import { mcpStatus } from '@/lib/status';
import { formatInt } from '@/lib/format';

const EM_DASH = '—';

const PAGE_TITLE = 'MCPs';
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

function ServerCard({ server }: { server: McpServerRow }) {
  return (
    <div className="panel flex flex-col gap-3 p-5">
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
        <StatusPill tone={mcpStatus(server.status).tone}>{mcpStatus(server.status).label}</StatusPill>
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
        {server.transport && <Tag><span className="k">via</span><b>{server.transport}</b></Tag>}
        {server.auth_type && (
          <Tag title="Auth between the gateway and this server. Your gateway access is always key-authed.">
            <span className="k">server auth</span><b>{server.auth_type}</b>
          </Tag>
        )}
        {server.tool_count > 0 ? (
          <Tag><b>{server.tool_count}</b> {server.tool_count === 1 ? 'tool' : 'tools'}</Tag>
        ) : (
          // A zero count here is indistinguishable from LiteLLM's non-admin
          // endpoint simply not reporting tools — don't claim "0 tools".
          <Tag dim>tools not listed</Tag>
        )}
      </div>

      {/* Tools — cap the visible chips so a chatty server doesn't dominate. */}
      {server.tools.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {server.tools.slice(0, 6).map((t) => (
            <Tag key={t}>{t}</Tag>
          ))}
          {server.tools.length > 6 && (
            <span className="inline-flex items-center rounded-md px-1.5 py-0.5 font-mono text-[10px] text-text-tertiary">
              +{server.tools.length - 6} more
            </span>
          )}
        </div>
      )}

      {/* Gateway access groups (NOT ACH Environments — a separate gateway-side
          grouping the server is wired into). */}
      {server.access_groups.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 border-t border-border pt-3">
          <span className="font-mono text-[10px] font-semibold uppercase tracking-wider text-text-tertiary">
            Access groups
          </span>
          {server.access_groups.map((g) => (
            <Tag key={g}>{g}</Tag>
          ))}
        </div>
      )}
    </div>
  );
}

// A centered state card (unavailable / empty), shared shape.
function StateCard({ heading, body }: { heading: string; body: string }) {
  return <CatalogState heading={heading}>{body}</CatalogState>;
}

export function Mcp() {
  // ponytail: the alitellm-auth default-key gate had no ACH equivalent (env
  // keys are the scoping unit, not a "default" key) — always fetch.
  const query = useMcp();
  const [search, setSearch] = useState('');
  const [groups, setGroups] = useState<Set<string>>(new Set());
  const servers = query.data?.servers ?? [];
  const header = <CatalogHead title={PAGE_TITLE} description={PAGE_SUB} count={servers.length} />;

  // Error (502 / network).
  if (query.isError) {
    return (
      <div className="catalog-page">
        {header}
        <CatalogState heading={ERR_HEADING}>
          {ERR_BODY}
          <br />
          <button type="button" className="btn" onClick={() => void query.refetch()}>
            retry
          </button>
        </CatalogState>
      </div>
    );
  }

  const data = query.data;
  // Search AND any selected access group (OR across groups).
  const visible = servers.filter((s) => matchesSearch(search, s.name, s.description) && (!groups.size || s.access_groups.some((g) => groups.has(g))));
  const counts = new Map<string, number>();
  for (const s of servers) for (const g of s.access_groups) counts.set(g, (counts.get(g) ?? 0) + 1);
  const groupOptions = [...counts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([key, n]) => ({ key, label: `${key} · ${formatInt(n)}` }));

  return (
    <div className="catalog-page flex flex-col gap-6">
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
        <div className="flex flex-col gap-3">
          <div className="toolbar">
            <TableSearch value={search} onChange={setSearch} placeholder="Search servers…" />
            {groupOptions.length > 0 && <div className="filter-set"><FilterChips label="Access groups" allLabel="All access groups" options={groupOptions} active={groups} onToggle={(k) => setGroups(toggled(groups, k))} onClear={() => setGroups(new Set())} max={5} /></div>}
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
