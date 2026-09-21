// ErrorCard.tsx — the error-state shell card (ported from src/ui/app.js
// ErrorCard). Shown when resolveState returns `error` (network failure status 0,
// or any 4xx/5xx that is not the cold-load 401). All copy is LOCKED per the
// UI-SPEC §Copywriting Contract: ERROR / "Service Unavailable" / "Unable to
// reach alitellm-auth. Try refreshing the page." / "retry".
//
// Carry-forward C1: the parent passes `retrying` (true while a loadSession is in
// flight, i.e. status === null). The retry button is DISABLED while retrying so
// a user cannot stack concurrent reloads.

import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';

export interface ErrorCardProps {
  onRetry: () => void;
  retrying?: boolean;
}

export function ErrorCard({ onRetry, retrying = false }: ErrorCardProps) {
  return (
    <div
      data-state="error"
      role="alert"
      className="flex flex-1 items-center justify-center p-8 min-h-screen"
    >
      <Card className="w-full max-w-[620px] gap-0 py-0">
        <div className="border-b border-border px-8 pt-8 pb-6">
          <div className="mb-4 flex items-center gap-2.5">
            <span
              aria-hidden="true"
              className="size-2 rounded-full bg-destructive shadow-[0_0_8px_var(--destructive)]"
            />
            <span className="font-mono text-xs font-semibold tracking-widest text-destructive">
              ERROR
            </span>
          </div>
          <h1 className="text-2xl font-semibold leading-tight text-text-primary">
            Service Unavailable
          </h1>
          <p className="mt-1.5 text-sm text-text-secondary">
            Unable to reach alitellm-auth. Try refreshing the page.
          </p>
        </div>
        <div className="px-8 py-7">
          <Button
            type="button"
            variant="destructive"
            onClick={onRetry}
            disabled={retrying}
            className="font-mono lowercase tracking-wide"
          >
            retry
          </Button>
        </div>
      </Card>
    </div>
  );
}
