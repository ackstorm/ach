// use-gateway-latency.ts — GET /platform/console/latency for the console range:
// LiteLLM's own request log (the latest rows in the window, `sampled` when
// capped), so its figures describe recent calls, not the whole period.
import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { getJson } from '@/lib/api';
import type { GatewayLatency } from '@/lib/api-types';
import type { StatsRangeInput } from './use-stats';

export function useGatewayLatency(range: StatsRangeInput): UseQueryResult<GatewayLatency> {
  return useQuery({
    queryKey: ['console', 'latency', range.start, range.end],
    queryFn: async ({ signal }): Promise<GatewayLatency> => {
      const params = new URLSearchParams({ start_date: range.start, end_date: range.end });
      const { status, data } = await getJson<GatewayLatency>(`/platform/console/latency?${params.toString()}`, { signal });
      if (status === 200 && data) return data;
      throw new Error('latency-load-failed');
    },
  });
}
