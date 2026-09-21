// AppShell.tsx — the authenticated WIDE shell, ported from app.js AuthedShell.
//
// A ~56px topbar (two-tone brand lockup + Stats/Status nav + connected pulse-dot
// + {name|email} + sign out), a centered ~1200px content region holding the
// matched route via <Outlet/>, and the shared footer. AppShell is just the
// chrome: each ROUTE owns its own content layout (the dashboard composes its own
// right sidebar next to a full-width keys table; /stats renders full width), so
// there is no shell-level sidebar slot to toggle per route.
//
// Real-links-only (D-02/D-03): the Status nav item renders an external anchor
// ONLY when config.links.status is set; otherwise it renders a connected
// pulse-dot label. No nav/footer anchor is built from a non-existent link.
//
// `me` is GUARANTEED non-null here (App.tsx falls through to ErrorCard when
// me === null — carry-forward C2), so the shell never renders against a null me.

import { Fragment } from 'react';
import { ChevronDown, ExternalLink, LogOut, Menu } from 'lucide-react';
import { Outlet, NavLink } from 'react-router';
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
import { useKeys } from '@/hooks/use-keys';
import { postJson } from '@/lib/api';
import { cn } from '@/lib/utils';
import { deriveSubdomainUrl } from '@/lib/urls';
import { BrandLockup } from './BrandLockup';
import { SiteFooter } from './SiteFooter';
import { ThemeToggle } from './ThemeToggle';

export interface AppShellProps {
  me: SessionMe;
  config: AppConfig;
}

