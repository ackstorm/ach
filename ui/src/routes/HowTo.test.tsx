// HowTo.test.tsx — jsdom suite for the onboarding guide.
//
// The page is static except for the live gateway base (from the session store)
// and the model picker (from useModels). Every external dependency is mocked so
// no fetch/router happens; tests assert which content BRANCH renders.

import { afterEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

import type { ModelRow } from '@/lib/api-types';
import type { ModelsSelection } from '@/hooks/use-capabilities';

vi.mock('react-router', () => ({ useNavigate: () => () => {} }));
vi.mock('@/stores/session', () => ({
  useSessionStore: (sel: (s: unknown) => unknown) =>
    sel({ me: { endpoint: 'https://api.ackstorm.ai', provider_name: 'acme' } }),
}));
vi.mock('@/stores/config', () => ({
  useConfigStore: (sel: (s: unknown) => unknown) => sel({ config: {} }),
}));
vi.mock('@/hooks/use-capabilities', () => ({ useModels: vi.fn() }));

import { useModels } from '@/hooks/use-capabilities';
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
    data: { models: names.map(model), provisioning: false },
    isPending: false,
    isError: false,
  } as unknown as UseQueryResult<ModelsSelection>);
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

// ACH ground truth (do not regress): keys are ek_/pk_, the credential header is
// x-ach-key taken verbatim (no Bearer prefix), and x-litellm-api-key is a
// passthrough slot that must never appear in a recommended snippet.
describe('HowTo — ACH auth ground truth', () => {
  it('renders x-ach-key and an ek_ placeholder, and never sk- or x-litellm-api-key', () => {
    setModels([]);
    const { container } = render(<HowTo />);
    const text = container.textContent ?? '';
    expect(text).toContain('x-ach-key');
    expect(text).toContain('ek_');
    expect(text).not.toContain('sk-');
    expect(text).not.toContain('x-litellm-api-key');
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

describe('HowTo — OpenCode install', () => {
  it('installs via the well-known URL and signs in with provider_name', () => {
    setModels([]);
    const { container } = render(<HowTo />);
    const text = container.textContent ?? '';
    expect(text).toContain('opencode auth login https://api.ackstorm.ai');
    expect(text).toContain('opencode auth login acme');
    expect(text).toContain('opencode service restart');
    expect(text).toContain(`export OPENCODE_CONFIG_CONTENT='{"websearch":{"provider":"exa"}}'`);
    expect(text).not.toContain('/clients/opencode/plugin');
  });
});

describe('HowTo — OpenCode only', () => {
  it('shows only OpenCode tools, its MCP commands and OpenCode Desktop', () => {
    setModels([]);
    const { container } = render(<HowTo />);
    for (const hidden of ['Claude Code', 'Codex', 'Pi agent', 'Qwen Code', 'Cursor', 'Claude Desktop']) {
      expect(screen.queryByRole('tab', { name: hidden })).toBeNull();
    }
    const text = container.textContent ?? '';
    expect(text).toContain('opencode mcp add google-drive --url https://api.ackstorm.ai/mcp/mcp-google-drive --global');
    expect(text).toContain('opencode mcp auth google-drive');
    expect(text).toContain('OpenCode Desktop');
    expect(text).not.toContain('opencode.json');
  });
});

// Login is the default path: editors + MCP lead, the key-based API section
// comes after them and starts collapsed (not every account may mint keys).
describe('HowTo — section order', () => {
  it('leads with editors and MCP, with API keys collapsed after them', () => {
    setModels([]);
    const { container } = render(<HowTo />);
    const ids = Array.from(container.querySelectorAll('section[id], details[id]')).map((el) => el.id);
    expect(ids).toEqual(['tools', 'mcp', 'api', 'chat', 'troubleshooting']);
    const api = container.querySelector('details#api') as HTMLDetailsElement;
    expect(api.open).toBe(false);
  });
});

describe('HowTo — header endpoint, no third-party docs', () => {
  it('shows the gateway endpoint in the header and copies exactly it', async () => {
    setModels([]);
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    render(<HowTo />);
    expect(screen.getByText('https://api.ackstorm.ai')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('https://api.ackstorm.ai'));
  });

  it('renders every snippet in the shared dark code block', () => {
    setModels([]);
    const { container } = render(<HowTo />);
    const blocks = container.querySelectorAll('.codeblock');
    expect(blocks.length).toBeGreaterThan(0);
    expect(container.querySelectorAll('pre').length).toBe(container.querySelectorAll('.codeblock pre').length);
  });

  it('says nothing about LiteLLM and links nowhere on its docs', () => {
    setModels([]);
    const { container } = render(<HowTo />);
    expect(container.textContent).not.toMatch(/LiteLLM/);
    expect(container.querySelector('a[href*="litellm"]')).toBeNull();
  });
});
