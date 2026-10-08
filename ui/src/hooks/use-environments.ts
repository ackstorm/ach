// use-environments.ts — TanStack Query hook for the Environments list.
// Mirrors use-teams.ts: the create-key modal's Environment picker degrades
// gracefully — a non-200 (or malformed) response resolves to [] rather than
// throwing.

import { useQuery } from '@tanstack/react-query';

import { getJson } from '@/lib/api';
import type { EnvironmentRow, EnvironmentsResponse } from '@/lib/api-types';

/** Query key for the environments list. */
export const ENVIRONMENTS_QUERY_KEY = ['environments'] as const;

/**
 * GET /platform/environments?limit=500 -> EnvironmentRow[] — the caller's
 * team-scoped list (admins included). `{ all: true }` adds `&all=true`, the
 * admin-only full inventory, under its own cache key; pass `enabled: false`
 * until the caller is known to be an admin. Degrades gracefully: a non-200
 * (or malformed) response resolves to [] (the picker shows no options) rather
 * than throwing.
 */
export function useEnvironments({
  all = false,
  enabled = true,
}: { all?: boolean; enabled?: boolean } = {}) {
  return useQuery({
    enabled,
    queryKey: all ? [...ENVIRONMENTS_QUERY_KEY, 'all'] : ENVIRONMENTS_QUERY_KEY,
    queryFn: async (): Promise<EnvironmentRow[]> => {
      const { status, data } = await getJson<EnvironmentsResponse>(
        `/platform/environments?limit=500${all ? '&all=true' : ''}`,
      );
      if (status === 200 && data && Array.isArray(data.items)) return data.items;
      return [];
    },
  });
}
