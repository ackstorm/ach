import { describe, it, expect } from 'vitest';
import { isRouterModel, isA2aModelRow, isMcpModelRow, mcpToolLabel } from './model-classify';

describe('isRouterModel', () => {
  it('detects the auto-router provider', () => {
    expect(isRouterModel({ providers: ['auto_router'] } as never)).toBe(true);
    expect(isRouterModel({ providers: ['AUTO_ROUTER'] } as never)).toBe(true);
    expect(isRouterModel({ providers: ['openai'] } as never)).toBe(false);
  });
});

describe('isA2aModelRow', () => {
  it('detects an a2a provider by prefix, including a trailing protocol-version digit', () => {
    expect(isA2aModelRow({ providers: ['a2a1'] } as never)).toBe(true);
    expect(isA2aModelRow({ providers: ['a2a'] } as never)).toBe(true);
    expect(isA2aModelRow({ providers: ['A2A1'] } as never)).toBe(true);
  });

  it('does not match an ordinary provider', () => {
    expect(isA2aModelRow({ providers: ['gemini'] } as never)).toBe(false);
    expect(isA2aModelRow({ providers: ['openrouter'] } as never)).toBe(false);
  });
});

describe('mcp rows', () => {
  it('classifies and labels MCP breakdown rows', () => {
    expect(isMcpModelRow('MCP: mcp-gitlab.gitlab_api')).toBe(true);
    expect(isMcpModelRow('gemini/flash')).toBe(false);
    expect(mcpToolLabel('MCP: mcp-gitlab.gitlab_api')).toBe('mcp-gitlab.gitlab_api');
  });
});
