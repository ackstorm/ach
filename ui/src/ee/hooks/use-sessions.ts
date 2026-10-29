// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// use-sessions.ts — the caller's own sessions, GET /platform/observability/sessions[/{id}].
import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { getJson } from '@/lib/api';
import { useEvidenceWindow } from '@/hooks/use-evidence-reports';
import { TRACE_PAGE_SIZE } from './use-traces';
import type { SessionDetail, SessionPage } from '@/lib/api-types';

/** Server-side list order: a column the API sorts sessions by and a direction. */
export type SessionSort = { key: 'started_at' | 'last_activity_at' | 'duration' | 'turns' | 'tokens' | 'cost'; dir: 'asc' | 'desc' };
export const DEFAULT_SESSION_SORT: SessionSort = { key: 'started_at', dir: 'desc' };

export function useSessions(cursor = 0, sort: SessionSort = DEFAULT_SESSION_SORT): UseQueryResult<SessionPage> {
  const { range, tooLong } = useEvidenceWindow();
  return useQuery({
    queryKey: ['observability', 'sessions', cursor, range.start, range.end, sort.key, sort.dir],
    enabled: !tooLong,
    queryFn: async ({ signal }): Promise<SessionPage> => {
      const params = new URLSearchParams({ ...range, cursor: String(cursor), limit: String(TRACE_PAGE_SIZE), sort: sort.key, dir: sort.dir });
      const { status, data } = await getJson<SessionPage>(`/platform/observability/sessions?${params}`, { signal });
      if (status === 200 && data) return data;
      throw new Error('sessions-load-failed');
    },
  });
}

export function useSession(sessionId: string | null, project: string | null = null): UseQueryResult<SessionDetail> {
  const { range, tooLong } = useEvidenceWindow();
  return useQuery({
    queryKey: ['observability', 'session', sessionId, project, range.start, range.end],
    enabled: Boolean(sessionId) && !tooLong,
    queryFn: async ({ signal }): Promise<SessionDetail> => {
      const params = new URLSearchParams({ ...range, ...(project ? { project } : {}) });
      const { status, data } = await getJson<SessionDetail>(`/platform/observability/sessions/${encodeURIComponent(sessionId!)}?${params}`, { signal });
      if (status === 200 && data) return data;
      throw new Error('session-load-failed');
    },
  });
}
