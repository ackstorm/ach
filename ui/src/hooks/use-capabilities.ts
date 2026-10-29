// use-capabilities.ts — TanStack Query hook for the personal capabilities
// endpoint (GET /platform/console/capabilities?scope=personal), backing the
// Models/MCP/A2A pages. Replaces the three independent alitellm-era fetches
// (use-models.ts/use-mcp.ts/use-a2a.ts, each its own endpoint) with ONE
// shared query: useCapabilities() owns the fetch; useModels/useMcp/useA2a
// each `select` their slice from the SAME cache entry (identical queryKey),
// so TanStack Query dedupes them into a single network request no matter how
// many of the three pages mount.
//
//   useCapabilities()  -> CapabilitiesResponse
//   useModels()        -> { models: ModelRow[], provisioning: boolean }
//   useMcp()           -> { servers: McpServerRow[], provisioning: boolean }
//   useA2a()           -> { agents: A2aAgentRow[], provisioning: boolean }
//
// Every selector also carries `provisioning` (handlers.go::personal): a
// deny-all shell-team sentinel alone — or an empty list, since LiteLLM
// filters the `no-default-models` sentinel out of the catalog — means the
// operator has not yet attached the caller's shell to an access group,
// reported as provisioning, never as "no models"/"not enabled".

import { useQuery } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';

import { getJson } from '@/lib/api';
import type {
  A2aAgentRow,
  CapabilitiesResponse,
  McpServerRow,
  ModelRow,
} from '@/lib/api-types';
import { useSessionStore } from '@/stores/session';

/** Query key for the personal capabilities fetch, scoped to the caller's identity (AC-04, mirrors use-keys.ts KEYS_QUERY_KEY). */
export function CAPABILITIES_QUERY_KEY(email: string | null | undefined) {
  return ['capabilities', 'personal', email ?? null] as const;
}

async function fetchCapabilities({
  signal,
}: {
  signal: AbortSignal;
}): Promise<CapabilitiesResponse> {
  const { status, data } = await getJson<CapabilitiesResponse>(
    '/platform/console/capabilities?scope=personal',
    { signal },
  );
  if (status === 200 && data) return data;
  throw new Error('capabilities-load-failed');
}

/**
 * GET /platform/console/capabilities?scope=personal. Throws on a non-200 /
 * malformed response so the query lands in `isError` (each consuming page
 * renders its error+retry branch); a 200 resolves to the CapabilitiesResponse
 * contract. `staleTime: 60_000` — the personal catalog changes only on an
 * Environment sync, so a minute-old read is still fresh.
 */
export function useCapabilities(): UseQueryResult<CapabilitiesResponse> {
  const email = useSessionStore((s) => s.me?.email);
  return useQuery({
    queryKey: CAPABILITIES_QUERY_KEY(email),
    queryFn: fetchCapabilities,
    staleTime: 60_000,
  });
}

export interface ModelsSelection {
  models: ModelRow[];
  provisioning: boolean;
}

/** The Models page's slice of the shared capabilities query. */
export function useModels(): UseQueryResult<ModelsSelection> {
  const email = useSessionStore((s) => s.me?.email);
  return useQuery({
    queryKey: CAPABILITIES_QUERY_KEY(email),
    queryFn: fetchCapabilities,
    staleTime: 60_000,
    select: (c) => ({ models: c.models, provisioning: c.provisioning }),
  });
}

export interface McpSelection {
  servers: McpServerRow[];
  provisioning: boolean;
}

/** The MCP page's slice of the shared capabilities query. */
export function useMcp(): UseQueryResult<McpSelection> {
  const email = useSessionStore((s) => s.me?.email);
  return useQuery({
    queryKey: CAPABILITIES_QUERY_KEY(email),
    queryFn: fetchCapabilities,
    staleTime: 60_000,
    select: (c) => ({ servers: c.mcp_servers, provisioning: c.provisioning }),
  });
}

export interface A2aSelection {
  agents: A2aAgentRow[];
  provisioning: boolean;
}

/** The A2A page's slice of the shared capabilities query. */
export function useA2a(): UseQueryResult<A2aSelection> {
  const email = useSessionStore((s) => s.me?.email);
  return useQuery({
    queryKey: CAPABILITIES_QUERY_KEY(email),
    queryFn: fetchCapabilities,
    staleTime: 60_000,
    select: (c) => ({ agents: c.a2a_agents, provisioning: c.provisioning }),
  });
}
