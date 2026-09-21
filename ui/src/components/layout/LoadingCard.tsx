// LoadingCard.tsx — the loading-state shell card (ported from src/ui/app.js
// LoadingCard). Rendered for BOTH the `loading` and `expired` views (expired
// also fires a silent redirect effect in App.tsx, so the card is only a brief
// placeholder). All copy is LOCKED per the UI-SPEC §Copywriting Contract:
// INITIALIZING / "Connecting to alitellm-auth..." / "Verifying your session,
// please wait." / "checking session".
//
// The locked copy strings are hardcoded per the UI-SPEC (parity with app.js,
// where LoadingCard takes no props and hardcodes the literals). M3: the dead
// `config` prop was removed — it was never read.

import { Card } from '@/components/ui/card';

export function LoadingCard() {
  return (
    <div
      data-state="loading"
      role="status"
      aria-live="polite"
      className="flex flex-1 items-center justify-center p-8 min-h-screen"
    >
      <Card className="w-full max-w-[620px] gap-0 py-0">
        <div className="border-b border-border px-8 pt-8 pb-6">
          <div className="mb-4 flex items-center gap-2.5">
            <span
              aria-hidden="true"
              className="size-2 animate-pulse rounded-full bg-primary shadow-[0_0_8px_var(--primary)]"
            />
            <span className="font-mono text-xs font-semibold tracking-widest text-primary">
              INITIALIZING
            </span>
          </div>
          <h1 className="text-2xl font-semibold leading-tight text-text-primary">
            Connecting to alitellm-auth...
          </h1>
          <p className="mt-1.5 text-sm text-text-secondary">
            Verifying your session, please wait.
          </p>
        </div>
        <div className="px-8 py-7">
          <span className="font-mono text-xs tracking-wider text-text-secondary">
            checking session...
          </span>
        </div>
      </Card>
    </div>
  );
}
