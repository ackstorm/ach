// AppShell.tsx — the authenticated console shell: a sidebar (brand, Overview →
// Access → Runtime → Observability → Help navigation, budget mini, user menu)
// and a main column with a top bar (crumb, Chat/Status links, theme toggle)
// over the routed page. The activity period lives in each page header.
//
// On a phone the sidebar is an off-canvas drawer: the hamburger opens it, focus
// moves into it and is trapped there, Escape or the scrim closes it and focus
// returns to the hamburger. On a desktop the sidebar collapses to icons only
// (brand-row toggle, kept in localStorage); the open phone drawer is always full.
//
// `me` is GUARANTEED non-null here (App.tsx falls through to ErrorCard when
// me === null), so the shell never renders against a null me.

import { ChevronDown, ExternalLink, LogOut, Menu, PanelLeftClose, PanelLeftOpen } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { Link, Outlet, useLocation } from 'react-router';
import { useIsFetching } from '@tanstack/react-query';
import type { AppConfig, SessionMe } from '@/lib/api-types';
import { CreateKeyModal } from '@/components/keys/CreateKeyModal';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Toaster } from '@/components/ui/toast';
import { postJson } from '@/lib/api';
import { formatCurrency } from '@/lib/format';
import { BrandLockup } from './BrandLockup';
import { ConsoleNavigation } from './ConsoleNavigation';
import { ThemeToggle } from './ThemeToggle';
import { eeTitles } from '@ee/routes';
import '@/styles/console-ledger.css';
import '@/styles/ledger-ui.css';

export interface AppShellProps {
  me: SessionMe;
  config: AppConfig;
}

