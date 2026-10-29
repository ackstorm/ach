import { describe, it, expect } from 'vitest';
import { isRouterModel, isA2aModelRow, isAgentModelRow, isMcpModelRow, isMcpProtocolRow, mcpToolLabel } from './model-classify';

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

describe('isAgentModelRow', () => {
  it('detects operator-exposed agent models', () => {
    expect(isAgentModelRow('agent.finops-advisor')).toBe(true);
    expect(isAgentModelRow('a2a/finops-advisor')).toBe(true);
    expect(isAgentModelRow('acme.smart')).toBe(false);
    expect(isAgentModelRow(null)).toBe(false);
  });
});

describe('isMcpProtocolRow', () => {
  it('flags only the tool-less list_tools row', () => {
    expect(isMcpProtocolRow('MCP: list_tools')).toBe(true);
    expect(isMcpProtocolRow('MCP: mcp-drive/list_files')).toBe(false);
    expect(isMcpProtocolRow('list_tools')).toBe(false);
  });
});
