// use-stats.ts — TanStack Query hook for the console usage stats contract.
//
// Mirrors use-keys.ts: the queryFn threads TanStack Query's `{ signal }` into the
// never-throw api wrapper (lib/api::getJson) and THROWS on a non-200 / malformed
// response so the query lands in `isError` (the dashboard renders its error
// branch), while a 200 resolves to the StatsResponse contract.
//
//   useStats(range)  GET /platform/console/stats?start_date=&end_date= -> StatsResponse
//
// Keyed by the range, so changing the range refetches and each range caches
// separately. The `{ signal }` makes cancellation work end-to-end with apiFetch's
// AbortError re-throw (unmount / superseding refetch aborts the in-flight fetch).

import { useQuery } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';

import { getJson } from '@/lib/api';
import type { StatsResponse } from '@/lib/api-types';

/**
 * The stats range, in YYYY-MM-DD. `start`/`end` map to the backend query params
 * `start_date`/`end_date`. Matches presetToRange() output.
 */
export interface StatsRangeInput {
  start: string; // YYYY-MM-DD (maps to start_date)
  end: string; // YYYY-MM-DD (maps to end_date)
}

/**
 * GET /platform/console/stats?start_date=&end_date=. Throws on a non-200 / malformed
 * response so the query lands in `isError`; a 200 with a body resolves to the
 * StatsResponse contract. The query key carries the range so each range caches
 * separately and a range change triggers a refetch.
 */
export function useStats(range: StatsRangeInput): UseQueryResult<StatsResponse> {
  return useQuery({
    queryKey: ['console', 'stats', range.start, range.end],
    queryFn: async ({ signal }): Promise<StatsResponse> => {
      const params = new URLSearchParams({
        start_date: range.start,
        end_date: range.end,
      });
      const { status, data } = await getJson<StatsResponse>(
        `/platform/console/stats?${params.toString()}`,
        { signal },
      );
      if (status === 200 && data) return data;
      throw new Error('stats-load-failed');
    },
  });
}
