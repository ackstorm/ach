// StatusPill.tsx — the ONE status pill (tinted fill, no dot, no border,
// UPPER mono 10px). Styles: `.pill.<tone>` in styles/ledger-ui.css. Labels and
// tones per domain come from lib/status.ts.
import type { HTMLAttributes } from 'react';

import type { Tone } from '@/lib/status';

export function StatusPill({ tone, className = '', ...rest }: { tone: Tone } & HTMLAttributes<HTMLSpanElement>) {
  return <span {...rest} className={`pill ${tone}${className ? ` ${className}` : ''}`} />;
}
