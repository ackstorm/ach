// Login.tsx — the OIDC sign-in landing (Task 2.5).
//
// Ported from src/ui/login.js: a config-aware topbar (brand lockup + tagline +
// Docs/Status/Support nav) ABOVE a single CENTERED Sign-in card, with the shared
// SiteFooter below. The card shows the READY status row, brand shield, "Sign in"
// heading, the config-aware sub copy, the `Continue with SSO` accent CTA, a
// reassurance line, and the labeled "BACKED BY" provider chips. (The former
// right-hand value-props / OVERVIEW teaser column was removed — the card now
// stands alone, centered.)
//
// THEMING: this page is fully token-driven, so it follows the active theme
// (dark / light / pastel) chosen by the topbar cycle. The decorative background
// glow blobs read the per-theme --glow-1/2/3 vars (subtle green in dark/light,
// vivid lavender·cyan·pink in pastel).
//
// SECURITY INVARIANTS (parity with login.js / threat register 09-06 / 14):
//   • T-09-17 (open-redirect): the `Continue with SSO` CTA is the ONLY redirect
//     trigger and points at the FIXED literal /api/oauth/login?action=ui — it is
//     NEVER built from a prop/query/next/hash. Docs/Status/Support + footer links
//     come ONLY from the server-sourced config.links allow-list (a missing target
//     DROPS the link, never interpolates).
//   • T-09-18 (pre-auth data leak): this component performs NO fetch and renders
//     NO live data. The BACKED BY chips are hardcoded illustrative content.
//   • T-10-14 (XSS): all brand/tagline/link/provider strings render as React text
//     children (auto-escaped); href values come only from config.links.

import { useState } from 'react';
import { Loader2 } from 'lucide-react';
import type { AppConfig } from '@/lib/api-types';
import { Button } from '@/components/ui/button';
import { BrandLockup } from '@/components/layout/BrandLockup';
import { SiteFooter } from '@/components/layout/SiteFooter';
import { ThemeToggle } from '@/components/layout/ThemeToggle';
import { ProviderChips } from '@/components/auth/ProviderChips';
import { cn } from '@/lib/utils';
import { useThemeStore } from '@/stores/theme';

// The SOLE redirect trigger (T-09-17). FIXED literal — the ?action=ui param makes
// the callback eager-create the LiteLLM user WITHOUT minting a key (D-13). Never
// construct this from any prop/query/next/hash.
const SSO_LOGIN_URL = '/api/oauth/login?action=ui';

// Theme-aware decorative background: three radial glow blobs reading the per-theme
// --glow-* vars. Shared shape with the authed shell so login and inner pages match.
const GLOW_BG =
  '[background-image:radial-gradient(ellipse_70%_55%_at_12%_-5%,var(--glow-1),transparent),radial-gradient(ellipse_65%_55%_at_88%_8%,var(--glow-2),transparent),radial-gradient(ellipse_80%_65%_at_50%_105%,var(--glow-3),transparent)]';

export interface LoginProps {
  config: AppConfig;
}

