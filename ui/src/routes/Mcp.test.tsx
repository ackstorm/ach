// Mcp.test.tsx — vitest for the #/mcp page (jsdom).
//
// useMcp is fully mocked so NO real fetch happens; each test programs its return
// to drive the loading / error / unavailable / empty / populated branches.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import type { McpServerRow } from '@/lib/api-types';
import type { McpSelection } from '@/hooks/use-capabilities';

vi.mock('@/hooks/use-capabilities', () => ({ useMcp: vi.fn() }));

import { useMcp } from '@/hooks/use-capabilities';
import { Mcp } from './Mcp';

const useMcpMock = vi.mocked(useMcp);

function makeServer(over: Partial<McpServerRow> = {}): McpServerRow {
  return {
    id: 'github-mcp',
    name: 'GitHub',
    description: 'Repos and issues.',
    url: 'https://mcp.internal/github',
    transport: 'http',
    auth_type: 'oauth2',
    status: 'healthy',
    tools: ['list_repos', 'create_issue'],
    tool_count: 2,
    access_groups: ['platform'],
    ...over,
  };
}

function setPending(): void {
  useMcpMock.mockReturnValue({
    data: undefined,
    isPending: true,
    isError: false,
    refetch: vi.fn(),
  } as unknown as UseQueryResult<McpSelection>);
}

function setError(): ReturnType<typeof vi.fn> {
  const refetch = vi.fn();
  useMcpMock.mockReturnValue({
    data: undefined,
    isPending: false,
    isError: true,
    refetch,
  } as unknown as UseQueryResult<McpSelection>);
  return refetch;
}

function setSuccess(data: McpSelection): void {
  useMcpMock.mockReturnValue({
    data,
    isPending: false,
    isError: false,
    refetch: vi.fn(),
  } as unknown as UseQueryResult<McpSelection>);
}

afterEach(() => {
  vi.clearAllMocks();
  cleanup();
});

describe('Mcp — loading', () => {
  beforeEach(() => setPending());
  it('renders skeletons and keeps the title', () => {
    const { container } = render(<Mcp />);
    expect(
      container.querySelectorAll('[data-slot="skeleton"]').length,
    ).toBeGreaterThan(0);
    expect(screen.getByText('MCP')).toBeInTheDocument();
  });
});

describe('Mcp — error', () => {
  it('shows the locked error heading and retry re-fires the query', () => {
    const refetch = setError();
    render(<Mcp />);
    expect(screen.getByText("Couldn't load MCP servers")).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'retry' }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });
});

describe('Mcp — unavailable (no gateway, not provisioning)', () => {
  it('shows the calm "not enabled" state when the list is empty and not provisioning', () => {
    setSuccess({ servers: [], provisioning: false });
    render(<Mcp />);
    expect(screen.getByText('MCP gateway not enabled')).toBeInTheDocument();
  });
});

describe('Mcp — provisioning', () => {
  it('shows the provisioning card, not the "not enabled" copy, when provisioning is true', () => {
    setSuccess({ servers: [], provisioning: true });
    render(<Mcp />);
    expect(screen.getByText('Access is being provisioned')).toBeInTheDocument();
    expect(screen.queryByText('MCP gateway not enabled')).not.toBeInTheDocument();
  });
});

describe('Mcp — populated', () => {
  it('renders the server name, status, tools, and access group', () => {
    setSuccess({ servers: [makeServer()], provisioning: false });
    render(<Mcp />);
    expect(screen.getByText('GitHub')).toBeInTheDocument();
    expect(screen.getByText('healthy')).toBeInTheDocument();
    expect(screen.getByText('list_repos')).toBeInTheDocument();
    expect(screen.getByText('create_issue')).toBeInTheDocument();
    expect(screen.getByText('platform')).toBeInTheDocument();
  });

  it('caps the visible tool chips at six with a "+N more" marker', () => {
    const tools = Array.from({ length: 9 }, (_, i) => `tool_${i}`);
    setSuccess({
      servers: [makeServer({ tools, tool_count: 9 })],
      provisioning: false,
    });
    render(<Mcp />);
    expect(screen.getByText('tool_5')).toBeInTheDocument(); // 6th chip (index 5)
    expect(screen.queryByText('tool_6')).not.toBeInTheDocument(); // hidden
    expect(screen.getByText('+3 more')).toBeInTheDocument();
  });
});
