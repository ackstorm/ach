import { describe, it, expect } from 'vitest';
import { isRouterModel, isMcpModelRow, mcpToolLabel } from './model-classify';

describe('isRouterModel', () => {
  it('detects the auto-router provider', () => {
    expect(isRouterModel({ providers: ['auto_router'] } as never)).toBe(true);
    expect(isRouterModel({ providers: ['AUTO_ROUTER'] } as never)).toBe(true);
    expect(isRouterModel({ providers: ['openai'] } as never)).toBe(false);
  });
});

describe('mcp rows', () => {
  it('classifies and labels MCP breakdown rows', () => {
    expect(isMcpModelRow('MCP: mcp-gitlab.gitlab_api')).toBe(true);
    expect(isMcpModelRow('gemini/flash')).toBe(false);
    expect(mcpToolLabel('MCP: mcp-gitlab.gitlab_api')).toBe('mcp-gitlab.gitlab_api');
  });
});
