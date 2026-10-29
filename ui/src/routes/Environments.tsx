// Environments.tsx — the #/environments page (nav: … · A2A · ENVIRONMENTS · …).
//
// Read-only list of the Environments the caller can use, from GET
// /platform/environments (team-scoped for every caller, admins included). One
// collapsed card per Environment (native <details>): name, status badge,
// description and per-kind counts; expanding it shows the declared names, the
// hydrate command and — when the Environment is keyable — a "Create key"
// button that opens the shared create-key modal with it preselected.
//
// Admins get a "Show all environments" toggle: it reads the admin-only
// `?all=true` inventory and badges the rows outside the caller's own teams
// (they can be read and hydrated, but an ek_ can't be minted there — the
// mint path has no admin bypass, so those rows get no Create key button).

import { useState } from 'react';
import { ChevronRight } from 'lucide-react';

import { CatalogHead } from '@/components/catalog/Catalog';
import { Skeleton } from '@/components/ui/skeleton';
import { StatusPill } from '@/components/ui/StatusPill';
import { Tag } from '@/components/ui/Tag';
import { CodeBlock } from '@/components/ui/CodeBlock';
import { TableSearch, matchesSearch } from '@/components/ui/table-search';
import { useEnvironments } from '@/hooks/use-environments';
import type { EnvironmentRow } from '@/lib/api-types';
import { formatInt } from '@/lib/format';
import { degradedReason, isKeyable } from '@/lib/env-status';
import { ENV_STATUS, type Status } from '@/lib/status';
import { cn } from '@/lib/utils';
import { useCreateKeyModalStore } from '@/stores/create-key-modal';
import { useSessionStore } from '@/stores/session';

const PAGE_TITLE = 'Environments';
const PAGE_SUB =
  'Environments your teams can use. Each one bundles models, MCP servers and agents with plugins, skills and prompts. Hydrate one into a folder, or create an ek- key scoped to it.';
const EMPTY_HEADING = 'No environments yet';
const EMPTY_BODY =
  'None of your teams has access to an Environment. Ask an admin to add one of your teams to an Environment.';

// Status pill: Available / Degraded (keyable but not fully Available) / Not
// ready (not keyable) — the same split lib/env-status.ts gives the key picker.
function statusOf(env: EnvironmentRow): Status {
  if (!isKeyable(env)) return ENV_STATUS.notReady;
  if (env.status !== 'Available') return ENV_STATUS.degraded;
  return ENV_STATUS.available;
}

// The declared name groups, in display order. Counts on the summary row use
// the first five; the body lists every non-empty group.
function groupsOf(env: EnvironmentRow): { label: string; unit: string; names: string[] }[] {
  const r = env.runtime ?? {};
  const c = env.context ?? {};
  return [
    { label: 'Models', unit: 'model', names: r.models ?? [] },
    { label: 'MCP servers', unit: 'MCP', names: r.mcpServers ?? [] },
    { label: 'A2A agents', unit: 'agent', names: r.a2aAgents ?? [] },
    { label: 'Plugins', unit: 'plugin', names: c.plugins ?? [] },
    { label: 'Skills', unit: 'skill', names: c.skills ?? [] },
    { label: 'Guardrails', unit: 'guardrail', names: r.guardrails ?? [] },
    { label: 'Prompts', unit: 'prompt', names: c.prompts ?? [] },
    { label: 'Artifacts', unit: 'artifact', names: c.artifacts ?? [] },
  ];
}

