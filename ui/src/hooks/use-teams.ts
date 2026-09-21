// use-teams.ts — TanStack Query hook for the session teams list.
//
// Wraps the never-throw api wrapper (lib/api) so the team picker can degrade
// gracefully: a non-200 (or malformed) response resolves to [] rather than
// throwing, so create-key still works on the session's default team.

import { useQuery } from '@tanstack/react-query';

import { getJson } from '@/lib/api';
import type { Team, TeamsResponse } from '@/lib/api-types';

/** Query key for the session teams list. */
export const TEAMS_QUERY_KEY = ['session', 'teams'] as const;

/**
 * GET /api/session/teams -> Team[]. The team picker degrades gracefully: a
 * non-200 (or malformed) response resolves to [] (no teams shown) rather than
 * throwing, so create-key still works on the default team.
 */
export function useTeams() {
  return useQuery({
    queryKey: TEAMS_QUERY_KEY,
    queryFn: async (): Promise<Team[]> => {
      const { status, data } = await getJson<TeamsResponse>('/api/session/teams');
      if (status === 200 && data && Array.isArray(data.teams)) return data.teams;
      return [];
    },
  });
}
