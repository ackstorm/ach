// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// use-traces.ts — the caller's own traces, GET /platform/observability/traces[/{id}].
import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { getJson } from '@/lib/api';
import { useEvidenceWindow } from '@/hooks/use-evidence-reports';
import type { TraceDetailResponse, TracesResponse } from '@/lib/api-types';

export const TRACE_PAGE_SIZE = 10;

export type TraceStatusFilter = 'all' | 'error' | 'success' | 'unknown';

/** Server-side list order: a column the API sorts by (evidence.TraceSorts) and a direction. */
export type TraceSort = { key: 'started_at' | 'kind' | 'spans' | 'status' | 'request_id' | 'model' | 'duration' | 'tokens' | 'cost' | 'key'; dir: 'asc' | 'desc' };
export const DEFAULT_TRACE_SORT: TraceSort = { key: 'started_at', dir: 'desc' };

export function useTraces(cursor = 0, status: TraceStatusFilter = 'all', search = '', sort: TraceSort = DEFAULT_TRACE_SORT): UseQueryResult<TracesResponse> {
  const { range, tooLong } = useEvidenceWindow();
  return useQuery({
    queryKey: ['observability', 'traces', cursor, range.start, range.end, status, search, sort.key, sort.dir],
    enabled: !tooLong,
    queryFn: async ({ signal }): Promise<TracesResponse> => {
      const params = new URLSearchParams({ ...range, cursor: String(cursor), limit: String(TRACE_PAGE_SIZE) });
      if (status !== 'all') params.set('status', status);
      if (search) params.set('search', search);
      params.set('sort', sort.key);
      params.set('dir', sort.dir);
      const { status: responseStatus, data } = await getJson<TracesResponse>(`/platform/observability/traces?${params}`, { signal });
      if (responseStatus === 200 && data) return data;
      throw new Error('traces-load-failed');
    },
  });
}

export function useTrace(traceId: string | null, project: string | null = null): UseQueryResult<TraceDetailResponse> {
  const { range, tooLong } = useEvidenceWindow();
  return useQuery({
    queryKey: ['observability', 'trace', traceId, project, range.start, range.end],
    enabled: Boolean(traceId) && !tooLong,
    queryFn: async ({ signal }): Promise<TraceDetailResponse> => {
      const params = new URLSearchParams({ ...range, ...(project ? { project } : {}) });
      const { status, data } = await getJson<TraceDetailResponse>(`/platform/observability/traces/${encodeURIComponent(traceId!)}?${params}`, { signal });
      if (status === 200 && data) return data;
      throw new Error('trace-load-failed');
    },
  });
}
