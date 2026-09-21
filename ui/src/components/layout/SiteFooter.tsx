// SiteFooter.tsx — the shared site footer, ported from src/ui/footer.js
// (Preact + htm -> React + Tailwind tokens).
//
// Renders the © line ALWAYS, plus optional Privacy Policy / Terms of Service
// anchors on the right. Each anchor is rendered ONLY when its target is set in
// config.links (real-links-only D-03 — a missing/empty target DROPS the anchor,
// never a dead/404 link). When neither link exists the © line renders alone.
// `href` values come ONLY from config.links (server-controlled allow-list),
// never user input (threat T-09-17). All copy renders as React text children
// (auto-escaped — never raw HTML, threat T-10-14).

import type { ReactNode } from 'react';

import type { AppConfig } from '@/lib/api-types';

export interface SiteFooterProps {
  config: AppConfig;
  /**
   * Optional node rendered on the footer's right side (e.g. the authed shell's
   * service-status indicator). Sits after any Privacy/Terms links. Omitted on
   * the unauthenticated login screen.
   */
  rightSlot?: ReactNode;
}

export function SiteFooter({ config, rightSlot }: SiteFooterProps) {
  const brand = config.brand || 'alitellm-auth';
  const links = config.links ?? {};
  const year = new Date().getFullYear();
  const privacy = links.privacy || null;
  const terms = links.terms || null;

  // A FULL-WIDTH bar that mirrors the topbar EXACTLY: one element, border-t +
  // bg-surface spanning edge-to-edge, with the SAME px-6 horizontal padding as
  // the header. The © line therefore sits at the same left margin as the
  // top-left brand lockup, and the links sit at the same right margin as the
  // topbar nav — NOT constrained to the centered content column.
  return (
    <footer className="flex flex-wrap items-center justify-between gap-2 border-t border-border bg-surface px-6 py-4 font-sans text-sm text-text-secondary">
      <span className="text-text-secondary">
        © {year} {brand}. All rights reserved.
      </span>
      {privacy || terms || rightSlot ? (
        <span className="ml-auto inline-flex items-center gap-4">
          {privacy ? (
            <a
              href={privacy}
              target="_blank"
              rel="noopener noreferrer"
              className="text-text-secondary transition-colors hover:text-text-primary"
            >
              Privacy Policy
            </a>
          ) : null}
          {terms ? (
            <a
              href={terms}
              target="_blank"
              rel="noopener noreferrer"
              className="text-text-secondary transition-colors hover:text-text-primary"
            >
              Terms of Service
            </a>
          ) : null}
          {rightSlot}
        </span>
      ) : null}
    </footer>
  );
}
