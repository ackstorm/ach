// use-toast.ts — Zustand toast store, ported and generalized from src/ui/toast.js.
//
// The Preact toast.js shipped a minimal single-toast hook `useToast() ->
// { visible, show }`: `show()` flips `visible` true then clears it after a
// fixed TOAST_DURATION_MS (2500ms) via setTimeout, restarting the timer on a
// rapid re-show, and clearing the pending timer on unmount (the clipboard.js
// `useCopyFeedback` pattern, toast.js:41-65). It carried the LOCKED copy
// "Coming soon", rendered bottom-center, role="status" aria-live="polite".
//
// Phase 3 (keys CRUD) and Phase 4 (stats) need richer feedback than a single
// "Coming soon" pill, so this generalizes that same behavior into a QUEUED,
// variant-aware store while preserving every invariant that mattered in
// toast.js:
//   * auto-dismiss after the SAME TOAST_DURATION_MS (2500ms) timeout;
//   * re-pushing does NOT leak timers — each toast owns one timer, cleared on
//     dismiss (the unmount-safe clear of toast.js);
//   * the store performs NO network request and NO state change beyond its own
//     toast queue (the D-13 no-op invariant — this module references no HTTP
//     client).
//
// Public API (consumed by the Toaster renderer in components/ui/toast.tsx):
//   toast({ message, variant?, durationMs? }) -> id   (push; auto-dismisses)
//   dismiss(id)                               -> void (manual immediate remove)
//   dismissAll()                              -> void
// plus the live `toasts` array. Mirrors toast.js's `show()` (now `toast()`)
// and adds the explicit dismiss the queue model requires.

import { create } from 'zustand';

/** Auto-dismiss duration — ported VERBATIM from toast.js (UI-SPEC §10, 2500ms). */
export const TOAST_DURATION_MS = 2500;

/** Locked "Coming soon" copy from toast.js (13-UI-SPEC §Copywriting Contract). */
export const TOAST_COMING_SOON = 'Coming soon';

/** Toast variants. `success` = primary/green, `error` = destructive, `info` = neutral. */
export type ToastVariant = 'success' | 'error' | 'info';

export interface Toast {
  id: string;
  message: string;
  variant: ToastVariant;
}

export interface ToastInput {
  message: string;
  /** Defaults to "info" (the neutral pill, matching toast.js's plain surface). */
  variant?: ToastVariant;
  /** Override the auto-dismiss window; defaults to TOAST_DURATION_MS (2500ms). */
  durationMs?: number;
}

export interface ToastState {
  toasts: Toast[];
  /** Push a toast onto the queue; returns its id. Auto-dismisses after the timeout. */
  toast: (input: ToastInput) => string;
  /** Remove a single toast immediately (manual dismiss / its expiry timer). */
  dismiss: (id: string) => void;
  /** Clear the whole queue (used by tests and on teardown). */
  dismissAll: () => void;
}

/** Per-toast expiry timers, kept OUTSIDE the store so they never serialize. */
const timers = new Map<string, ReturnType<typeof setTimeout>>();

function clearTimer(id: string): void {
  const t = timers.get(id);
  if (t !== undefined) {
    clearTimeout(t);
    timers.delete(id);
  }
}

/** Initial state — an empty queue. Exported so tests can reset the store. */
export const initialToastState = {
  toasts: [] as Toast[],
} as const;

let counter = 0;
function nextId(): string {
  counter += 1;
  return `toast-${counter}-${Date.now()}`;
}

export const useToastStore = create<ToastState>((set, get) => ({
  ...initialToastState,

  toast: ({ message, variant = 'info', durationMs = TOAST_DURATION_MS }) => {
    const id = nextId();
    set((s) => ({ toasts: [...s.toasts, { id, message, variant }] }));

    // Auto-dismiss after the SAME fixed timeout toast.js used. Each toast owns
    // exactly one timer; dismiss() clears it so no timer ever fires on a toast
    // that is already gone (the unmount-safe clear of toast.js).
    const timer = setTimeout(() => {
      timers.delete(id);
      get().dismiss(id);
    }, durationMs);
    timers.set(id, timer);

    return id;
  },

  dismiss: (id) => {
    clearTimer(id);
    set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }));
  },

  dismissAll: () => {
    for (const id of timers.keys()) clearTimer(id);
    set({ toasts: [] });
  },
}));

/**
 * useToast() — the public hook. Returns the live `toasts` queue plus the
 * push/dismiss actions. Naming mirrors toast.js's `useToast`; `toast()`
 * supersedes its `show()` (now variant + message aware), and `dismiss()` is
 * the explicit manual remove the queue model needs.
 */
export function useToast() {
  const toasts = useToastStore((s) => s.toasts);
  const toast = useToastStore((s) => s.toast);
  const dismiss = useToastStore((s) => s.dismiss);
  const dismissAll = useToastStore((s) => s.dismissAll);
  return { toasts, toast, dismiss, dismissAll };
}