function EnvironmentCard({ env, outsideTeams }: { env: EnvironmentRow; outsideTeams: boolean }) {
  const openModal = useCreateKeyModalStore((s) => s.openModal);
  const { tone, label } = statusOf(env);
  const groups = groupsOf(env);
  const degraded = degradedReason(env);
  const teams = env.authorizedTeams ?? [];

  return (
    <details className="group overflow-hidden panel open:border-primary/40">
      <summary className="grid cursor-pointer list-none grid-cols-[auto_1fr] items-center gap-x-4 gap-y-1.5 px-5 py-4 hover:bg-surface-hover md:grid-cols-[auto_1fr_auto] [&::-webkit-details-marker]:hidden">
        <ChevronRight
          aria-hidden="true"
          className="row-span-2 size-4 text-text-tertiary transition-transform group-open:rotate-90"
        />
        <div className="flex flex-wrap items-center gap-2.5 font-mono text-[15px] font-semibold text-text-primary">
          {env.name}
          <StatusPill tone={tone}>{label}</StatusPill>
          {outsideTeams && (
            <StatusPill tone={ENV_STATUS.outsideTeams.tone}>{ENV_STATUS.outsideTeams.label}</StatusPill>
          )}
        </div>
        <div className="col-start-2 flex flex-wrap gap-1.5 md:col-start-3 md:row-span-2 md:row-start-1 md:justify-end">
          {groups.slice(0, 5).map((g) => (
            <Tag key={g.unit} dim={!g.names.length} className="whitespace-nowrap">
              {g.names.length} {g.names.length === 1 ? g.unit : `${g.unit}s`}
            </Tag>
          ))}
        </div>
        <div
          className={cn(
            'col-start-2 font-sans text-sm',
            env.description ? 'text-text-secondary' : 'text-text-tertiary',
          )}
        >
          {env.description || 'No description'}
        </div>
      </summary>

      <div className="flex flex-col gap-4 border-t border-border px-5 py-5 md:pl-[52px]">
        {outsideTeams && (
          <p className="rounded-lg border border-warning/35 bg-warning/10 px-3 py-2 text-xs text-text-primary">
            Visible because you are an admin. You can inspect and hydrate it, but you can't create
            keys here — your teams aren't authorized.
          </p>
        )}
        {tone === 'danger' && (
          <p className="rounded-lg border border-destructive/35 bg-destructive/10 px-3 py-2 text-xs text-text-primary">
            Can't create keys yet — the AI gateway access group is not synced. Ask an admin.
          </p>
        )}
        {degraded && (
          <p className="rounded-lg border border-warning/35 bg-warning/10 px-3 py-2 text-xs text-text-primary">
            Keys and traffic work, but some content is not resolved yet ({degraded}).
          </p>
        )}

        <div className="grid gap-x-8 gap-y-4 md:grid-cols-2">
          {groups
            .filter((g) => g.names.length > 0)
            .map((g) => (
              <div key={g.label}>
                <h4 className="mb-2 font-mono text-[11px] font-semibold uppercase tracking-wider text-text-tertiary">
                  {g.label} <span className="text-text-secondary">{g.names.length}</span>
                </h4>
                <div className="flex flex-wrap gap-1.5">
                  {g.names.map((n) => (
                    <Tag key={n}>{n}</Tag>
                  ))}
                </div>
              </div>
            ))}
        </div>

        <CodeBlock inline code={`ach-cli env hydrate ${env.name} --dir ./${env.name} --target opencode`} />

        <div className="flex flex-wrap items-center gap-3">
          {!outsideTeams && isKeyable(env) && (
            <button
              type="button"
              onClick={() => openModal(env.name)}
              className="btn primary"
            >
              + Create key in this env
            </button>
          )}
          {teams.length > 0 && (
            <span className="ml-auto font-mono text-[11px] text-text-tertiary">
              teams: {teams.join(' · ')}
            </span>
          )}
        </div>
      </div>
    </details>
  );
}

export function Environments() {
  const isAdmin = useSessionStore((s) => s.me?.is_admin ?? false);
  const [showAll, setShowAll] = useState(false);
  const [search, setSearch] = useState('');
  const own = useEnvironments();
  // Only an admin can tick the box, so a non-admin never sends ?all=true.
  const all = useEnvironments({ all: true, enabled: isAdmin && showAll });

  const adminView = isAdmin && showAll;
  // The admin view needs BOTH lists: badging "Not in your teams" against an
  // own list that hasn't loaded yet would badge every row.
  const pending = adminView ? all.isPending || own.isPending : own.isPending;
  const envs = (adminView ? all.data : own.data) ?? [];
  const ownNames = new Set((own.data ?? []).map((e) => e.name));
  const outside = adminView ? envs.filter((e) => !ownNames.has(e.name)).length : 0;
  const visible = envs.filter((e) => matchesSearch(search, e.name, e.description, ...(e.authorizedTeams ?? [])));

  return (
    <div className="catalog-page flex flex-col gap-6">
      <CatalogHead title={PAGE_TITLE} description={PAGE_SUB} count={pending ? undefined : envs.length} />

      <div className="flex flex-col gap-3">
      <div className="toolbar">
        <TableSearch value={search} onChange={setSearch} placeholder="Search environments…" />
        {isAdmin && (
          <label className="toolbar-end inline-flex cursor-pointer items-center gap-2 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-text-secondary">
            <input
              type="checkbox"
              checked={showAll}
              onChange={(e) => setShowAll(e.target.checked)}
              className="size-3.5 accent-primary"
            />
            Show all environments{outside > 0 && ` · ${formatInt(outside)} outside`}
            <StatusPill tone="warn">admin</StatusPill>
          </label>
        )}
      </div>

      {pending ? (
        <div className="flex flex-col gap-3">
          <Skeleton variant="card" />
          <Skeleton variant="card" />
        </div>
      ) : envs.length === 0 ? (
        <div className="flex flex-col items-center gap-2 panel p-12 text-center">
          <div className="font-sans text-lg font-semibold text-text-primary">{EMPTY_HEADING}</div>
          <div className="max-w-md font-sans text-sm text-text-secondary">{EMPTY_BODY}</div>
        </div>
      ) : visible.length === 0 ? (
        <p className="text-sm text-text-secondary">No environments match.</p>
      ) : (
        <div className="flex flex-col gap-3">
          {visible.map((env) => (
            <EnvironmentCard
              key={env.name}
              env={env}
              outsideTeams={adminView && !ownNames.has(env.name)}
            />
          ))}
        </div>
      )}
      </div>
    </div>
  );
}
