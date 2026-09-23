// App.test.tsx — smoke tests for the 5-state shell driver (Task 2.4).
//
// Strategy: drive the driver by setting the Zustand stores DIRECTLY via
// setState (no real fetch). The boot effect's loadSession/loadConfig are
// replaced with vi.fn() spies so mounting App does NOT hit the network and we
// can assert the error-retry calls loadSession. resolveState maps:
//
//   status null              -> loading  -> LoadingCard
//   401 & !hasLoaded         -> signin   -> Login (CTA -> /platform/console/session/login?next=%2F)
//   0 / 5xx                  -> error    -> ErrorCard (retry calls loadSession; disabled when status===null)
//   200 & me                 -> authed   -> AppShell (brand + email + Log out button -> POST /platform/console/session/logout)
//   200 & me === null        -> authed   -> ErrorCard (C2 fall-through)
//
// Each test resets BOTH stores to a known state in beforeEach so they stay
// deterministic and isolated.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactElement } from 'react';
import { App } from './App';
import { useSessionStore, initialSessionState } from './stores/session';
import { useConfigStore, initialConfigState, DEFAULT_CONFIG } from './stores/config';
import type { SessionMe } from './lib/api-types';
import { setUnauthorizedHandler, notifyUnauthorized } from './lib/on-unauthorized';

// The authed dashboard now mounts KeysTable + the dashboard's own useKeys()
// query, so App must render inside a QueryClientProvider. A fresh, retry-free
// client per render keeps the keys query from firing real retries in jsdom (it
// simply lands in its pending/error branch — these are shell-level smoke tests,
// they assert on the topbar/greeting, not on keys data).
function renderApp(): ReturnType<typeof render> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const ui: ReactElement = (
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  );
  return render(ui);
}

const ME: SessionMe = {
  email: 'alice@example.com',
  name: 'Alice Example',
  is_admin: false,
  openwork_enabled: false,
  suspend_propagation_seconds: 60,
  keys_used: null,
  max_keys: null,
  endpoint: 'https://litellm.example.com',
};

// Spies stand in for the store boot actions so mounting App never fetches.
let loadSessionSpy: ReturnType<typeof vi.fn<() => Promise<void>>>;
let markExpiredSpy: ReturnType<typeof vi.fn<() => void>>;

beforeEach(() => {
  loadSessionSpy = vi.fn<() => Promise<void>>();
  markExpiredSpy = vi.fn<() => void>();
  // Replace whole state, re-supplying the (spied) actions.
  useSessionStore.setState(
    { ...initialSessionState, loadSession: loadSessionSpy, markExpired: markExpiredSpy },
    true,
  );
  useConfigStore.setState({ ...initialConfigState }, true);
});

afterEach(() => {
  cleanup();
  setUnauthorizedHandler(null);
  // Reset the hash so hash-router tests stay isolated (a leftover #/garbage or
  // #/stats would leak into the next render).
  window.location.hash = '';
});

describe('App driver — boot', () => {
  it('calls loadSession once on mount', () => {
    renderApp();
    expect(loadSessionSpy).toHaveBeenCalledTimes(1);
  });
});

describe('App driver — loading', () => {
  it('status null -> LoadingCard with locked copy', () => {
    renderApp(); // initial status is null
    expect(screen.getByText('INITIALIZING')).toBeInTheDocument();
    expect(screen.getByText('Connecting to ACH...')).toBeInTheDocument();
  });
});

describe('App driver — signin', () => {
  it('cold-load 401 -> Login with CTA pointing at /platform/console/session/login?next=%2F', () => {
    useSessionStore.setState({ status: 401, hasLoaded: false, me: null });
    renderApp();
    const cta = screen.getByRole('link', { name: 'Continue with SSO' });
    expect(cta).toHaveAttribute('href', '/platform/console/session/login?next=%2F');
  });
});

describe('App driver — error', () => {
  it('network failure (status 0) -> ErrorCard, retry calls loadSession', () => {
    useSessionStore.setState({ status: 0, hasLoaded: false, me: null });
    renderApp();
    expect(screen.getByText('Service Unavailable')).toBeInTheDocument();

    const retry = screen.getByRole('button', { name: 'retry' });
    // status is 0 (not null) -> not retrying -> button enabled.
    expect(retry).not.toBeDisabled();

    loadSessionSpy.mockClear(); // ignore the boot-effect call
    fireEvent.click(retry);
    expect(loadSessionSpy).toHaveBeenCalledTimes(1);
  });

  it('5xx -> ErrorCard, retry enabled (status !== null)', () => {
    useSessionStore.setState({ status: 503, hasLoaded: false, me: null });
    renderApp();
    const retry = screen.getByRole('button', { name: 'retry' });
    expect(retry).not.toBeDisabled(); // status 503 !== null
  });
});