export function Login({ config }: LoginProps) {
  const links = config.links ?? {};
  const brandShort = config.brand_short || 'LiteLLM';
  const tagline = config.tagline || '';
  const providers =
    Array.isArray(config.providers) && config.providers.length
      ? config.providers
      : [];

  // Pastel mode gets the richer treatment the user liked: a frosted-glass card
  // and a fuchsia→violet→sky gradient CTA. Dark/light stay token-flat (the green
  // button's ink is the --primary-foreground token: dark in dark, white in light).
  const isPastel = useThemeStore((s) => s.theme) === 'pastel';

  // The CTA is a real full-page navigation (T-09-17), so "pending" only needs to
  // flip the leading glyph to a spinner and block a double-click during the brief
  // redirect latency — the browser tears the page down on its own. Navigation is
  // NOT prevented; we just paint feedback on the way out.
  const [pending, setPending] = useState(false);

  return (
    <div data-state="signin" className={`flex min-h-screen flex-col bg-background ${GLOW_BG}`}>
      {/* ── Topbar — full-width bar (mirrors the authed shell): brand lockup +
          tagline on the left, real-links nav on the right. ──────────────────── */}
      <header className="flex h-14 shrink-0 items-center gap-4 border-b border-border bg-surface px-6">
        <div className="inline-flex flex-col gap-px">
          <BrandLockup config={config} className="text-sm" />
          {tagline ? (
            <span className="font-sans text-[11px] text-text-secondary">
              {tagline}
            </span>
          ) : null}
        </div>

        <nav
          aria-label="Resources"
          className="ml-auto flex items-center gap-6 font-mono text-[11px] font-semibold uppercase tracking-wider"
        >
          {links.docs ? (
            <a
              href={links.docs}
              target="_blank"
              rel="noopener noreferrer"
              className="text-text-secondary transition-colors hover:text-text-primary"
            >
              Docs
            </a>
          ) : null}
          {links.status ? (
            <a
              href={links.status}
              target="_blank"
              rel="noopener noreferrer"
              className="text-text-secondary transition-colors hover:text-text-primary"
            >
              Status
            </a>
          ) : null}
          {links.support ? (
            <a
              href={links.support}
              target="_blank"
              rel="noopener noreferrer"
              className="text-text-secondary transition-colors hover:text-text-primary"
            >
              Support
            </a>
          ) : null}
        </nav>

        <ThemeToggle />
      </header>

      {/* ── Body — a single, centered Sign-in card ─────────────────────────────── */}
      <div className="flex flex-1 items-center justify-center px-6 py-12">
        <main
          className={cn(
            'w-full max-w-[480px]',
            isPastel
              ? 'rounded-2xl border border-white/70 bg-white/70 shadow-[0_25px_70px_-20px_rgba(168,140,255,0.55)] ring-1 ring-violet-200/50 backdrop-blur-xl'
              : 'rounded-xl border border-border bg-card shadow-sm'
          )}
        >
          <div className="border-b border-border px-8 pt-8 pb-6">
            <BrandLockup
              config={config}
              className="text-2xl tracking-tight"
              iconClassName="size-[26px]"
            />
            <h1 className="mt-4 text-2xl font-semibold leading-tight text-text-primary">
              Sign in
            </h1>
            <p className="mt-4 text-sm leading-relaxed text-text-secondary">
              Authenticate to manage your{' '}
              <span className="text-text-primary">{brandShort} virtual keys</span>{' '}
              — view, mint, and revoke. No password is handled here; sign-in is
              delegated to your SSO provider.
            </p>

            {/* Matrix-style command line — decorative; the real action is the
                "Continue with SSO" button below. */}
            <div
              aria-hidden="true"
              className="mt-5 flex items-center gap-2 overflow-hidden font-mono text-[13px] whitespace-nowrap"
            >
              <span className="select-none text-text-tertiary">$</span>
              <span className="text-text-secondary">
                <span className="text-primary">sign-in</span> --sso
                --provider=openid
              </span>
              <span className="inline-block h-[1.05em] w-[0.5em] animate-blink bg-primary" />
            </div>
          </div>

          <div className="px-8 py-7">
            <Button
              asChild
              className={cn(
                'w-full font-semibold',
                isPastel &&
                  'border-0 bg-gradient-to-r from-fuchsia-300 via-violet-300 to-sky-300 text-violet-950 shadow-md shadow-violet-200/60 hover:from-fuchsia-400 hover:via-violet-400 hover:to-sky-400'
              )}
            >
              <a
                href={SSO_LOGIN_URL}
                onClick={() => setPending(true)}
                aria-busy={pending}
                className={cn(pending && 'pointer-events-none')}
              >
                {pending ? (
                  <Loader2 aria-hidden="true" className="animate-spin" />
                ) : (
                  <svg
                    viewBox="0 0 24 24"
                    aria-hidden="true"
                    className="fill-none"
                    strokeWidth={2}
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4" />
                    <path d="M10 17l5-5-5-5" />
                    <path d="M15 12H3" />
                  </svg>
                )}
                Continue with SSO
              </a>
            </Button>

            <p className="mt-4 text-sm text-text-secondary">
              Secure, seamless access powered by your identity provider.
            </p>

            <ProviderChips providers={providers} />
          </div>

          <div className="flex items-center gap-4 border-t border-border px-8 py-5 font-mono text-[11px] text-text-secondary">
            <span className="inline-flex items-center gap-2">
              <svg
                viewBox="0 0 24 24"
                aria-hidden="true"
                className="size-[15px] fill-none stroke-text-secondary"
                strokeWidth={2}
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <rect x="3" y="11" width="18" height="11" rx="2" />
                <path d="M7 11V7a5 5 0 0110 0v4" />
              </svg>
              no password stored here
            </span>
            <span
              role="status"
              aria-label="All systems operational"
              title="All systems operational"
              className="ml-auto inline-flex cursor-default items-center gap-2 text-primary"
            >
              <span
                aria-hidden="true"
                className="size-2 animate-pulse rounded-full bg-primary shadow-[0_0_8px_var(--primary)]"
              />
              <span className="font-semibold tracking-widest">READY</span>
            </span>
          </div>
        </main>
      </div>

      {/* ── Shared footer — the SAME transversal SiteFooter as the authed pages,
          so login and the inner pages stay coherent (full-width bar, © line +
          real-links-only Privacy/Terms). ───────────────────────────────────── */}
      <SiteFooter config={config} />
    </div>
  );
}
