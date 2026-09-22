// DeleteKeyModal.test.tsx — vitest suite for the delete-key confirm modal (jsdom).
//
// useDeleteKey is fully mocked so NO real fetch happens; each test programs its
// `mutateAsync` (resolve / reject) + `isPending`. The REAL toast store is used
// (reset between tests) so a fired success toast can be asserted via its live
// `toasts` array. onClose is a spy supplied by the parent (this modal is a
// controlled, prop-driven leaf).

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

// Mock the delete-key mutation hook — each test sets mutateAsync + isPending.
vi.mock('@/hooks/use-keys', () => ({
  useDeleteKey: vi.fn(),
}));

import { useDeleteKey } from '@/hooks/use-keys';
import type { KeyRow } from '@/lib/api-types';
import { initialToastState, useToastStore } from '@/hooks/use-toast';
import { DELETE_ERROR, DELETE_SUCCESS, DeleteKeyModal } from './DeleteKeyModal';

const useDeleteKeyMock = vi.mocked(useDeleteKey);

/** Program useDeleteKey with a given mutateAsync + pending flag. */
function setMutation(mutateAsync: ReturnType<typeof vi.fn>, isPending = false): void {
  useDeleteKeyMock.mockReturnValue({
    mutateAsync,
    isPending,
  } as unknown as ReturnType<typeof useDeleteKey>);
}

/** Build a minimal valid KeyRow with overrides. */
function makeKey(overrides: Partial<KeyRow> = {}): KeyRow {
  return {
    key_id: 'ekid_abc',
    type: 'ek',
    owner_email: 'alice@example.com',
    environment: 'prod',
    name: 'ci',
    status: 'active',
    created_at: '2026-03-01T10:00:00Z',
    expires_at: null,
    ...overrides,
  };
}

beforeEach(() => {
  // Reset the real toast store to an empty queue, preserving its actions.
  const { toast, dismiss, dismissAll } = useToastStore.getState();
  useToastStore.setState({ ...initialToastState, toast, dismiss, dismissAll }, true);
});

afterEach(() => {
  vi.clearAllMocks();
  useToastStore.getState().dismissAll();
});

describe('DeleteKeyModal — closed states', () => {
  it('keyToDelete={null} -> no dialog content visible', () => {
    setMutation(vi.fn());
    render(<DeleteKeyModal keyToDelete={null} onClose={vi.fn()} />);

    expect(screen.queryByText('Revoke Key')).not.toBeInTheDocument();
  });

  it('a key with no .key_id -> dialog not open / renders no confirm content (defensive)', () => {
    setMutation(vi.fn());
    render(
      <DeleteKeyModal keyToDelete={{ name: 'x' } as KeyRow} onClose={vi.fn()} />,
    );

    expect(screen.queryByText('Revoke Key')).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Confirm Revoke' }),
    ).not.toBeInTheDocument();
  });
});

describe('DeleteKeyModal — open', () => {
  it('shows "Revoke Key" title and the body with the name + "This cannot be undone."', () => {
    setMutation(vi.fn());
    render(
      <DeleteKeyModal
        keyToDelete={makeKey({ key_id: 'ekid_abc', name: 'production-key' })}
        onClose={vi.fn()}
      />,
    );

    expect(screen.getByText('Revoke Key')).toBeInTheDocument();
    const body = screen.getByText(/This will permanently revoke/);
    // The human name is named — NOT the raw key_id (which would overflow the box).
    expect(body).toHaveTextContent('production-key');
    expect(body).not.toHaveTextContent('ekid_abc');
    expect(body).toHaveTextContent('This cannot be undone.');
  });

  it('falls back to the MASKED key_id (prefix…last4) when the key has no name', () => {
    setMutation(vi.fn());
    render(
      <DeleteKeyModal
        keyToDelete={makeKey({ key_id: 'ekid_0123456789abcdef', name: undefined })}
        onClose={vi.fn()}
      />,
    );

    const body = screen.getByText(/This will permanently revoke/);
    // maskKey('ekid_0123456789abcdef') -> 'ekid…cdef' — bounded, never the full id.
    expect(body).toHaveTextContent('ekid…cdef');
    expect(body).not.toHaveTextContent('ekid_0123456789abcdef');
  });
});

describe('DeleteKeyModal — confirm success', () => {
  it('mutateAsync resolves -> called with key_id, success toast fired, onClose called', async () => {
    const mutateAsync = vi.fn().mockResolvedValue(null);
    setMutation(mutateAsync);
    const onClose = vi.fn();
    render(
      <DeleteKeyModal keyToDelete={makeKey({ key_id: 'ekid_abc' })} onClose={onClose} />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm Revoke' }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledWith('ekid_abc'));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));

    const { toasts } = useToastStore.getState();
    expect(toasts).toHaveLength(1);
    expect(toasts[0].message).toBe(DELETE_SUCCESS);
    expect(toasts[0].variant).toBe('success');
  });
});

describe('DeleteKeyModal — confirm error', () => {
  it('mutateAsync rejects with no detail -> DELETE_ERROR shown, onClose NOT called, no success toast', async () => {
    const mutateAsync = vi
      .fn()
      .mockRejectedValue(Object.assign(new Error(), { status: 403, detail: null }));
    setMutation(mutateAsync);
    const onClose = vi.fn();
    render(
      <DeleteKeyModal keyToDelete={makeKey({ key_id: 'ekid_abc' })} onClose={onClose} />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm Revoke' }));

    expect(await screen.findByText(DELETE_ERROR)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    expect(useToastStore.getState().toasts).toHaveLength(0);
  });

  it('a rejection carrying a detail shows the server detail, not the generic error', async () => {
    const detail = 'an expired key cannot be suspended';
    const mutateAsync = vi
      .fn()
      .mockRejectedValue(Object.assign(new Error(), { status: 409, detail }));
    setMutation(mutateAsync);
    const onClose = vi.fn();
    render(
      <DeleteKeyModal keyToDelete={makeKey({ key_id: 'ekid_abc' })} onClose={onClose} />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm Revoke' }));

    expect(await screen.findByText(detail)).toBeInTheDocument();
    expect(screen.queryByText(DELETE_ERROR)).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe('DeleteKeyModal — keep key', () => {
  it('"Keep Key" click -> onClose called', async () => {
    setMutation(vi.fn());
    const onClose = vi.fn();
    render(
      <DeleteKeyModal keyToDelete={makeKey({ key_id: 'ekid_abc' })} onClose={onClose} />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Keep Key' }));

    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });
});