// Two-letter avatar initials from a display label: first char of the first and
// last word (e.g. "Juan Carlos Moreno" -> "JM", "alice@acme.com" -> "AL").
function initialsOf(label: string): string {
  const parts = (label.split('@')[0] || '').split(/[\s._-]+/).filter(Boolean);
  if (parts.length === 0) return '?';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

const TITLES: Record<string, string> = {
  '/': 'Overview', ...eeTitles,
  '/keys': 'Keys', '/environments': 'Environments', '/howto': 'How-to',
  '/models': 'Models', '/mcp': 'MCPs', '/a2a': 'A2A',
};
const RUNTIME = ['/models', '/mcp', '/a2a'];
const ACCESS = ['/keys', '/environments'];

export function AppShell({ me, config }: AppShellProps) {
  const [navOpen, setNavOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => {
    try { return window.localStorage.getItem('console.sidebar.collapsed') === 'true'; } catch { return false; }
  });
  useEffect(() => {
    try { window.localStorage.setItem('console.sidebar.collapsed', String(collapsed)); } catch { /* storage blocked */ }
  }, [collapsed]);
  const slim = collapsed && !navOpen;
  const trigger = useRef<HTMLButtonElement>(null);
  const sidebar = useRef<HTMLElement>(null);
  const scroll = useRef<HTMLElement>(null);
  const location = useLocation();
  const fetching = useIsFetching() > 0;
  useEffect(() => { if (scroll.current) scroll.current.scrollTop = 0; }, [location.pathname, location.search.includes('view=full')]);
  const closeNav = () => { setNavOpen(false); trigger.current?.focus(); };
  useEffect(() => {
    if (!navOpen) return;
    sidebar.current?.querySelector<HTMLAnchorElement>('nav a')?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setNavOpen(false); trigger.current?.focus(); }
      if (event.key === 'Tab') {
        const items = [...(sidebar.current?.querySelectorAll<HTMLElement>('a[href], button, select') ?? [])].filter((item) => item.getClientRects().length > 0);
        const first = items[0]; const last = items.at(-1);
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
      }
    };
    document.addEventListener('keydown', key);
    return () => document.removeEventListener('keydown', key);
  }, [navOpen]);

  const menuLabel = me.name || me.email;
  const title = TITLES[location.pathname] || 'Console';
  const group = location.pathname === '/' ? null : location.pathname === '/howto' ? 'Help' : RUNTIME.includes(location.pathname) ? 'Runtime' : ACCESS.includes(location.pathname) ? 'Access' : 'Observability';
  // The caller's own user:<email> tag, the ceiling the forwarder enforces.
  // Absent = unavailable, never a fabricated $0.
  const budget = me.budget ?? null;
  const cap = budget?.max_budget ?? null;
  const percent = budget && cap != null && cap > 0 ? Math.min(100, Math.max(0, budget.spend / cap * 100)) : 0;
  // Neutral below 80% of the cap, amber from 80%, red from 90%.
  const tone = percent >= 90 ? 'red' : percent >= 80 ? 'amber' : '';
  const links = config.links ?? {};

  return <div className={'console-ledger' + (navOpen ? ' nav-open' : '') + (slim ? ' side-collapsed' : '')}>
    <aside ref={sidebar} className="side" id="console-navigation">
      <div className="brand-row">
        <Link to="/" className="brand" aria-label="Console home" onClick={closeNav}><BrandLockup config={config} subtitle={config.tagline} /></Link>
        <button type="button" className="side-toggle" aria-label={slim ? 'Expand sidebar' : 'Collapse sidebar'} title={slim ? 'Expand sidebar' : 'Collapse sidebar'} aria-controls="console-navigation" aria-expanded={!slim} onClick={() => setCollapsed(!slim)}>{slim ? <PanelLeftOpen aria-hidden="true" /> : <PanelLeftClose aria-hidden="true" />}</button>
      </div>
      <ConsoleNavigation onNavigate={closeNav} collapsed={slim} />
      <div className="budget-mini" title={slim ? (budget ? `${formatCurrency(budget.spend)}${cap == null ? '' : ` of ${formatCurrency(cap)}`}` : 'Budget unavailable') : undefined}><div className={`bar ${tone}`}><i style={{ width: `${percent}%` }} /></div><div className={`minirow ${tone}`}><strong>{budget ? formatCurrency(budget.spend) : 'Unavailable'}</strong><span>{!budget ? 'budget unknown' : cap == null ? 'no limit' : `of ${formatCurrency(cap)}`}</span></div></div>
      <div className="profile">
        {/* `modal={false}`: a tiny identity menu must not scroll-lock and mutate <body>. */}
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger aria-label="User menu" className="ledger-user-trigger">
            <span className="grid size-8 shrink-0 place-items-center rounded-full bg-primary/15 font-sans text-xs font-semibold text-primary sm:size-6 sm:text-[10px]">
              {initialsOf(menuLabel)}
            </span>
            <span className="profile-info"><b>{menuLabel}</b><small>SSO session active</small></span>
            <ChevronDown aria-hidden="true" className="hidden size-3.5 text-text-secondary transition-transform group-data-[state=open]:rotate-180 sm:block" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-[13rem]">
            <div className="px-2 py-1.5">
              <p className="truncate font-sans text-xs font-medium text-text-primary">{me.name || me.email}</p>
              {me.name ? <p className="truncate font-mono text-[11px] text-text-secondary">{me.email}</p> : null}
            </div>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              {/* Logout is a POST (D-28 CSRF: same-origin fetch, never a GET
                  link); the web session is deleted, then a full reload lands
                  on the sign-in card. */}
              <button
                type="button"
                onClick={async () => {
                  await postJson('/platform/console/session/logout', {});
                  window.location.href = '/';
                }}
              >
                <LogOut aria-hidden="true" />
                Log out
              </button>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </aside>
    {navOpen && <button type="button" className="nav-scrim on" aria-label="Close navigation" onClick={closeNav} />}
    <div className="main">
      <header className="top">
        <button ref={trigger} type="button" className="btn hamb" aria-label="Open menu" aria-controls="console-navigation" aria-expanded={navOpen} onClick={() => setNavOpen(true)}><Menu aria-hidden="true" /></button>
        {group && <><span className="crumb crumb-context">{group}</span><span className="crumb crumb-context">/</span></>}<span className="crumb"><strong>{title}</strong></span>
        <span className="grow" />
        <div className="utility-links">
          {/* Chat URL is deployment config (chart platformApi.console.chatUrl); unset hides it. */}
          {me.chat_url && <a className="btn accent" href={me.chat_url} target="_blank" rel="noopener noreferrer">Chat <ExternalLink aria-hidden="true" className="inline size-3" /></a>}
          {links.status && <a href={links.status} target="_blank" rel="noopener noreferrer">Status</a>}
        </div>
        <ThemeToggle />
        {fetching && <span className="top-progress" role="progressbar" aria-label="Loading data" />}
      </header>
      <main ref={scroll} className="scroll"><div className="page"><Outlet /></div></main>
      {/* Overlays mounted at the authed root: the Keys page CTA opens the SAME
          modal via useCreateKeyModalStore; toasts only fire in authed flows. */}
      <CreateKeyModal /><Toaster />
    </div>
  </div>;
}
