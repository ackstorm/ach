// CreateKeyModal.tsx — the single create-key modal (DASH-04), rewired onto
// ACH's POST /platform/keys. Driven by the create-key-modal open-state store
// (no prop-drilling): the keys table CTA and the sidebar shortcut both call
// openModal().
//
// Two views toggled by local `result` state:
//   FORM   — three fields: Environment (required, Available-only selectable),
//            Name (required, client validation mirrored from
//            envkeys/handler.go), Expiry preset (never/7d/30d/90d -> an
//            absolute expires_at, computed client-side — no free-text
//            duration input any more).
//   RESULT — after a 200, the full plaintext ek- is shown ONCE with the
//            locked shown-once warning + a copy button, then a `done` button
//            closes.
//
// SECURITY (threat T-10-09, Information Disclosure):
//   - The full plaintext lives ONLY in local `result` state + the in-memory
//     fresh-keys store (written by useCreateKey.onSuccess, NOT here).
//   - It is NEVER written to any web Storage, NEVER logged (no console.*),
//     NEVER placed in a title / aria-label / thrown error. It renders only as
//     element text in the result view (React escapes it — no innerHTML sink).

import { Check, Copy } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { useCopyFeedback } from '@/hooks/use-copy-feedback';
import { useCreateKey } from '@/hooks/use-keys';
import { useEnvironments } from '@/hooks/use-environments';
import type { CreateKeyBody } from '@/lib/api-types';
import { degradedReason, isKeyable } from '@/lib/env-status';
import {
  CREATE_502_ERROR,
  EXPIRY_PRESETS,
  SHOWN_ONCE_WARNING_EMPHASIS,
  SHOWN_ONCE_WARNING_POST,
  SHOWN_ONCE_WARNING_PRE,
  presetToExpiresAt,
  validateName,
  type ExpiryPreset,
} from '@/lib/key-validation';
import { cn } from '@/lib/utils';
import { useCreateKeyModalStore } from '@/stores/create-key-modal';
import { useSessionStore } from '@/stores/session';

const ENVIRONMENT_REQUIRED_ERROR = 'Select an environment.';

/** The shown-once create response held in local state (key_id + full plaintext). */
interface CreateResult {
  keyId: string;
  plaintext: string;
}

