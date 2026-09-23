// model-classify.ts — pure row classification for the models catalog + the
// stats "Usage Breakdown" table (LiteLLM lumps MCP tool spend into the model
// breakdown under an "MCP: " prefix).
import type { ModelRow } from '@/lib/api-types';

export function isRouterModel(row: Pick<ModelRow, 'providers'>): boolean {
  return (row.providers ?? []).some((p) => String(p).toLowerCase() === 'auto_router');
}

// A2A agents ride the same /v2/model/info catalog as ordinary models, e.g.
// model_group "agent.finops-advisor" with providers: ["a2a1"] (the trailing
// digit is a protocol version, not part of the prefix) vs. an ordinary
// model's ["gemini"]/["openrouter"]/etc. They aren't models — filter them out
// of the models catalog + quickstart picker.
export function isA2aModelRow(row: Pick<ModelRow, 'providers'>): boolean {
  return (row.providers ?? []).some((p) => String(p).toLowerCase().startsWith('a2a'));
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
