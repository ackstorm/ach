// use-mcp.ts — TanStack Query hook for the configured MCP servers list.
//
// Mirrors use-models.ts: the queryFn threads `{ signal }` into the never-throw api
// wrapper and THROWS on a non-200 / malformed response so the query lands in
// `isError` (the MCP page renders its error+retry branch). A 200 resolves to the
// McpResponse contract — note `available: false` is a VALID 200 (the deployment's
// LiteLLM has no MCP gateway), surfaced by the page as a calm "not enabled" state,
// NOT an error.
//
//   useMcp()  GET /api/session/mcp -> McpResponse

import { useQuery } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';

import { getJson } from '@/lib/api';
import type { McpResponse } from '@/lib/api-types';

// `enabled` gates the fetch — same per-user/default-key rationale as useModels:
// the page passes `false` when the user has no default key so no request fires.
export function useMcp(enabled: boolean = true): UseQueryResult<McpResponse> {
  return useQuery({
    queryKey: ['session', 'mcp'],
    enabled,
    queryFn: async ({ signal }): Promise<McpResponse> => {
      const { status, data } = await getJson<McpResponse>('/api/session/mcp', {
        signal,
      });
      if (status === 200 && data) return data;
      throw new Error('mcp-load-failed');
    },
  });
}
