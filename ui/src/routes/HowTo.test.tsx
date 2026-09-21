// HowTo.test.tsx — jsdom suite for the onboarding guide.
//
// The page is static except for the live gateway base (from the session store)
// and the model picker (from useModels). Every external dependency is mocked so
// no fetch/router happens; tests assert which content BRANCH renders.

import { afterEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import type { ModelsResponse, ModelRow } from '@/lib/api-types';

vi.mock('react-router', () => ({ useNavigate: () => () => {} }));
vi.mock('@/stores/session', () => ({
  useSessionStore: (sel: (s: unknown) => unknown) =>
    sel({ me: { endpoint: 'https://api.ackstorm.ai' } }),
}));
vi.mock('@/stores/config', () => ({
  useConfigStore: (sel: (s: unknown) => unknown) => sel({ config: {} }),
}));
vi.mock('@/hooks/use-keys', () => ({ useHasDefaultKey: vi.fn(() => true) }));
vi.mock('@/hooks/use-models', () => ({ useModels: vi.fn() }));

import { useModels } from '@/hooks/use-models';
import { HowTo } from './HowTo';

const useModelsMock = vi.mocked(useModels);

function model(name: string): ModelRow {
  return {
    name,
    providers: ['openai'],
    mode: 'chat',
    max_input_tokens: null,
    max_output_tokens: null,
    input_cost_per_token: null,
    output_cost_per_token: null,
    supports_vision: false,
    supports_function_calling: false,
    supports_reasoning: false,
    supports_web_search: false,
  };
}

function setModels(names: string[]): void {
  useModelsMock.mockReturnValue({
    data: { models: names.map(model) } as ModelsResponse,
    isPending: false,
    isError: false,
  } as unknown as UseQueryResult<ModelsResponse>);
}

afterEach(() => {
  vi.clearAllMocks();
  cleanup();
});

describe('HowTo — quickstart SDK tabs', () => {
  it('offers Python and TypeScript quickstart tabs', () => {
    setModels([]);
    render(<HowTo />);
    expect(screen.getByRole('tab', { name: /python/i })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: /typescript/i })).toBeInTheDocument();
  });
});

describe('HowTo — troubleshooting', () => {
  it('has a troubleshooting section with common errors', () => {
    setModels([]);
    render(<HowTo />);
    expect(screen.getByText(/401 Unauthorized/)).toBeInTheDocument();
    expect(screen.getByText(/429/)).toBeInTheDocument();
  });
});

describe('HowTo — model picker', () => {
  it('lets the user pick a model that flows into the snippets', () => {
    setModels(['zeta.model', 'omega.model']);
    render(<HowTo />);
    const select = screen.getByRole('combobox', { name: 'Model' });
    // The default curl tab shows the standard alias before any pick.
    expect(screen.getByText(/"model": "ackstorm.fast"/)).toBeInTheDocument();
    fireEvent.change(select, { target: { value: 'zeta.model' } });
    // The visible curl snippet now carries the picked alias.
    expect(screen.getByText(/"model": "zeta.model"/)).toBeInTheDocument();
  });
});
