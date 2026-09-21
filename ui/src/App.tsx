// App.tsx — the shell DRIVER, ported from src/ui/app.js App().
//
// On mount it kicks off ONE loadSession() and ONE loadConfig() (the boot
// fetches). It then feeds {status, hasLoaded} through the pure resolveState()
// to pick which of the five shell states to render:
//
//   loading | signin | authed | error | expired
//
// Render is a PURE function of state; side effects (boot fetches, the
// mid-session expiry redirect) live in effects.
//
// Carry-forwards applied:
//   C1 — ErrorCard gets `retrying={status === null}` so the retry button is
//        disabled while a loadSession is in flight (no stacked reloads).
//   C2 — the `authed` view falls through to ErrorCard when me === null; the
//        shell is NEVER rendered against a null me.
//   C3 — selector discipline: every store read is a narrow selector
//        (useSessionStore(s => s.x)), never a bare useSessionStore().

import { useEffect, useMemo } from 'react';
import { createHashRouter, Navigate, RouterProvider } from 'react-router';
import { resolveState } from '@/lib/resolve-state';
import { setUnauthorizedHandler } from '@/lib/on-unauthorized';
import { useSessionStore } from '@/stores/session';
import { useConfigStore } from '@/stores/config';
import type { AppConfig, SessionMe } from '@/lib/api-types';
import { AppShell } from '@/components/layout/AppShell';
import { LoadingCard } from '@/components/layout/LoadingCard';
import { ErrorCard } from '@/components/layout/ErrorCard';
import { Login } from '@/routes/Login';
import { Dashboard } from '@/routes/Dashboard';
import { Stats } from '@/routes/Stats';
import { Models } from '@/routes/Models';
import { Mcp } from '@/routes/Mcp';
import { A2a } from '@/routes/A2a';
import { HowTo } from '@/routes/HowTo';

// Mid-session expiry redirects to the BARE login (no landing card) — FIXED
// literal, never built from a prop/query/hash. Parity with app.js.
const EXPIRED_REDIRECT_URL = '/api/oauth/login';

// The authenticated hash router. Built ONCE per mount of the authed view so
// route state survives re-renders; `me`/`config` are threaded into AppShell as
// props (not closed over here — AuthedRouter re-reads them per render).
function makeRouter(me: SessionMe, config: AppConfig) {
  return createHashRouter([
    {
      path: '/',
      element: <AppShell me={me} config={config} />,
      children: [
        { index: true, element: <Dashboard me={me} /> },
        { path: 'stats', element: <Stats /> },
        { path: 'models', element: <Models /> },
        { path: 'mcp', element: <Mcp /> },
        { path: 'a2a', element: <A2a /> },
        { path: 'howto', element: <HowTo /> },
      ],
    },
    // Any unknown hash falls back to the dashboard (allow-list parity with the
    // old hash router; the hash never becomes a redirect target). Without this
    // redirect an unknown hash matched AppShell with NO children -> an empty
    // <Outlet/> -> topbar with a BLANK main region.
    { path: '*', element: <Navigate to="/" replace /> },
  ]);
}

// AuthedRouter — mounts the hash router for the authed view. The router instance
// is memoized so route navigation persists; `me`/`config` are stable references
// from the stores within a single authed session, so re-deriving the router only
// when they change is safe and keeps AppShell props fresh.
function AuthedRouter({ me, config }: { me: SessionMe; config: AppConfig }) {
  const router = useMemo(() => makeRouter(me, config), [me, config]);
  return <RouterProvider router={router} />;
}

export function App() {
  // C3: narrow selectors only — never a bare useSessionStore().
  const status = useSessionStore((s) => s.status);
  const hasLoaded = useSessionStore((s) => s.hasLoaded);
  const me = useSessionStore((s) => s.me);
  const loadSession = useSessionStore((s) => s.loadSession);
  const markExpired = useSessionStore((s) => s.markExpired);
  const config = useConfigStore((s) => s.config);
  const loadConfig = useConfigStore((s) => s.loadConfig);

  // Boot: register the mid-session 401 handler, then ONE loadSession + ONE
  // loadConfig on mount. Any /api/session/* 401 after load (e.g. a Models/Stats
  // fetch once the cookie expired) marks the session expired -> resolveState maps
  // that to 'expired' -> the redirect effect below navigates to login.
  useEffect(() => {
    setUnauthorizedHandler(() => markExpired());
    loadSession();
    loadConfig();
    return () => setUnauthorizedHandler(null);
  }, [loadSession, loadConfig, markExpired]);

  const view = resolveState(status, hasLoaded);

  // Mid-session expiry: silent redirect, no card. Done as an effect so render
  // stays a pure function of state. The `expired` view renders the loading card
  // as a brief placeholder while the browser navigates.
  useEffect(() => {
    if (view === 'expired') {
      window.location.href = EXPIRED_REDIRECT_URL;
    }
  }, [view]);

  if (view === 'authed') {
    // C2: never render the shell against a null me — fall through to ErrorCard.
    if (me == null) {
      return <ErrorCard onRetry={loadSession} retrying={status === null} />;
    }
    return <AuthedRouter me={me} config={config} />;
  }

  if (view === 'signin') {
    return <Login config={config} />;
  }

  if (view === 'error') {
    // C1: disable retry while a reload is in flight (status === null).
    return <ErrorCard onRetry={loadSession} retrying={status === null} />;
  }

  // "loading" and "expired" both render the loading card (expired also triggers
  // the redirect effect above, so the card is only a brief placeholder).
  return <LoadingCard />;
}
