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
 * GET /platform/environments?limit=500 -> EnvironmentRow[]. Degrades
 * gracefully: a non-200 (or malformed) response resolves to [] (the picker
 * shows no options) rather than throwing.
 */
export function useEnvironments() {
  return useQuery({
    queryKey: ENVIRONMENTS_QUERY_KEY,
    queryFn: async (): Promise<EnvironmentRow[]> => {
      const { status, data } = await getJson<EnvironmentsResponse>(
        '/platform/environments?limit=500',
      );
      if (status === 200 && data && Array.isArray(data.items)) return data.items;
      return [];
    },
  });
}
