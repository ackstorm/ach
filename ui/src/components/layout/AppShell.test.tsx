// AppShell.test.tsx — vitest suite for the authed shell's nav + CHAT link.
//
// ACH has no default-key gating concept (env keys are the scoping unit, not a
// "default" key) — CHAT and every nav item always render as a live link.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

// AppShell mounts CreateKeyModal (which calls useCreateKey), so the mock must
// export it too — a benign stub, since the modal is idle/closed in these tests.
vi.mock('@/hooks/use-keys', () => ({
  useCreateKey: vi.fn(() => ({
    mutate: vi.fn(),
    mutateAsync: vi.fn(),
    isPending: false,
    reset: vi.fn(),
  })),
  KEYS_QUERY_KEY: vi.fn(() => ['keys', null]),
}));

// CreateKeyModal also calls useEnvironments (a useQuery) — stub it to [] so the
// shell renders without a QueryClientProvider; the picker is then empty.
vi.mock('@/hooks/use-environments', () => ({
  useEnvironments: vi.fn(() => ({ data: [] })),
}));

import type { AppConfig, SessionMe } from '@/lib/api-types';
import { AppShell } from './AppShell';

const ME: SessionMe = {
  email: 'alice@example.com',
  name: 'Alice Example',
  is_admin: false,
  openwork_enabled: false,
  suspend_propagation_seconds: 60,
  endpoint: 'https://api.acme.ai',
};

const CONFIG = { links: {} } as unknown as AppConfig;

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

describe('AppShell — CHAT link', () => {
  it('CHAT is always an external link to chat.<domain>', () => {
    renderShell();
    const link = screen.getByRole('link', { name: 'Chat' });
    expect(link).toHaveAttribute('href', 'https://chat.acme.ai');
    expect(link).toHaveAttribute('target', '_blank');
  });
});

describe('AppShell — primary nav', () => {
  it('every nav item is a live link', () => {
    renderShell();
    expect(screen.getByRole('link', { name: 'Keys' })).toHaveAttribute('href', '/');
    expect(screen.getByRole('link', { name: 'Models' })).toHaveAttribute('href', '/models');
    expect(screen.getByRole('link', { name: 'MCPs' })).toHaveAttribute('href', '/mcp');
    expect(screen.getByRole('link', { name: 'A2A' })).toHaveAttribute('href', '/a2a');
    expect(screen.getByRole('link', { name: 'Stats' })).toHaveAttribute('href', '/stats');
    expect(screen.getByRole('link', { name: 'How-to' })).toHaveAttribute('href', '/howto');
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
    renderShell();
    // Radix opens on Enter (see App.test.tsx). Awaiting the item also lets the
    // scroll-lock effect run — so a false pass (lock applied late) can't slip by.
    fireEvent.keyDown(screen.getByRole('button', { name: 'User menu' }), { key: 'Enter' });
    expect(await screen.findByRole('menuitem', { name: 'Log out' })).toBeInTheDocument();
    expect(document.body.hasAttribute('data-scroll-locked')).toBe(false);
  });

  it('Log out POSTs /platform/console/session/logout as JSON (same-origin fetch, D-28)', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      status: 204,
      json: () => Promise.reject(new SyntaxError('no body')),
    } as unknown as Response);
    vi.stubGlobal('fetch', fetchMock);
    try {
      renderShell();
      fireEvent.keyDown(screen.getByRole('button', { name: 'User menu' }), { key: 'Enter' });
      fireEvent.click(await screen.findByRole('menuitem', { name: 'Log out' }));
      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toBe('/platform/console/session/logout');
      expect(init.method).toBe('POST');
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('opening the mobile hamburger menu does not lock <body>', async () => {
    renderShell();
    fireEvent.keyDown(screen.getByRole('button', { name: 'Open menu' }), { key: 'Enter' });
    expect(await screen.findByRole('menuitem', { name: 'How-to' })).toBeInTheDocument();
    expect(document.body.hasAttribute('data-scroll-locked')).toBe(false);
  });
});
