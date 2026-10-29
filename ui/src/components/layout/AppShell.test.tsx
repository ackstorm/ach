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
// The top progress line reads the shared query client; programmable here.
const fetching = vi.hoisted(() => ({ n: 0 }));
vi.mock('@tanstack/react-query', async (orig) => ({ ...(await orig<object>()), useIsFetching: () => fetching.n }));

vi.mock('@/hooks/use-environments', () => ({
  useEnvironments: vi.fn(() => ({ data: [] })),
}));

import type { AppConfig, SessionMe } from '@/lib/api-types';
import { AppShell } from './AppShell';
import { useSessionStore } from '@/stores/session';

const ME: SessionMe = {
  email: 'alice@example.com',
  name: 'Alice Example',
  is_admin: false,
  enterprise: true,
  openwork_enabled: false,
  suspend_propagation_seconds: 60,
  keys_used: null,
  max_keys: null,
  endpoint: 'https://api.acme.ai',
  chat_url: 'https://chat.acme.ai',
  provider_name: 'ai-platform',
};

const CONFIG = { links: {} } as unknown as AppConfig;

function renderShell(me: SessionMe = ME): void {
  useSessionStore.setState({ me });
  render(
    <MemoryRouter>
      <AppShell me={me} config={CONFIG} />
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('AppShell — CHAT link', () => {
  it('CHAT is an external link to the configured chat_url', () => {
    renderShell();
    const link = screen.getByRole('link', { name: 'Chat' });
    expect(link).toHaveAttribute('href', 'https://chat.acme.ai');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'));
    expect(link).toHaveClass('btn', 'accent');
  });

  it('no chat_url hides CHAT instead of guessing a host', () => {
    renderShell({ ...ME, chat_url: '' });
    expect(screen.queryByRole('link', { name: 'Chat' })).toBeNull();
  });
});

describe('AppShell — sidebar nav', () => {
  it('Overview first and standalone, then Access → Runtime → Observability → Help', () => {
    const { container } = render(<MemoryRouter><AppShell me={ME} config={CONFIG} /></MemoryRouter>);
    const groups = screen.getAllByRole('navigation').map((nav) => nav.getAttribute('aria-label'));
    expect(groups).toEqual(['Overview', 'Access', 'Runtime', 'Observability', 'Help']);
    expect([...container.querySelectorAll('.nav-label')].map((el) => el.textContent)).toEqual(['Access', 'Runtime', 'Observability', 'Help']);
    const side = container.querySelector('#console-navigation')!;
    expect(side.querySelector('nav a')).toHaveTextContent('Overview');
    expect(screen.getByRole('navigation', { name: 'Help' })).toContainElement(screen.getByRole('link', { name: 'How-to' }));
    expect(screen.queryByRole('combobox', { name: 'Activity period' })).toBeNull();
    expect(screen.getByRole('link', { name: 'Keys' })).toHaveAttribute('href', '/keys');
    expect(screen.getByRole('link', { name: 'Environments' })).toHaveAttribute('href', '/environments');
    expect(screen.getByRole('link', { name: 'How-to' })).toHaveAttribute('href', '/howto');
    expect(screen.getByRole('link', { name: 'Models' })).toHaveAttribute('href', '/models');
    expect(screen.getByRole('link', { name: 'MCPs' })).toHaveAttribute('href', '/mcp');
    expect(screen.getByRole('link', { name: 'A2A' })).toHaveAttribute('href', '/a2a');
    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute('href', '/');
    expect(screen.getByRole('link', { name: 'Traces' })).toHaveAttribute('href', '/analytics/traces');
  });

  it('Analytics is collapsed by default; Evals is listed disabled', () => {
    window.sessionStorage.clear();
    renderShell();
    const toggle = screen.getByRole('button', { name: 'Analytics' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(toggle.querySelector('svg.chevron')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Cost' })).toBeNull();
    fireEvent.click(toggle);
    expect(screen.getByRole('link', { name: 'Cost' })).toHaveAttribute('href', '/analytics/cost');
    expect(screen.getByRole('link', { name: 'Workloads' })).toHaveAttribute('href', '/analytics/workloads');
    expect(screen.getByText('Evals · Coming soon')).toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('link', { name: 'Cost' })).toBeNull();
  });

  it('breadcrumb: Overview has no group; How-to sits under Help', () => {
    const { container, unmount } = render(<MemoryRouter><AppShell me={ME} config={CONFIG} /></MemoryRouter>);
    expect(container.querySelector('.top')!.textContent).not.toMatch(/Observability/);
    expect(container.querySelector('.top strong')).toHaveTextContent('Overview');
    unmount();
    const howto = render(<MemoryRouter initialEntries={['/howto']}><AppShell me={ME} config={CONFIG} /></MemoryRouter>);
    expect(howto.container.querySelector('.top')!.textContent).toMatch(/Help\/How-to/);
  });

  it('no static workspace badge; the budget mini sits just above the user menu', () => {
    const { container } = render(<MemoryRouter><AppShell me={ME} config={CONFIG} /></MemoryRouter>);
    expect(screen.queryByText(/Personal workspace/)).toBeNull();
    expect(container.querySelector('.budget-mini')!.nextElementSibling).toHaveClass('profile');
  });

  it('shows a top progress line only while a query is fetching', () => {
    renderShell();
    expect(screen.queryByRole('progressbar', { name: 'Loading data' })).toBeNull();
    cleanup();
    fetching.n = 2;
    renderShell();
    expect(screen.getByRole('progressbar', { name: 'Loading data' })).toBeInTheDocument();
    fetching.n = 0;
  });

  it('the brand link shows ACH and the Agent Capability Hub tagline', () => {
    render(<MemoryRouter><AppShell me={ME} config={{ ...CONFIG, brand: 'ACH', tagline: 'Agent Capability Hub' }} /></MemoryRouter>);
    const brand = screen.getByRole('link', { name: 'Console home' });
    expect(brand).toHaveTextContent('ACH');
    expect(brand).toHaveTextContent('Agent Capability Hub');
  });

  it('the sidebar collapses to icons: aria-expanded flips, state persists, links stay named', () => {
    window.localStorage.clear();
    const first = render(<MemoryRouter><AppShell me={ME} config={CONFIG} /></MemoryRouter>);
    const toggle = screen.getByRole('button', { name: 'Collapse sidebar' });
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(toggle);
    expect(screen.getByRole('button', { name: 'Expand sidebar' })).toHaveAttribute('aria-expanded', 'false');
    expect(first.container.firstChild).toHaveClass('side-collapsed');
    first.unmount();
    const { container } = render(<MemoryRouter><AppShell me={ME} config={CONFIG} /></MemoryRouter>);
    expect(container.firstChild).toHaveClass('side-collapsed');
    for (const a of container.querySelectorAll('nav a')) { expect(a).toHaveAttribute('aria-label'); expect(a).toHaveAttribute('title', a.getAttribute('aria-label')); }
    expect(screen.queryByRole('button', { name: 'Analytics' })).toBeNull();
    expect(screen.queryByText('Evals · Coming soon')).toBeNull();
    for (const name of ['Cost', 'Performance', 'Runtime', 'Reliability', 'Workloads', 'Skills & context']) expect(screen.getByRole('link', { name })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Expand sidebar' }));
    expect(window.localStorage.getItem('console.sidebar.collapsed')).toBe('false');
  });

  it('the open phone drawer is never collapsed', () => {
    window.localStorage.setItem('console.sidebar.collapsed', 'true');
    const { container } = render(<MemoryRouter><AppShell me={ME} config={CONFIG} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: 'Open menu' }));
    expect(container.firstChild).not.toHaveClass('side-collapsed');
    window.localStorage.clear();
  });

  it('the budget mini says Unavailable when bootstrap carries no budget, never $0', () => {
    renderShell({ ...ME, budget: null });
    expect(screen.getByText('Unavailable')).toBeInTheDocument();
    cleanup();
    renderShell({ ...ME, budget: { spend: 12, max_budget: 100, budget_duration: '30d', reset_at: null } });
    expect(screen.getByText('of $100.00')).toBeInTheDocument();
  });

  it('the budget bar is neutral below 80% of the cap, amber from 80%, red from 90%', () => {
    for (const [spend, tone] of [[51, ''], [80, 'amber'], [92, 'red']] as const) {
      renderShell({ ...ME, budget: { spend, max_budget: 100, budget_duration: '30d', reset_at: null } });
      const bar = document.querySelector('.budget-mini .bar')!;
      expect(bar.classList.contains('amber')).toBe(tone === 'amber');
      expect(bar.classList.contains('red')).toBe(tone === 'red');
      cleanup();
    }
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

  it('the mobile hamburger opens the sidebar drawer; Escape closes it and restores focus', () => {
    renderShell();
    const open = screen.getByRole('button', { name: 'Open menu' });
    fireEvent.click(open);
    expect(open).toHaveAttribute('aria-expanded', 'true');
    expect(document.body.hasAttribute('data-scroll-locked')).toBe(false);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(open).toHaveAttribute('aria-expanded', 'false');
    expect(open).toHaveFocus();
  });
});