// A thin "|" divider between nav items so the group reads as KEYS | MODELS | ….
// Module-level (stable component identity) so it never remounts on parent render.
// Two-letter avatar initials from a display label: first char of the first and
// last word (e.g. "Juan Carlos Moreno" -> "JM", "alice@acme.com" -> "AL"). The
// email local-part is used when there is no name; punctuation splits into words.
function initialsOf(label: string): string {
  const parts = (label.split('@')[0] || '').split(/[\s._-]+/).filter(Boolean);
  if (parts.length === 0) return '?';
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

export function AppShell({ me, config }: AppShellProps) {
  const links = config.links ?? {};
  // User-menu label prefers the display name, falling back to email (UI-SPEC
  // copy table). Rendered as a text child only — never raw HTML (T-10-14).
  const menuLabel = me.name || me.email;

  // Shared NavLink class: L1 primary nav as an UNDERLINE tab strip — the top of
  // the app's green selected-state hierarchy (L1 underline > L2/L3 tint pills).
  // The active item carries a green underline + green text; idle items keep a
  // transparent underline so the row never reflows on selection. `leading-none`
  // pins the text box to the glyph height so the UPPERCASE labels optically
  // center next to the lowercase brand.
  const navLinkClass = ({ isActive }: { isActive: boolean }) =>
    cn(
      'border-b-2 px-1 py-1.5 font-mono text-[11px] font-semibold uppercase leading-none tracking-wider transition-colors',
      isActive
        ? 'border-primary text-primary'
        : 'border-transparent text-text-secondary hover:text-text-primary'
    );

  // CHAT lives on the RIGHT, next to the user — an accent CTA that clearly reads
  // as a clickable destination (tinted accent fill + accent border + hover lift +
  // an open-in-new-tab icon). It stays distinct from the SELECTED nav tab (a
  // mono-uppercase green underline) via sentence-case sans type, the soft fill,
  // the shadow, and the external icon. Gated on the user having a
  // default key — Chat needs one to authenticate; a default is never auto-assigned
  // (explicit-only), so when none exists it renders disabled with a hint.
  const { data: keys } = useKeys();
  const hasDefault = (keys ?? []).some((k) => k.is_default);
  // Explicit chat URL when the deployment sets CHAT_PUBLIC_URL; otherwise derive
  // chat.<domain> from the gateway host.
  const chatUrl = config.chat_public_url || deriveSubdomainUrl(me.endpoint, 'chat');
  const chatPill =
    'inline-flex h-8 items-center gap-1.5 rounded-lg px-3 font-sans text-xs font-semibold leading-none transition-all';
  const chatEl = hasDefault ? (
    <a
      href={chatUrl}
      target="_blank"
      rel="noopener noreferrer"
      className={cn(
        chatPill,
        'border border-primary/60 bg-primary/10 text-primary shadow-sm hover:-translate-y-px hover:bg-primary/20 hover:shadow-md'
      )}
    >
      Chat
      <ExternalLink className="size-3.5" aria-hidden="true" />
    </a>
  ) : (
    <span
      aria-disabled="true"
      title="You need a default key — set one on the Keys tab"
      className={cn(
        chatPill,
        'cursor-not-allowed border border-border text-text-tertiary'
      )}
    >
      Chat
      <ExternalLink className="size-3.5 opacity-50" aria-hidden="true" />
    </span>
  );

  // Single source of truth for the primary nav, rendered two ways: an inline row
  // on md+ and a collapsed hamburger menu on mobile (so the header never overflows
  // the viewport — a too-wide header is what silently breaks `position: sticky` on
  // phones). Models/MCPs/A2A are per-user catalogs scoped through the default key,
  // so they are gated like CHAT: a real link when a default exists, else a
  // disabled, muted item with a hint (the route itself also shows the prompt).
  const navItems: { to: string; label: string; gated: boolean; end?: boolean }[] = [
    { to: '/', label: 'Keys', gated: false, end: true },
    { to: '/models', label: 'Models', gated: true },
    { to: '/mcp', label: 'MCPs', gated: true },
    { to: '/a2a', label: 'A2A', gated: true },
    { to: '/stats', label: 'Stats', gated: false },
    { to: '/howto', label: 'How-to', gated: false },
  ];
  const gateTitle = 'You need a default key — set one on the Keys tab';
  const disabledNavClass =
    'cursor-not-allowed border-b-2 border-transparent px-1 py-1.5 font-mono text-[11px] font-semibold uppercase leading-none tracking-wider text-text-tertiary';

  // Service-status indicator (D-02/D-03). An external link when config.links.status
  // is set, else a connected pulse-dot "operational" label. Moved OUT of the
  // topbar nav and into the footer's (otherwise empty) right side.
  const statusIndicator = links.status ? (
    <a
      href={links.status}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-1.5 font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary transition-colors hover:text-text-primary"
    >
      Status
    </a>
  ) : (
    <span
      role="status"
      aria-label="All systems operational"
      title="All systems operational"
      className="inline-flex cursor-default items-center gap-1.5 font-mono text-[11px] font-semibold uppercase tracking-wider text-primary"
    >
      <span
        aria-hidden="true"
        className="size-2 animate-pulse rounded-full bg-primary shadow-[0_0_8px_var(--primary)]"
      />
      READY
    </span>
  );

  return (
    <div className="flex min-h-screen flex-col bg-background bg-fixed [background-image:radial-gradient(ellipse_70%_55%_at_12%_-5%,var(--glow-1),transparent),radial-gradient(ellipse_65%_55%_at_88%_8%,var(--glow-2),transparent),radial-gradient(ellipse_80%_65%_at_50%_105%,var(--glow-3),transparent)]">
      <header className="sticky top-0 z-40 flex h-14 shrink-0 items-center gap-4 border-b border-border bg-surface px-4 md:gap-6 md:px-6">
        {/* Mobile: the nav collapses into a hamburger so the header fits the
            viewport width (overflow here is what breaks sticky on phones).
            `modal={false}`: this is a pure navigation menu, so it must NOT
            scroll-lock the page. Radix's default modal mode mounts
            react-remove-scroll, which mutates <body> (overflow:hidden,
            position:relative, pointer-events:none, scrollbar-gap padding) while
            open — on a narrow viewport that reflows the page content into a
            collapsed, one-word-per-line column. Non-modal removes all of it. */}
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger
            aria-label="Open menu"
            className="inline-flex size-9 shrink-0 items-center justify-center rounded-md text-text-secondary outline-none transition-colors hover:bg-primary/5 hover:text-text-primary focus-visible:ring-2 focus-visible:ring-ring/50 md:hidden"
          >
            <Menu aria-hidden="true" className="size-5" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-[12rem]">
            {navItems.map((item) =>
              item.gated && !hasDefault ? (
                <DropdownMenuItem
                  key={item.to}
                  disabled
                  title={gateTitle}
                  className="font-mono text-[11px] font-semibold uppercase tracking-wider"
                >
                  {item.label}
                </DropdownMenuItem>
              ) : (
                <DropdownMenuItem
                  key={item.to}
                  asChild
                  className="font-mono text-[11px] font-semibold uppercase tracking-wider"
                >
                  <NavLink to={item.to} end={item.end}>
                    {item.label}
                  </NavLink>
                </DropdownMenuItem>
              )
            )}
          </DropdownMenuContent>
        </DropdownMenu>

        <BrandLockup config={config} className="text-sm" />

        {/* Desktop: inline underline tab strip, KEYS  MODELS  …. `end` on Keys
            keeps it active ONLY on the exact "/" route, not nested paths. */}
        <nav aria-label="Primary" className="hidden items-center gap-4 md:flex lg:gap-5">
          {navItems.map((item) => (
            <Fragment key={item.to}>
              {item.gated && !hasDefault ? (
                <span aria-disabled="true" title={gateTitle} className={disabledNavClass}>
                  {item.label}
                </span>
              ) : (
                <NavLink to={item.to} end={item.end} className={navLinkClass}>
                  {item.label}
                </NavLink>
              )}
            </Fragment>
          ))}
        </nav>

        <div className="ml-auto flex items-center gap-2 md:gap-3">
          {/* CHAT — separate destination, low-contrast outline pill, just before
              the user menu. Gated on a default key (disabled span otherwise). */}
          {chatEl}
          {/* User menu: an avatar + name trigger opening a small dropdown whose
              only action (for now) is Log out. Replaces the inline name + "sign
              out" anchor so the identity reads as one affordance.
              `modal={false}` for the same reason as the hamburger above: a tiny
              nav/identity menu must not scroll-lock and mutate <body>. */}
          <DropdownMenu modal={false}>
            <DropdownMenuTrigger
              aria-label="User menu"
              className="group inline-flex items-center gap-2 rounded-full outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring/50 sm:h-8 sm:rounded-lg sm:border sm:border-border sm:bg-surface sm:pl-1 sm:pr-2 sm:hover:border-primary/40 sm:hover:bg-primary/5 sm:data-[state=open]:border-primary/40 sm:data-[state=open]:bg-primary/5"
            >
              {/* Mobile compacts to JUST the rounded avatar (no pill, name, or
                  chevron); the full pill returns at sm+. */}
              <span className="grid size-8 shrink-0 place-items-center rounded-full bg-primary/15 font-sans text-xs font-semibold text-primary sm:size-6 sm:text-[10px]">
                {initialsOf(menuLabel)}
              </span>
              <span className="hidden max-w-[14rem] truncate font-sans text-xs font-medium text-text-primary sm:block">
                {menuLabel}
              </span>
              <ChevronDown
                aria-hidden="true"
                className="hidden size-3.5 text-text-secondary transition-transform group-data-[state=open]:rotate-180 sm:block"
              />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="min-w-[13rem]">
              <div className="px-2 py-1.5">
                <p className="truncate font-sans text-xs font-medium text-text-primary">
                  {me.name || me.email}
                </p>
                {me.name ? (
                  <p className="truncate font-mono text-[11px] text-text-secondary">
                    {me.email}
                  </p>
                ) : null}
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
          <ThemeToggle />
        </div>
      </header>

      <div className="mx-auto flex w-full max-w-[1200px] flex-1 flex-col px-6 py-8">
        {/* Routes own their own content layout: the dashboard composes its own
            right sidebar alongside a full-width keys table; /stats renders full
            width. AppShell is just the chrome (topbar + footer + overlays). */}
        <main className="min-w-0 flex-1 animate-content-in">
          <Outlet />
        </main>
      </div>

      {/* Shared footer, on every authed route. Full-width transversal bar that
          mirrors the topbar. The service-status indicator (moved out of the
          topbar nav) rides in the footer's right slot. */}
      <SiteFooter config={config} rightSlot={statusIndicator} />

      {/* Store-driven / queue-driven overlays mounted at the authed shell root.
          The CreateKeyModal is mounted HERE (not in the dashboard) so both the
          dashboard `+ New Key` CTA and the sidebar `Create key` shortcut open
          the SAME modal via useCreateKeyModalStore. The Toaster is mounted here
          (not at App root) because toasts only fire in authed flows (create /
          delete) and AppShell is the authed root — this scopes them correctly
          while guaranteeing they render wherever a toast can fire. Both render
          nothing when idle. */}
      <CreateKeyModal />
      <Toaster />
    </div>
  );
}