describe('App driver — authed', () => {
  it('200 + me -> AppShell shows brand, name and the user-menu Log out button', async () => {
    useSessionStore.setState({ status: 200, hasLoaded: true, me: ME });
    renderApp();
    // DEFAULT_CONFIG brand "ACH" with no accent segment -> one span.
    expect(screen.getByText('ACH')).toBeInTheDocument();
    // The user-menu label AND the dashboard greeting both render the name, so
    // it appears more than once — assert at least one is present.
    expect(screen.getAllByText(ME.name).length).toBeGreaterThan(0);
    // Logout now lives inside the user menu — open it (Radix opens on Enter),
    // then assert the only item is a Log out BUTTON (logout is a POST — D-28
    // CSRF: a same-origin fetch, never a GET link).
    fireEvent.keyDown(screen.getByRole('button', { name: 'User menu' }), {
      key: 'Enter',
    });
    const logout = await screen.findByRole('menuitem', { name: 'Log out' });
    expect(logout.tagName).toBe('BUTTON');
    expect(logout).not.toHaveAttribute('href');
    // The dashboard mounts in the content slot — the greeting proves it.
    expect(screen.getByText(/Welcome back,/)).toBeInTheDocument();
  });

  it('C2: 200 but me === null -> ErrorCard (never renders the shell)', () => {
    useSessionStore.setState({ status: 200, hasLoaded: true, me: null });
    renderApp();
    expect(screen.getByText('Service Unavailable')).toBeInTheDocument();
    // The shell must NOT have mounted (no user menu).
    expect(screen.queryByRole('button', { name: 'User menu' })).toBeNull();
  });

  it('unknown hash (#/garbage) redirects to the index Dashboard', async () => {
    // Regression for the `*` route: an unknown hash must Navigate to "/" so the
    // index Dashboard renders, not an empty <Outlet/> with a blank main region.
    window.location.hash = '#/garbage';
    useSessionStore.setState({ status: 200, hasLoaded: true, me: ME });
    renderApp();
    // The Navigate redirect is a client-side navigation; wait for the index
    // Dashboard to mount in the content slot (the greeting proves we landed
    // on "/").
    await waitFor(() =>
      expect(screen.getByText(/Welcome back,/)).toBeInTheDocument(),
    );
  });
});

describe('App driver — active nav (aria-current)', () => {
  it('Stats NavLink has aria-current="page" at /stats, not at /', () => {
    // At the index route the Stats link is NOT current.
    useSessionStore.setState({ status: 200, hasLoaded: true, me: ME });
    const { unmount } = renderApp();
    expect(screen.getByRole('link', { name: 'Stats' })).not.toHaveAttribute(
      'aria-current',
    );
    unmount();
    cleanup();

    // At #/stats react-router v7 sets aria-current="page" on the active NavLink.
    window.location.hash = '#/stats';
    useSessionStore.setState({ status: 200, hasLoaded: true, me: ME });
    renderApp();
    expect(screen.getByRole('link', { name: 'Stats' })).toHaveAttribute(
      'aria-current',
      'page',
    );
  });
});

describe('App driver — brand lockup (empty accent_segment)', () => {
  it('empty accent_segment renders the whole brand with no accent span', () => {
    useSessionStore.setState({ status: 200, hasLoaded: true, me: ME });
    useConfigStore.setState({
      config: { ...DEFAULT_CONFIG, brand: 'ACKStorm', accent_segment: '' },
    });
    renderApp();
    // Whole brand renders as one string; no "-auth" accent span.
    expect(screen.getByText('ACKStorm')).toBeInTheDocument();
    expect(screen.queryByText('-auth')).toBeNull();
  });
});

describe('App driver — mid-session 401 wiring', () => {
  it('registers a handler so notifyUnauthorized() calls markExpired', () => {
    // Authed shell so App mounts normally and the boot effect runs.
    useSessionStore.setState({ status: 200, hasLoaded: true, me: ME });
    renderApp();

    notifyUnauthorized();

    expect(markExpiredSpy).toHaveBeenCalledTimes(1);
  });
});
