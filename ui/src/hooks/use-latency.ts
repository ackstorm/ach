// use-latency.ts — TanStack Query hook for the console latency contract.
//
//   useLatency(range)  GET /platform/console/latency?start_date=&end_date= -> LatencyResponse
//
// Mirrors use-stats.ts (same date window as the rest of the page), with one
// difference: the latency panel is SUPPLEMENTARY, and a degraded backend answers a
// valid 200 with { available: false } (spend logs unreadable for this user). That is
// NOT an error — the hook resolves it so the panel renders its calm "not available"
// state. Only a non-200 / missing body throws into isError. Keyed by the range so a
// range change refetches and each range caches separately.

import { useQuery } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';

import { getJson } from '@/lib/api';
import type { LatencyResponse } from '@/lib/api-types';
import type { StatsRangeInput } from '@/hooks/use-stats';

export function useLatency(
  range: StatsRangeInput,
): UseQueryResult<LatencyResponse> {
  return useQuery({
    queryKey: ['console', 'latency', range.start, range.end],
    queryFn: async ({ signal }): Promise<LatencyResponse> => {
      const params = new URLSearchParams({
        start_date: range.start,
        end_date: range.end,
      });
      const { status, data } = await getJson<LatencyResponse>(
        `/platform/console/latency?${params.toString()}`,
        { signal },
      );
      if (status === 200 && data) return data;
      throw new Error('latency-load-failed');
    },
  });
}