export function CreateKeyModal() {
  const open = useCreateKeyModalStore((s) => s.open);
  const closeModal = useCreateKeyModalStore((s) => s.closeModal);
  const me = useSessionStore((s) => s.me);

  const [name, setName] = useState('');
  const [environment, setEnvironment] = useState('');
  const [expiry, setExpiry] = useState<ExpiryPreset>('never');
  const [nameError, setNameError] = useState<string | null>(null);
  const [environmentError, setEnvironmentError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  // `result` switches the modal from the form view to the shown-once key view
  // after a 200. Kept in component state only — never persisted (T-10-09).
  const [result, setResult] = useState<CreateResult | null>(null);

  const createKey = useCreateKey();
  const submitting = createKey.isPending;
  const keyLimitReached =
    me?.keys_used != null && me.max_keys != null && me.keys_used >= me.max_keys;
  const { copied, copy } = useCopyFeedback();

  // useEnvironments never throws — it resolves to [] when the list can't be
  // loaded, which leaves the picker empty (submit then blocked by validation).
  const { data: environments = [] } = useEnvironments();

  // Non-blocking hint for the currently selected environment when it's
  // keyable but not fully `Available` (some other condition still pending).
  const selectedDegradedReason = (() => {
    const selected = environments.find((e) => e.name === environment);
    return selected ? degradedReason(selected) : undefined;
  })();

  // Default the picker to the first KEYABLE environment once they load, but
  // only if the user hasn't picked yet (environment still ''). A user choice
  // or reset takes precedence. Keyable is the real backend gate
  // (AccessGroupSynced), not the collapsed `Available` status — see
  // lib/env-status.ts.
  useEffect(() => {
    if (environment === '') {
      const firstKeyable = environments.find(isKeyable);
      if (firstKeyable) setEnvironment(firstKeyable.name);
    }
  }, [environment, environments]);

  // Reset every transient field then bubble the close up via the store. Wired to
  // Cancel / done AND to onOpenChange(false) (Esc / overlay click).
  const handleClose = useCallback(() => {
    setName('');
    setEnvironment('');
    setExpiry('never');
    setNameError(null);
    setEnvironmentError(null);
    setFormError(null);
    setResult(null);
    closeModal();
  }, [closeModal]);

  const onSubmit = useCallback(
    async (e: React.FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      if (submitting || keyLimitReached) return;
      setFormError(null);

      const nameValue = name.trim();

      // Mirror envkeys/handler.go's required-field check BEFORE the request so
      // the locked field messages surface without a round-trip.
      const nErr = validateName(nameValue);
      const eErr = environment === '' ? ENVIRONMENT_REQUIRED_ERROR : null;
      setNameError(nErr);
      setEnvironmentError(eErr);
      if (nErr || eErr) return;

      const body: CreateKeyBody = { environment, name: nameValue };
      const expiresAt = presetToExpiresAt(expiry);
      if (expiresAt) body.expires_at = expiresAt;

      try {
        const data = await createKey.mutateAsync(body);
        // Create always returns 200 with a body (never the 204 shape the
        // shared mutation factory also allows for suspend/resume/delete);
        // the null branch is unreachable defense, not an expected path.
        if (!data) {
          setFormError(CREATE_502_ERROR);
          return;
        }
        // Show the full plaintext ONCE; the fresh-key stash + list invalidation
        // are ALREADY handled by useCreateKey.onSuccess — do NOT duplicate here.
        setResult({ keyId: data.key_id, plaintext: data.plaintext });
      } catch (e) {
        const err = e as { status?: number; detail?: string | null };
        setFormError(err.detail ?? CREATE_502_ERROR);
      }
    },
    [name, environment, expiry, submitting, keyLimitReached, createKey],
  );

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) handleClose();
      }}
    >
      <DialogContent className="max-w-md">
        {result ? (
          // ── Shown-once result view ──────────────────────────────────────
          <>
            <DialogHeader>
              <DialogTitle>Key created</DialogTitle>
            </DialogHeader>
            <div className="flex flex-col gap-4">
              {/* One-time notice — plain professional prose (no tinted callout,
                  mirroring LiteLLM's own dialog), with the key-visibility clause
                  bold inline. The three parts are the locked shown-once copy. */}
              <p
                role="alert"
                className="text-sm leading-relaxed text-text-secondary"
              >
                {SHOWN_ONCE_WARNING_PRE}
                <strong className="font-semibold text-text-primary">
                  {SHOWN_ONCE_WARNING_EMPHASIS}
                </strong>
                {SHOWN_ONCE_WARNING_POST}
              </p>

              {/* The secret key — the focal point. Max-contrast mono (ink in
                  light, near-white in dark via text-text-primary) on a muted slab;
                  copy sits in the label row, right beside the value it acts on. */}
              <div className="flex flex-col gap-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-secondary">
                    Secret key
                  </span>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className={cn(
                      'h-7 gap-1.5 px-2.5',
                      // Same feedback as the EndpointChip copy: filled green while
                      // "copied!" is showing, reverting after the 2s timeout.
                      copied &&
                        'border-primary bg-primary/15 text-primary hover:bg-primary/15 hover:text-primary'
                    )}
                    onClick={() => void copy(result.plaintext)}
                  >
                    {copied ? (
                      <Check aria-hidden="true" className="size-3.5" />
                    ) : (
                      <Copy aria-hidden="true" className="size-3.5" />
                    )}
                    {copied ? 'copied!' : 'copy'}
                  </Button>
                </div>
                <div className="break-all rounded-md border border-border bg-muted px-3.5 py-3 font-mono text-sm leading-relaxed text-text-primary select-all">
                  {result.plaintext}
                </div>
              </div>
            </div>
            <div className="flex justify-end">
              <Button type="button" onClick={handleClose}>
                done
              </Button>
            </div>
          </>
        ) : (
          // ── Form view ───────────────────────────────────────────────────
          <form onSubmit={onSubmit} className="contents">
            <DialogHeader>
              <DialogTitle>Create Key</DialogTitle>
              <DialogDescription>
                Generate a new virtual key for an Environment.
              </DialogDescription>
            </DialogHeader>
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <label
                  htmlFor="ck-environment"
                  className="font-mono text-xs font-semibold uppercase tracking-wide text-text-secondary"
                >
                  environment
                </label>
                {/* Native <select> styled to match Input (no shadcn Select in
                    this project). Only KEYABLE environments are selectable —
                    others render disabled so the user sees why. Keyable is the
                    real backend gate (AccessGroupSynced), not the collapsed
                    `status` — an Environment can be fully keyable while some
                    OTHER sub-condition leaves `status !== 'Available'`. */}
                <select
                  id="ck-environment"
                  value={environment}
                  onChange={(e) => setEnvironment(e.target.value)}
                  disabled={submitting}
                  aria-invalid={environmentError ? true : undefined}
                  className="border-input dark:bg-input/30 h-9 w-full min-w-0 rounded-md border bg-transparent px-3 py-1 text-base shadow-xs transition-[color,box-shadow] outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-sm"
                >
                  <option value="" disabled>
                    Select an environment
                  </option>
                  {environments.map((env) => {
                    const keyable = isKeyable(env);
                    return (
                      <option key={env.name} value={env.name} disabled={!keyable}>
                        {env.name}
                        {!keyable ? ` (${env.status || 'not ready'})` : ''}
                      </option>
                    );
                  })}
                </select>
                {environmentError ? (
                  <p className="text-xs text-destructive">{environmentError}</p>
                ) : selectedDegradedReason ? (
                  <p className="text-xs text-text-secondary">
                    degraded: {selectedDegradedReason}
                  </p>
                ) : null}
              </div>

              <div className="flex flex-col gap-1.5">
                <label
                  htmlFor="ck-name"
                  className="font-mono text-xs font-semibold uppercase tracking-wide text-text-secondary"
                >
                  name
                </label>
                <Input
                  id="ck-name"
                  type="text"
                  placeholder="key-YYYY-MM-DD"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  disabled={submitting}
                  aria-invalid={nameError ? true : undefined}
                />
                <p className="text-xs text-text-secondary">
                  Letters, numbers, dash, underscore, dot. Up to 128 characters.
                </p>
                {nameError ? (
                  <p className="text-xs text-destructive">{nameError}</p>
                ) : null}
              </div>

              <div className="flex flex-col gap-1.5">
                <label
                  htmlFor="ck-expires"
                  className="font-mono text-xs font-semibold uppercase tracking-wide text-text-secondary"
                >
                  expires
                </label>
                {/* Native <select> — a fixed preset, not free text (ACH takes an
                    absolute expires_at, never a LiteLLM duration string). */}
                <select
                  id="ck-expires"
                  value={expiry}
                  onChange={(e) => setExpiry(e.target.value as ExpiryPreset)}
                  disabled={submitting}
                  className="border-input dark:bg-input/30 h-9 w-full min-w-0 rounded-md border bg-transparent px-3 py-1 text-base shadow-xs transition-[color,box-shadow] outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-sm"
                >
                  {EXPIRY_PRESETS.map((p) => (
                    <option key={p.value} value={p.value}>
                      {p.label}
                    </option>
                  ))}
                </select>
              </div>

              {formError ? (
                <p className="text-xs text-destructive">{formError}</p>
              ) : null}
              {keyLimitReached ? (
                <p className="text-xs text-destructive">
                  Key limit reached ({me?.keys_used} of {me?.max_keys} in use). Ask an admin to raise it.
                </p>
              ) : null}
            </div>
            <div className="flex justify-end gap-3">
              <Button
                type="button"
                variant="outline"
                onClick={handleClose}
                disabled={submitting}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={submitting || keyLimitReached}>
                Create Key
              </Button>
            </div>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
