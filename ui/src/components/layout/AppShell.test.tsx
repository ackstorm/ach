// AppShell.test.tsx — vitest suite for the authed shell's CHAT nav gating.
//
// useKeys is mocked so the shell renders deterministically without a real fetch.
// The CHAT nav item is set apart and gated on the user having a default key:
//   • a default exists -> an external link to chat.<domain>
//   • no default       -> a disabled, non-link span with a "need a default key" hint

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

// AppShell mounts CreateKeyModal (which calls useCreateKey), so the mock must
// export it too — a benign stub, since the modal is idle/closed in these tests.
vi.mock('@/hooks/use-keys', () => ({
  useKeys: vi.fn(),
  useCreateKey: vi.fn(() => ({
    mutate: vi.fn(),
    mutateAsync: vi.fn(),
    isPending: false,
    reset: vi.fn(),
  })),
  KEYS_QUERY_KEY: ['session', 'keys'],
}));

// CreateKeyModal also calls useTeams (a useQuery) — stub it to [] so the shell
// renders without a QueryClientProvider; the picker is hidden either way.
vi.mock('@/hooks/use-teams', () => ({
  useTeams: vi.fn(() => ({ data: [] })),
}));

import { useKeys } from '@/hooks/use-keys';
import type { AppConfig, KeyRow, SessionMe } from '@/lib/api-types';
import { AppShell } from './AppShell';

const useKeysMock = vi.mocked(useKeys);

const ME: SessionMe = {
  email: 'alice@example.com',
  name: 'Alice Example',
  team_id: 'team-platform',
  endpoint: 'https://api.acme.ai',
  limits: null,
  spend: { current: 0, source: 'user' },
};

const CONFIG = { links: {} } as unknown as AppConfig;

function setKeys(rows: KeyRow[] | undefined): void {
  useKeysMock.mockReturnValue({ data: rows } as unknown as UseQueryResult<KeyRow[]>);
}

function makeRow(overrides: Partial<KeyRow> = {}): KeyRow {
  return {
    id: 'key-1',
    key_alias: 'k',
    spend: 0,
    budget: null,
    tpm_limit: null,
    rpm_limit: null,
    models: null,
    team_id: null,
    created_at: null,
    expires: null,
    last_used: null,
    is_default: false,
    ...overrides,
  };
}

function renderShell(): void {
  render(
    <MemoryRouter>
      <AppShell me={ME} config={CONFIG} />
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

beforeEach(() => {
  setKeys([]);
});

describe('AppShell — CHAT nav gating', () => {
  it('with a default key, CHAT is an external link to chat.<domain>', () => {
    setKeys([makeRow({ id: 'key-1', is_default: true })]);
    renderShell();
    const link = screen.getByRole('link', { name: 'Chat' });
    expect(link).toHaveAttribute('href', 'https://chat.acme.ai');
    expect(link).toHaveAttribute('target', '_blank');
  });

  it('without a default key, CHAT is disabled (no link) with a hint', () => {
    setKeys([makeRow({ id: 'key-1', is_default: false })]);
    renderShell();
    expect(screen.queryByRole('link', { name: 'Chat' })).not.toBeInTheDocument();
    const disabled = screen.getByText('Chat');
    expect(disabled).toHaveAttribute('aria-disabled', 'true');
    expect(disabled).not.toHaveAttribute('href');
    expect(disabled.getAttribute('title') ?? '').toMatch(/default key/i);
  });

  it('treats an undefined keys list as no default', () => {
    setKeys(undefined);
    renderShell();
    expect(screen.queryByRole('link', { name: 'Chat' })).not.toBeInTheDocument();
    expect(screen.getByText('Chat')).toHaveAttribute('aria-disabled', 'true');
  });
});

describe('AppShell — Models/MCPs nav gating', () => {
  it('with a default key, Models + MCPs are nav links', () => {
    setKeys([makeRow({ id: 'key-1', is_default: true })]);
    renderShell();
    expect(screen.getByRole('link', { name: 'Models' })).toHaveAttribute('href', '/models');
    expect(screen.getByRole('link', { name: 'MCPs' })).toHaveAttribute('href', '/mcp');
  });

  it('without a default key, Models + MCPs are disabled (no link) with a hint', () => {
    setKeys([makeRow({ id: 'key-1', is_default: false })]);
    renderShell();
    expect(screen.queryByRole('link', { name: 'Models' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'MCPs' })).not.toBeInTheDocument();
    for (const label of ['Models', 'MCPs']) {
      const el = screen.getByText(label);
      expect(el).toHaveAttribute('aria-disabled', 'true');
      expect(el).not.toHaveAttribute('href');
      expect(el.getAttribute('title') ?? '').toMatch(/default key/i);
    }
  });

  it('KEYS / STATS / HOW-TO stay links regardless of default key', () => {
    setKeys([]);
    renderShell();
    expect(screen.getByRole('link', { name: 'Keys' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Stats' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'How-to' })).toBeInTheDocument();
  });
});

// The header dropdowns are pure navigation and MUST be non-modal: Radix's
// default modal mode mounts react-remove-scroll, which scroll-locks <body>
// (data-scroll-locked + overflow:hidden + pointer-events:none) while a menu is
// open. On a narrow viewport that mutation reflowed the page content into a
// collapsed, one-word-per-line column (the "menu open does something strange to
// the content" bug). `modal={false}` removes the body mutation; these tests
// fail if a regression re-enables modal on either menu.
describe('AppShell — header menus do not scroll-lock the page', () => {
  afterEach(() => {
    // react-remove-scroll mutates the shared document.body; make sure a leaked
    // attribute from one test cannot mask a regression in the next.
    document.body.removeAttribute('data-scroll-locked');
  });

  it('opening the user menu does not lock <body>', async () => {
    setKeys([]);
    renderShell();
    // Radix opens on Enter (see App.test.tsx). Awaiting the item also lets the
    // scroll-lock effect run — so a false pass (lock applied late) can't slip by.
    fireEvent.keyDown(screen.getByRole('button', { name: 'User menu' }), { key: 'Enter' });
    expect(await screen.findByRole('menuitem', { name: 'Log out' })).toBeInTheDocument();
    expect(document.body.hasAttribute('data-scroll-locked')).toBe(false);
  });

  it('opening the mobile hamburger menu does not lock <body>', async () => {
    setKeys([]);
    renderShell();
    fireEvent.keyDown(screen.getByRole('button', { name: 'Open menu' }), { key: 'Enter' });
    // How-to is an always-ungated item in the mobile nav menu.
    expect(await screen.findByRole('menuitem', { name: 'How-to' })).toBeInTheDocument();
    expect(document.body.hasAttribute('data-scroll-locked')).toBe(false);
  });
});
