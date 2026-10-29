// A2a.tsx — the #/a2a page (nav: KEYS · MODELS · MCPs · A2A · STATS · HOW-TO).
//
// Read-only list of the Agent-to-Agent (A2A) agents registered on the gateway,
// sourced from GET /platform/console/capabilities?scope=personal (a2a_agents, a
// PUBLIC projection — no headers/params). Per agent it shows name + description,
// version pill, endpoint URL, transport + streaming chips, and the agent's skills.
//
// States: per-page error+retry card (useA2a throws on non-200); a calm
// "provisioning" card while the personal credential is still being set up
// (provisioning:true); and an empty "no agents configured" state. The page is
// presentational — the hook owns the fetch. Mirrors routes/Mcp.tsx.

import { useState } from 'react';
import { Bot, ExternalLink } from 'lucide-react';

import { Skeleton } from '@/components/ui/skeleton';
import { Tag } from '@/components/ui/Tag';
import { TableSearch, matchesSearch } from '@/components/ui/table-search';
import { useA2a } from '@/hooks/use-capabilities';
import { CatalogHead, CatalogState } from '@/components/catalog/Catalog';
import { useSessionStore } from '@/stores/session';
import type { A2aAgentRow } from '@/lib/api-types';

const EM_DASH = '—';

// Where to invoke an A2A agent: the gateway base (me.endpoint, e.g.
// https://api.<domain>) + /a2a/<name>. Mirrors HowTo's apiBase fallback so the
// card still shows a sensible host before the session endpoint resolves.
const FALLBACK_API_BASE = 'https://api.your-domain.example';

const PAGE_TITLE = 'A2A';
const PAGE_SUB =
  'Agent-to-Agent agents registered on the gateway — autonomous agents your apps can call.';
const ERR_HEADING = "Couldn't load A2A agents";
const ERR_BODY =
  "We couldn't reach the A2A gateway. Check your connection and retry.";
const UNAVAIL_HEADING = 'A2A gateway not enabled';
const UNAVAIL_BODY =
  'This deployment has no A2A gateway configured. Once agents are added, they will appear here.';
const PROVISIONING_HEADING = 'Access is being provisioned';
const PROVISIONING_BODY =
  'Your personal catalog appears after the next Environment sync.';

function AgentCard({ agent, apiBase }: { agent: A2aAgentRow; apiBase: string }) {
  // The public invoke URL on the gateway: {BASE}/a2a/{name}. Falls back to the
  // agent id when the card omits a name; the row is dropped only if both are absent.
  const slug = agent.name ?? agent.id;
  const invokeUrl = slug ? `${apiBase}/a2a/${slug}` : null;
  return (
    <div className="flex flex-col gap-3 panel p-5">
      {/* Head: name + version */}
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2.5">
          <span className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-border">
            <Bot className="size-4 text-primary" aria-hidden="true" />
          </span>
          <span className="font-sans text-sm font-semibold text-text-primary">
            {agent.name ?? agent.id ?? EM_DASH}
          </span>
        </div>
        {agent.version && <Tag>v{agent.version}</Tag>}
      </div>

      {/* Description */}
      {agent.description && (
        <p className="font-sans text-sm leading-relaxed text-text-secondary">
          {agent.description}
        </p>
      )}

      {/* Invoke URL — {BASE}/a2a/{name} on the gateway */}
      {invokeUrl && (
        <div className="flex items-center gap-1.5 break-all font-mono text-[11px] text-text-tertiary">
          <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
          {invokeUrl}
        </div>
      )}

      {/* Meta chips: transport + streaming + skills count */}
      <div className="flex flex-wrap gap-1.5">
        {agent.transport && <Tag><span className="k">via</span><b>{agent.transport}</b></Tag>}
        {agent.streaming && <Tag><span className="k">stream</span><b>yes</b></Tag>}
        <Tag><span className="k">skills</span><b>{agent.skill_count}</b></Tag>
      </div>

      {/* Skills */}
      {agent.skills.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {agent.skills.map((s) => (
            <Tag key={s}>{s}</Tag>
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

export function A2a() {
  // ponytail: the alitellm-auth default-key gate had no ACH equivalent (env
  // keys are the scoping unit, not a "default" key) — always fetch.
  const query = useA2a();
  const me = useSessionStore((s) => s.me);
  const apiBase = me?.endpoint || FALLBACK_API_BASE;
  const [search, setSearch] = useState('');
  const agents = query.data?.agents ?? [];
  const header = <CatalogHead title={PAGE_TITLE} description={PAGE_SUB} count={agents.length} />;

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
  const visible = agents.filter((a) => matchesSearch(search, a.name, a.description));

  return (
    <div className="catalog-page flex flex-col gap-6">
      {header}

      {query.isPending ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <Skeleton variant="card" />
          <Skeleton variant="card" />
        </div>
      ) : agents.length === 0 && data?.provisioning ? (
        <StateCard heading={PROVISIONING_HEADING} body={PROVISIONING_BODY} />
      ) : agents.length === 0 ? (
        <StateCard heading={UNAVAIL_HEADING} body={UNAVAIL_BODY} />
      ) : (
        <div className="flex flex-col gap-3">
          <div className="toolbar">
            <TableSearch value={search} onChange={setSearch} placeholder="Search agents…" />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            {visible.map((a, i) => (
              <AgentCard key={a.id ?? i} agent={a} apiBase={apiBase} />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
