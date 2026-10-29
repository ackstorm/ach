// use-copy-feedback.ts — copy-to-clipboard helper + a 2s "copied!" feedback
// hook. Ported from src/ui/clipboard.js (Preact -> React), reused by the
// key-ID copy button and the fresh-key copy button.
//
// SECURITY (threat T-10-01, Information Disclosure):
//   - copyText writes ONLY the caller-supplied string, on an explicit user
//     gesture — there is no auto-copy.
//   - the clipboard value is NEVER logged (no logging calls anywhere here).
//   - copyText never throws — a denied/unavailable clipboard resolves false so
//     the caller can fall back silently.

import { useCallback, useEffect, useRef, useState } from 'react';

/** How long the "copied!" feedback stays on after a successful copy. */
const COPIED_FEEDBACK_MS = 2000;

// copyText(text) -> Promise<boolean>. Writes `text` to the clipboard on an
// explicit user action; resolves true on success, false on any failure
// (clipboard API unavailable, permission denied). Never throws, never logs.
export async function copyText(text: string): Promise<boolean> {
  try {
    if (!navigator?.clipboard?.writeText) return false;
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

// useCopyFeedback() -> { copied, copy }. `copy(text)` awaits copyText then, on
// success, sets `copied = true` and clears it after exactly 2000ms (the
// success.html 2s feedback pattern). On failure `copied` stays false.
//
// The pending timeout id is held in a ref so it can be cleared before re-arming
// (overlapping copies reset the timer cleanly) and on unmount (no setState on an
// unmounted component).
export function useCopyFeedback(): {
  copied: boolean;
  copy: (text: string) => Promise<boolean>;
} {
  const [copied, setCopied] = useState(false);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clearPending = useCallback(() => {
    if (timeoutRef.current !== null) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
  }, []);

  // Clear any pending timer on unmount so we never setState after unmount.
  // (Returning `clearPending` registers it as the effect cleanup — it runs on
  // unmount/re-arm, NOT at mount.)
  useEffect(() => {
    return clearPending;
  }, [clearPending]);

  const copy = useCallback(
    async (text: string): Promise<boolean> => {
      const ok = await copyText(text);
      if (ok) {
        setCopied(true);
        clearPending();
        timeoutRef.current = setTimeout(() => {
          setCopied(false);
          timeoutRef.current = null;
        }, COPIED_FEEDBACK_MS);
      }
      return ok;
    },
    [clearPending]
  );

  return { copied, copy };
}
