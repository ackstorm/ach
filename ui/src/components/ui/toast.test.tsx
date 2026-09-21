// toast.test.tsx — vitest suite for the useToast store + Toaster renderer.
//
// Store behavior (with vi.useFakeTimers()): pushing adds to the queue; the
// toast auto-dismisses after the SAME TOAST_DURATION_MS toast.js used; manual
// dismiss removes the right toast immediately; variants carry through. The
// store is reset before every test so cases are deterministic.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';

import {
  initialToastState,
  TOAST_DURATION_MS,
  useToastStore,
} from '@/hooks/use-toast';
import { Toaster } from './toast';

beforeEach(() => {
  vi.useFakeTimers();
  // Reset the queue (and any live timers) before each test for determinism.
  const { dismissAll } = useToastStore.getState();
  dismissAll();
  useToastStore.setState({ ...initialToastState }, false);
});

afterEach(() => {
  // Drain pending timers then restore real timers so RTL/jsdom is clean.
  vi.clearAllTimers();
  vi.useRealTimers();
});

describe('useToast store', () => {
  it('pushing a toast adds it to the queue and returns an id', () => {
    const id = useToastStore.getState().toast({ message: 'Saved', variant: 'success' });
    const { toasts } = useToastStore.getState();
    expect(toasts).toHaveLength(1);
    expect(toasts[0]).toMatchObject({ id, message: 'Saved', variant: 'success' });
  });

  it('defaults the variant to "info" when omitted', () => {
    useToastStore.getState().toast({ message: 'Heads up' });
    expect(useToastStore.getState().toasts[0].variant).toBe('info');
  });

  it('auto-dismisses after TOAST_DURATION_MS', () => {
    useToastStore.getState().toast({ message: 'bye soon' });
    expect(useToastStore.getState().toasts).toHaveLength(1);

    // Just before the timeout: still present.
    act(() => void vi.advanceTimersByTime(TOAST_DURATION_MS - 1));
    expect(useToastStore.getState().toasts).toHaveLength(1);

    // At the timeout: gone.
    act(() => void vi.advanceTimersByTime(1));
    expect(useToastStore.getState().toasts).toHaveLength(0);
  });

  it('honors a per-toast durationMs override', () => {
    useToastStore.getState().toast({ message: 'quick', durationMs: 500 });
    act(() => void vi.advanceTimersByTime(499));
    expect(useToastStore.getState().toasts).toHaveLength(1);
    act(() => void vi.advanceTimersByTime(1));
    expect(useToastStore.getState().toasts).toHaveLength(0);
  });

  it('manual dismiss removes the right toast immediately and cancels its timer', () => {
    const a = useToastStore.getState().toast({ message: 'first' });
    const b = useToastStore.getState().toast({ message: 'second' });
    expect(useToastStore.getState().toasts).toHaveLength(2);

    act(() => useToastStore.getState().dismiss(a));
    const remaining = useToastStore.getState().toasts;
    expect(remaining).toHaveLength(1);
    expect(remaining[0].id).toBe(b);

    // The dismissed toast's auto-dismiss timer must not fire later / double-remove.
    act(() => void vi.advanceTimersByTime(TOAST_DURATION_MS));
    expect(useToastStore.getState().toasts).toHaveLength(0);
  });

  it('stacks multiple toasts and dismissAll clears them', () => {
    useToastStore.getState().toast({ message: '1' });
    useToastStore.getState().toast({ message: '2' });
    useToastStore.getState().toast({ message: '3' });
    expect(useToastStore.getState().toasts).toHaveLength(3);

    act(() => useToastStore.getState().dismissAll());
    expect(useToastStore.getState().toasts).toHaveLength(0);
  });

  it('variants carry through to the queue item', () => {
    useToastStore.getState().toast({ message: 'ok', variant: 'success' });
    useToastStore.getState().toast({ message: 'bad', variant: 'error' });
    useToastStore.getState().toast({ message: 'note', variant: 'info' });
    expect(useToastStore.getState().toasts.map((t) => t.variant)).toEqual([
      'success',
      'error',
      'info',
    ]);
  });
});

describe('Toaster renderer', () => {
  it('renders the queued toasts inside an aria-live region', () => {
    render(<Toaster />);
    act(() => {
      useToastStore.getState().toast({ message: 'Key created', variant: 'success' });
    });

    const region = screen.getByRole('region', { name: 'Notifications' });
    expect(region).toHaveAttribute('aria-live', 'polite');
    expect(screen.getByText('Key created')).toBeInTheDocument();
  });

  it('error toasts upgrade to role="alert" + aria-live="assertive"', () => {
    render(<Toaster />);
    act(() => {
      useToastStore.getState().toast({ message: 'It broke', variant: 'error' });
    });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveAttribute('aria-live', 'assertive');
    expect(alert).toHaveTextContent('It broke');
  });

  it('the dismiss button has an accessible name and removes the toast', () => {
    render(<Toaster />);
    act(() => {
      useToastStore.getState().toast({ message: 'dismiss me' });
    });

    const btn = screen.getByRole('button', { name: 'Dismiss notification' });
    act(() => btn.click());
    expect(useToastStore.getState().toasts).toHaveLength(0);
  });
});
