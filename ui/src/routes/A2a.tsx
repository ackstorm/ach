// A2a.tsx — the #/a2a page (nav: KEYS · MODELS · MCPs · A2A · STATS · HOW-TO).
//
// Read-only list of the Agent-to-Agent (A2A) agents registered on the gateway,
// sourced from GET /api/session/a2a (server-side LiteLLM /v1/agents, projected to
// a PUBLIC subset — no headers/params). Per agent it shows name + description,
// version pill, endpoint URL, transport + streaming chips, and the agent's skills.
//
// States: per-page error+retry card (useA2a throws on non-200); a calm "A2A not
// enabled" state when the deployment's LiteLLM has no A2A gateway (a valid 200 with
// available:false); and an empty "no agents configured" state. The page is
// presentational — the hook owns the fetch. Mirrors routes/Mcp.tsx.

import { useState } from 'react';
import { Bot, ExternalLink } from 'lucide-react';

import { RequiresDefaultKey } from '@/components/layout/RequiresDefaultKey';
import { Skeleton } from '@/components/ui/skeleton';
import { TableSearch, matchesSearch } from '@/components/ui/table-search';
import { useHasDefaultKey } from '@/hooks/use-keys';
import { useA2a } from '@/hooks/use-a2a';
import { useSessionStore } from '@/stores/session';
import type { A2aAgentRow } from '@/lib/api-types';

const EM_DASH = '—';

// Where to invoke an A2A agent: the gateway base (me.endpoint, e.g.
// https://api.<domain>) + /a2a/<name>. Mirrors HowTo's apiBase fallback so the
// card still shows a sensible host before the session endpoint resolves.
const FALLBACK_API_BASE = 'https://api.your-domain.example';

// LiteLLM how-to for calling A2A agents (shown as a header link).
const A2A_DOCS_URL = 'https://docs.litellm.ai/docs/a2a_invoking_agents';

const PAGE_TITLE = 'A2A';
const PAGE_SUB =
  'Agent-to-Agent agents registered on the gateway — autonomous agents your apps can call.';
const ERR_HEADING = "Couldn't load A2A agents";
const ERR_BODY =
  "We couldn't reach the A2A gateway. Check your connection and retry.";
const UNAVAIL_HEADING = 'A2A gateway not enabled';
const UNAVAIL_BODY =
  'This deployment has no A2A gateway configured. Once agents are added, they will appear here.';
const EMPTY_HEADING = 'No A2A agents yet';
const EMPTY_BODY =
  'The A2A gateway is enabled but no agents are configured for your access level.';

// A small neutral version pill (sits where the MCP status pill sits).
function VersionPill({ version }: { version: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface-elevated px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wider text-text-secondary">
      v{version}
    </span>
  );
}

// A neutral meta chip (transport / streaming / skills).
function MetaChip({ label, value }: { label: string; value: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-md border border-border px-1.5 py-0.5 font-mono text-[10px] tracking-wide text-text-secondary">
      <span className="text-text-tertiary">{label}</span>
      <span className="font-semibold text-text-primary">{value}</span>
    </span>
  );
}

function AgentCard({ agent, apiBase }: { agent: A2aAgentRow; apiBase: string }) {
  // The public invoke URL on the gateway: {BASE}/a2a/{name}. Falls back to the
  // agent id when the card omits a name; the row is dropped only if both are absent.
  const slug = agent.name ?? agent.id;
  const invokeUrl = slug ? `${apiBase}/a2a/${slug}` : null;
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-border bg-surface p-5">
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
        {agent.version && <VersionPill version={agent.version} />}
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
        {agent.transport && <MetaChip label="via" value={agent.transport} />}
        {agent.streaming && <MetaChip label="stream" value="yes" />}
        <MetaChip label="skills" value={String(agent.skill_count)} />
      </div>

      {/* Skills */}
      {agent.skills.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {agent.skills.map((s) => (
            <span
              key={s}
              className="inline-flex items-center rounded-md border border-border bg-surface-elevated px-1.5 py-0.5 font-mono text-[10px] text-text-secondary"
            >
              {s}
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

export function A2a() {
  // Per-user catalog: gated on a default key (the gateway scopes the read through
  // it). No default → render the prompt and DON'T fetch (useA2a disabled).
  const hasDefault = useHasDefaultKey();
  const query = useA2a(hasDefault);
  const me = useSessionStore((s) => s.me);
  const apiBase = me?.endpoint || FALLBACK_API_BASE;
  const [search, setSearch] = useState('');
  const header = (
    <div>
      <h1 className="font-sans text-2xl font-semibold leading-snug text-text-primary">
        {PAGE_TITLE}
      </h1>
      <p className="mt-1 max-w-2xl font-sans text-sm text-text-secondary">
        {PAGE_SUB}
      </p>
      <a
        href={A2A_DOCS_URL}
        target="_blank"
        rel="noopener noreferrer"
        className="mt-2 inline-flex items-center gap-1.5 font-mono text-[11px] font-semibold uppercase tracking-wide text-primary transition-colors hover:text-text-primary"
      >
        <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
        How to invoke A2A agents
      </a>
    </div>
  );

  // No default key → calm "set a default" prompt (no request fired).
  if (!hasDefault) {
    return (
      <div className="flex flex-col gap-8">
        {header}
        <RequiresDefaultKey subject="A2A agents" />
      </div>
    );
  }

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
  const agents = data?.agents ?? [];
  const visible = agents.filter((a) => matchesSearch(search, a.name, a.description));

  return (
    <div className="flex flex-col gap-8">
      {header}

      {query.isPending ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <Skeleton variant="card" />
          <Skeleton variant="card" />
        </div>
      ) : data && data.available === false ? (
        <StateCard heading={UNAVAIL_HEADING} body={UNAVAIL_BODY} />
      ) : agents.length === 0 ? (
        <StateCard heading={EMPTY_HEADING} body={EMPTY_BODY} />
      ) : (
        <div className="flex flex-col gap-4">
          <div className="flex flex-wrap items-center gap-3">
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
