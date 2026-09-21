// A2a.test.tsx — vitest for the #/a2a page (jsdom). Mirrors Mcp.test.tsx.
//
// useA2a is fully mocked so NO real fetch happens; each test programs its return
// to drive the loading / error / unavailable / empty / populated branches.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import type { A2aResponse, A2aAgentRow } from '@/lib/api-types';

vi.mock('@/hooks/use-a2a', () => ({ useA2a: vi.fn() }));
vi.mock('@/hooks/use-keys', () => ({ useHasDefaultKey: vi.fn() }));

import { useA2a } from '@/hooks/use-a2a';
import { useHasDefaultKey } from '@/hooks/use-keys';
import { A2a } from './A2a';

const useA2aMock = vi.mocked(useA2a);
const useHasDefaultKeyMock = vi.mocked(useHasDefaultKey);

// Default: the user HAS a default key (the branches under test need it). The
// no-default gate has its own suite that flips this to false.
beforeEach(() => useHasDefaultKeyMock.mockReturnValue(true));

function makeAgent(over: Partial<A2aAgentRow> = {}): A2aAgentRow {
  return {
    id: 'research-agent',
    name: 'Research Agent',
    description: 'Web research with citations.',
    url: 'https://a2a.internal/research',
    transport: 'JSONRPC',
    version: '1.2.0',
    skills: ['deep_research', 'summarize'],
    skill_count: 2,
    streaming: true,
    ...over,
  };
}

function setPending(): void {
  useA2aMock.mockReturnValue({
    data: undefined,
    isPending: true,
    isError: false,
    refetch: vi.fn(),
  } as unknown as UseQueryResult<A2aResponse>);
}

function setError(): ReturnType<typeof vi.fn> {
  const refetch = vi.fn();
  useA2aMock.mockReturnValue({
    data: undefined,
    isPending: false,
    isError: true,
    refetch,
  } as unknown as UseQueryResult<A2aResponse>);
  return refetch;
}

function setSuccess(data: A2aResponse): void {
  useA2aMock.mockReturnValue({
    data,
    isPending: false,
    isError: false,
    refetch: vi.fn(),
  } as unknown as UseQueryResult<A2aResponse>);
}

afterEach(() => {
  vi.clearAllMocks();
  cleanup();
});

describe('A2a — no default key (gate)', () => {
  it('renders the default-key prompt and does NOT call useA2a for data', () => {
    useHasDefaultKeyMock.mockReturnValue(false);
    setSuccess({ agents: [makeAgent()], available: true });
    render(<A2a />);
    expect(screen.getByText('A default key is required')).toBeInTheDocument();
    expect(screen.getByText('A2A')).toBeInTheDocument(); // header still shows
    expect(screen.queryByText('Research Agent')).not.toBeInTheDocument();
    expect(useA2aMock).toHaveBeenCalledWith(false); // fetch disabled
  });
});

describe('A2a — loading', () => {
  beforeEach(() => setPending());
  it('renders skeletons and keeps the title', () => {
    const { container } = render(<A2a />);
    expect(
      container.querySelectorAll('[data-slot="skeleton"]').length,
    ).toBeGreaterThan(0);
    expect(screen.getByText('A2A')).toBeInTheDocument();
  });
});

describe('A2a — error', () => {
  it('shows the locked error heading and retry re-fires the query', () => {
    const refetch = setError();
    render(<A2a />);
    expect(screen.getByText("Couldn't load A2A agents")).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'retry' }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });
});

describe('A2a — unavailable (no gateway)', () => {
  it('shows the calm "not enabled" state when available is false', () => {
    setSuccess({ agents: [], available: false });
    render(<A2a />);
    expect(screen.getByText('A2A gateway not enabled')).toBeInTheDocument();
  });
});

describe('A2a — empty (gateway on, no agents)', () => {
  it('shows the empty state', () => {
    setSuccess({ agents: [], available: true });
    render(<A2a />);
    expect(screen.getByText('No A2A agents yet')).toBeInTheDocument();
  });
});

describe('A2a — populated', () => {
  it('renders the agent name, version, and skills', () => {
    setSuccess({ agents: [makeAgent()], available: true });
    render(<A2a />);
    expect(screen.getByText('Research Agent')).toBeInTheDocument();
    expect(screen.getByText('v1.2.0')).toBeInTheDocument();
    expect(screen.getByText('deep_research')).toBeInTheDocument();
    expect(screen.getByText('summarize')).toBeInTheDocument();
  });
});
