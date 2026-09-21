// ProviderChips.tsx — the "BACKED BY" identity-provider chip row, ported from
// src/ui/login.js TwoColumnLogin's `.backed-by` + `.backed-by-row`.
//
// Renders one rounded chip per `config.providers` entry (an icon tile + the
// provider label). Known providers show their REAL brand glyph (Google, Dex);
// everything else falls back to the generic shield. The chips are STATIC
// illustrative content naming which IdPs back the SSO button — NOT live auth
// state (T-09-18 is about *live* data).
//
// a11y (WR-04): the chips are NOT aria-hidden — they carry non-redundant text and
// must stay in the accessibility tree. Only the decorative icon SVG inside each
// chip is aria-hidden. All labels render as React text children (T-10-14 XSS).
//
// Brand glyphs use their OFFICIAL colors (hardcoded hex is correct here — a brand
// mark must not be re-tinted to theme tokens), shown on a neutral tile so the
// colors read true. The generic shield keeps the green token tile + stroke.

import type { ReactElement } from 'react';

import type { ConfigProvider } from '@/lib/api-types';
import { cn } from '@/lib/utils';

// Google "G" — official 4-color mark (svgrepo 303108).
function GoogleGlyph(): ReactElement {
  return (
    <svg viewBox="-3 0 262 262" aria-hidden="true" className="size-[15px]">
      <path
        fill="#4285F4"
        d="M255.878 133.451c0-10.734-.871-18.567-2.756-26.69H130.55v48.448h71.947c-1.45 12.04-9.283 30.172-26.69 42.356l-.244 1.622 38.755 30.023 2.685.268c24.659-22.774 38.875-56.282 38.875-96.027"
      />
      <path
        fill="#34A853"
        d="M130.55 261.1c35.248 0 64.839-11.605 86.453-31.622l-41.196-31.913c-11.024 7.688-25.82 13.055-45.257 13.055-34.523 0-63.824-22.773-74.269-54.25l-1.531.13-40.298 31.187-.527 1.465C35.393 231.798 79.49 261.1 130.55 261.1"
      />
      <path
        fill="#FBBC05"
        d="M56.281 156.37c-2.756-8.123-4.351-16.827-4.351-25.82 0-8.994 1.595-17.697 4.206-25.82l-.073-1.73L15.26 71.312l-1.335.635C5.077 89.644 0 109.517 0 130.55s5.077 40.905 13.925 58.602l42.356-32.782"
      />
      <path
        fill="#EB4335"
        d="M130.55 50.479c24.514 0 41.05 10.589 50.479 19.438l36.844-35.974C195.245 12.91 165.798 0 130.55 0 79.49 0 35.393 29.301 13.925 71.947l42.211 32.783c10.59-31.477 39.891-54.251 74.414-54.251"
      />
    </svg>
  );
}

// Dex color glyph — official mark (dexidp.io/img/logos/dex-glyph-color.svg).
function DexGlyph(): ReactElement {
  return (
    <svg viewBox="0 0 112 109" aria-hidden="true" className="size-[15px]">
      <path
        fill="#449FD8"
        d="M88.345,51.574c7.588-3.55,12.764-10.49,14.175-18.53C96.396,19.395,84.663,9.054,70.094,4.851c4.923,7.133,7.272,15.583,6.771,24.17C83.311,34.466,87.716,42.55,88.345,51.574z M27.27,38.542c-8.207-1.045-16.333,1.973-21.858,8.054C3.23,61.683,7.869,76.84,18.099,88.158c-0.527-8.64,1.856-17.306,6.831-24.483C22.19,55.048,23.32,45.944,27.27,38.542z M33.01,76.928c-2.997,8.079-1.755,17.193,3.642,24.215c12.155,4.943,26.051,5.146,38.643-0.035c-7.818-2.516-14.886-7.518-19.887-14.731C47.233,86.23,39.124,83.032,33.01,76.928z M63.122,22.202C61.615,14.044,56.069,6.819,47.892,3.47C33.778,5.711,20.745,13.966,12.76,26.631c8.115-2.487,16.74-2.178,24.529,0.639C44.816,22.008,54.043,20.144,63.122,22.202z M85.891,66.457c-3.086,7.399-8.722,13.188-15.678,16.61c6.194,5.604,14.805,7.758,22.852,5.834c9.054-9.587,13.884-22.198,13.9-35.009C101.549,60.198,94.131,64.67,85.891,66.457z"
      />
      <circle fill="#F04D5C" cx="56.035" cy="53.892" r="15.972" />
    </svg>
  );
}

// Generic shield — the fallback for any provider without a known brand glyph.
function ShieldGlyph(): ReactElement {
  return (
    <svg
      viewBox="0 0 24 24"
      aria-hidden="true"
      className="size-[15px] fill-none stroke-primary"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M12 2 4 6v6c0 4.5 3.4 7.3 8 10 4.6-2.7 8-5.5 8-10V6z" />
    </svg>
  );
}

// Resolve a provider label to its glyph + whether it is a (full-color) brand
// mark. Brand marks sit on a neutral tile; the generic shield keeps the green
// token tile.
function resolveGlyph(label: string): { glyph: ReactElement; brand: boolean } {
  switch (label.trim().toLowerCase()) {
    case 'google':
      return { glyph: <GoogleGlyph />, brand: true };
    case 'dex':
      return { glyph: <DexGlyph />, brand: true };
    default:
      return { glyph: <ShieldGlyph />, brand: false };
  }
}

export interface ProviderChipsProps {
  providers: ConfigProvider[];
}

export function ProviderChips({ providers }: ProviderChipsProps) {
  return (
    <div>
      {/* 11px mono caption label — letter-spaced + uppercase (login.js .backed-by). */}
      <div className="mt-6 mb-2 font-mono text-[11px] font-semibold uppercase tracking-[2px] text-text-secondary">
        BACKED BY
      </div>
      {/* WR-04: no aria-hidden — these chips stay in the a11y tree. */}
      <div className="flex flex-wrap items-center gap-2">
        {providers.map((p, i) => {
          const { glyph, brand } = resolveGlyph(p.label);
          return (
            <span
              // Labels can repeat in principle; index keeps the key stable.
              key={`${p.label}-${i}`}
              className="inline-flex items-center gap-2 rounded-full border border-border bg-surface px-3 py-1"
            >
              <span
                className={cn(
                  'inline-flex size-7 shrink-0 items-center justify-center rounded-lg border border-border',
                  // Brand marks: neutral tile so their colors read true. Generic
                  // shield: the green token tile it was designed against.
                  brand ? 'bg-surface-elevated' : 'bg-accent'
                )}
              >
                {glyph}
              </span>
              <span className="font-sans text-[13px] font-semibold text-text-primary">
                {p.label}
              </span>
            </span>
          );
        })}
      </div>
    </div>
  );
}
