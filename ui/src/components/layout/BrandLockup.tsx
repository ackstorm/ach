// BrandLockup.tsx — the two-tone wordmark, ported from app.js AuthedShell's
// brand-lockup logic.
//
// Splits `config.brand` on `config.accent_segment`: when accent_segment is
// non-empty AND brand ends with it, the wordmark renders base + a green accent
// span (e.g. "alitellm" + green "-auth"). When accent_segment is empty (or brand
// does not end with it), the WHOLE brand renders in the bright color with no
// accent span (parity with app.js, e.g. ACKStorm).
//
// All strings render as React text children only — never dangerouslySetInnerHTML
// (T-10-14 XSS). The shield-check SVG is the same icon used by app.js's topbar
// brand.

import type { AppConfig } from '@/lib/api-types';
import { cn } from '@/lib/utils';

export interface BrandLockupProps {
  config: AppConfig;
  /** Tailwind size/weight classes for the wordmark text (topbar vs landing). */
  className?: string;
  /** Tailwind size classes for the icon. */
  iconClassName?: string;
}

export function BrandLockup({
  config,
  className,
  iconClassName = 'size-[18px]',
}: BrandLockupProps) {
  const accent = config.accent_segment || '';
  const hasAccent = Boolean(accent) && config.brand.endsWith(accent);
  const base = hasAccent
    ? config.brand.slice(0, config.brand.length - accent.length)
    : config.brand;

  return (
    <span className="inline-flex items-center gap-2 font-sans font-semibold text-text-primary">
      <svg
        viewBox="0 0 24 24"
        aria-hidden="true"
        className={cn('stroke-primary fill-none', iconClassName)}
        strokeWidth={2}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M12 2 4 6v6c0 4.5 3.4 7.3 8 10 4.6-2.7 8-5.5 8-10V6z" />
        <path d="m9 12 2 2 4-4" />
      </svg>
      <span className={className}>
        {hasAccent ? (
          <>
            {base}
            <span className="text-accent-bright">{accent}</span>
          </>
        ) : (
          config.brand
        )}
      </span>
    </span>
  );
}
