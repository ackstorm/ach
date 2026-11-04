// model-classify.ts — pure row classification for the models catalog + the
// stats "Usage Breakdown" table (LiteLLM lumps MCP tool spend into the model
// breakdown under an "MCP: " prefix).
import type { ModelRow } from '@/lib/api-types';

export function isRouterModel(row: Pick<ModelRow, 'providers'>): boolean {
  return (row.providers ?? []).some((p) => String(p).toLowerCase() === 'auto_router');
}

const MCP_PREFIX = 'MCP:';

export function isMcpModelRow(name: string | null | undefined): boolean {
  return typeof name === 'string' && name.trim().startsWith(MCP_PREFIX);
}

// Strip the "MCP: " prefix for display; returns the input unchanged if absent.
export function mcpToolLabel(name: string | null | undefined): string {
  if (!isMcpModelRow(name)) return name ?? '';
  return (name as string).slice((name as string).indexOf(MCP_PREFIX) + MCP_PREFIX.length).trim();
}

// Agents shown as model rows in usage accounting: the operator's `agent.<name>`
// and LiteLLM's own `a2a/<name>`. The Runtime report counts them as agents.
export function isAgentModelRow(name: string | null | undefined): boolean {
  return typeof name === 'string' && (name.startsWith('agent.') || name.startsWith('a2a/'));
}

// Tool-less MCP protocol traffic (every tools/list is logged as "MCP: list_tools").
export function isMcpProtocolRow(name: string | null | undefined): boolean {
  return isMcpModelRow(name) && mcpToolLabel(name) === 'list_tools';
}
