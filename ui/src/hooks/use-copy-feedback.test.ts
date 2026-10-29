// use-copy-feedback.test.ts — vitest suite for the clipboard copy helper and
// the 2s "copied!" feedback hook (jsdom). Ported from src/ui/clipboard.js.
//
// SECURITY (threat T-10-01, Information Disclosure): copyText writes ONLY the
// caller-supplied string, never throws, never logs. The tests assert it
// resolves false on a denied/unavailable clipboard rather than rejecting.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';

import { copyText, useCopyFeedback } from './use-copy-feedback';

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.restoreAllMocks();
  // Remove any clipboard stub so a later test starts from a known state.
  Reflect.deleteProperty(navigator, 'clipboard');
});

/** Install a navigator.clipboard.writeText stub for the duration of a test. */
function stubClipboard(writeText: (text: string) => Promise<void>): void {
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
    writable: true,
  });
}

describe('copyText', () => {
  it('returns true when navigator.clipboard.writeText resolves', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    stubClipboard(writeText);

    await expect(copyText('hello')).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith('hello');
  });

  it('returns false when navigator.clipboard.writeText rejects (never throws)', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    stubClipboard(writeText);

    await expect(copyText('hello')).resolves.toBe(false);
  });

  it('returns false when the clipboard API is unavailable', async () => {
    // No navigator.clipboard installed (afterEach deletes it).
    await expect(copyText('hello')).resolves.toBe(false);
  });
});

describe('useCopyFeedback', () => {
  it('sets copied=true on a successful copy, then clears after exactly 2000ms', async () => {
    stubClipboard(vi.fn().mockResolvedValue(undefined));
    const { result } = renderHook(() => useCopyFeedback());

    expect(result.current.copied).toBe(false);

    await act(async () => {
      await result.current.copy('x');
    });
    expect(result.current.copied).toBe(true);

    // Just before the timeout: still flagged.
    act(() => void vi.advanceTimersByTime(1999));
    expect(result.current.copied).toBe(true);

    // At the timeout: cleared.
    act(() => void vi.advanceTimersByTime(1));
    expect(result.current.copied).toBe(false);
  });

  it('keeps copied=false on a failed copy', async () => {
    stubClipboard(vi.fn().mockRejectedValue(new Error('denied')));
    const { result } = renderHook(() => useCopyFeedback());

    let returned: boolean | undefined;
    await act(async () => {
      returned = await result.current.copy('x');
    });

    expect(returned).toBe(false);
    expect(result.current.copied).toBe(false);
  });
});
