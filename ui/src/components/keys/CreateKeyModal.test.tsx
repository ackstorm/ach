// CreateKeyModal.test.tsx — vitest suite for the create-key modal (jsdom).
//
// useCreateKey is fully mocked so NO real fetch happens; each test programs its
// `mutateAsync` (resolve / reject) + `isPending`. The clipboard is stubbed so
// the result-view copy can be observed without a real platform clipboard. Both
// the open-state store and the fresh-keys store are reset between tests; the
// modal is rendered with the store already opened.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

// Mock the create-key mutation hook — each test sets mutateAsync + isPending.
vi.mock('@/hooks/use-keys', () => ({
  useCreateKey: vi.fn(),
}));

// Mock the teams hook — each test programs the team list (default [] so the
// picker is hidden, mirroring the single-team fallback).
vi.mock('@/hooks/use-teams', () => ({
  useTeams: vi.fn(),
}));

import { useCreateKey } from '@/hooks/use-keys';
import { useTeams } from '@/hooks/use-teams';
import type { Team } from '@/lib/api-types';
import { CreateKeyModal } from './CreateKeyModal';
import { ALIAS_ERROR, DURATION_ERROR } from '@/lib/key-validation';
import {
  initialCreateKeyModalState,
  useCreateKeyModalStore,
} from '@/stores/create-key-modal';
import {
  initialFreshKeysState,
  useFreshKeysStore,
} from '@/stores/fresh-keys';

const useCreateKeyMock = vi.mocked(useCreateKey);
const useTeamsMock = vi.mocked(useTeams);

/** Program useCreateKey with a given mutateAsync + pending flag. */
function setMutation(mutateAsync: ReturnType<typeof vi.fn>, isPending = false): void {
  useCreateKeyMock.mockReturnValue({
    mutateAsync,
    isPending,
  } as unknown as ReturnType<typeof useCreateKey>);
}

/** Program useTeams with a given team list (defaults to [] in beforeEach). */
function setTeams(teams: Team[]): void {
  useTeamsMock.mockReturnValue({
    data: teams,
  } as unknown as ReturnType<typeof useTeams>);
}

/** Install a navigator.clipboard.writeText stub, returning the spy. */
function stubClipboard(): ReturnType<typeof vi.fn> {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
    writable: true,
  });
  return writeText;
}

beforeEach(() => {
  // Reset both stores, then open the modal so render shows the form.
  const { openModal, closeModal } = useCreateKeyModalStore.getState();
  useCreateKeyModalStore.setState(
    { ...initialCreateKeyModalState, openModal, closeModal },
    true,
  );
  const { setFresh, dropFresh } = useFreshKeysStore.getState();
  useFreshKeysStore.setState({ ...initialFreshKeysState, setFresh, dropFresh }, true);
  useCreateKeyModalStore.getState().openModal();
  // Default to no teams -> picker hidden, single-team fallback (tests opt in).
  setTeams([]);
});

afterEach(() => {
  vi.clearAllMocks();
  Reflect.deleteProperty(navigator, 'clipboard');
});

describe('CreateKeyModal — form view', () => {
  it('renders the form: title "Create Key" + both fields with helper text', () => {
    setMutation(vi.fn());
    render(<CreateKeyModal />);

    expect(screen.getByRole('heading', { name: 'Create Key' })).toBeInTheDocument();
    expect(screen.getByLabelText('name')).toBeInTheDocument();
    expect(screen.getByLabelText('expires')).toBeInTheDocument();
    expect(
      screen.getByText(
        'Letters, numbers, dash, underscore, dot. Up to 128 characters.',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText('Leave blank for no expiry. Format: 90d, 24h, 30m.'),
    ).toBeInTheDocument();
  });
});

describe('CreateKeyModal — client validation (no request)', () => {
  it('an invalid alias shows ALIAS_ERROR and does NOT call mutateAsync', () => {
    const mutateAsync = vi.fn();
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    fireEvent.change(screen.getByLabelText('name'), {
      target: { value: 'bad name!' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(screen.getByText(ALIAS_ERROR)).toBeInTheDocument();
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it('an invalid duration shows DURATION_ERROR and does NOT call mutateAsync', () => {
    const mutateAsync = vi.fn();
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    fireEvent.change(screen.getByLabelText('expires'), {
      target: { value: '90x' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(screen.getByText(DURATION_ERROR)).toBeInTheDocument();
    expect(mutateAsync).not.toHaveBeenCalled();
  });
});

describe('CreateKeyModal — valid submit + one-time reveal', () => {
  it('empty fields -> mutateAsync({}) -> result view shows the warning + full sk-, copy writes it', async () => {
    const writeText = stubClipboard();
    const mutateAsync = vi.fn().mockResolvedValue({ id: 'key-1', key: 'sk-secret' });
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    // The body omits empty fields -> {} is sent.
    expect(mutateAsync).toHaveBeenCalledWith({});

    // The view switches to the shown-once result.
    expect(await screen.findByText('Key created')).toBeInTheDocument();
    // The full shown-once copy assembles on the <p> (across the bold clause).
    expect(
      screen.getByText(
        (_content, el) =>
          el?.tagName === 'P' &&
          el.textContent ===
            "Save this secret key somewhere safe and accessible. For security reasons, you won't be able to view it again. If you lose it, you'll need to generate a new one.",
      ),
    ).toBeInTheDocument();
    // The key-visibility clause is emphasized (bold inline).
    const emphasis = screen.getByText("you won't be able to view it again.");
    expect(emphasis.tagName).toBe('STRONG');
    expect(emphasis).toHaveClass('font-semibold');
    expect(screen.getByText('sk-secret')).toBeInTheDocument();

    // Copy writes the FULL sk- to the clipboard stub.
    fireEvent.click(screen.getByRole('button', { name: 'copy' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('sk-secret'));
  });
});

describe('CreateKeyModal — server error routing', () => {
  it('a 422 rejection routes the detail to the alias field (form stays open)', async () => {
    const mutateAsync = vi
      .fn()
      .mockRejectedValue(
        Object.assign(new Error(), {
          status: 422,
          detail: 'alias must be 1-128 characters',
        }),
      );
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(
      await screen.findByText('alias must be 1-128 characters'),
    ).toBeInTheDocument();
    // Form stays open (still shows the submit button, not the result view).
    expect(screen.getByRole('button', { name: 'Create Key' })).toBeInTheDocument();
    expect(screen.queryByText('Key created')).not.toBeInTheDocument();
  });

  it('a 502 rejection (detail null) shows CREATE_502_ERROR', async () => {
    const mutateAsync = vi
      .fn()
      .mockRejectedValue(Object.assign(new Error(), { status: 502, detail: null }));
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(
      await screen.findByText("Couldn't create the key. Try again in a moment."),
    ).toBeInTheDocument();
  });
});

describe('CreateKeyModal — reopen clears the shown-once key', () => {
  it('reopening after a successful create shows a fresh form, not the previous key', async () => {
    stubClipboard();
    const mutateAsync = vi
      .fn()
      .mockResolvedValue({ id: 'key-1', key: 'sk-PREVIOUS-SECRET' });
    setMutation(mutateAsync);
    // Mount ONCE — the component owns `result` state, so reopen must toggle the
    // store (not remount) to prove handleClose actually cleared the secret.
    render(<CreateKeyModal />);

    // Submit the empty (valid) form and reach the shown-once result view.
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));
    expect(await screen.findByText('Key created')).toBeInTheDocument();
    expect(screen.getByText('sk-PREVIOUS-SECRET')).toBeInTheDocument();

    // Close via the result view's `done` button (calls handleClose -> resets
    // `result` + closeModal()).
    fireEvent.click(screen.getByRole('button', { name: 'done' }));
    await waitFor(() =>
      expect(screen.queryByText('Key created')).not.toBeInTheDocument(),
    );

    // Reopen via the store, the same mechanism the CTA/shortcut use.
    act(() => {
      useCreateKeyModalStore.getState().openModal();
    });

    // The FORM view is back — and the previous sk- is gone from the document.
    expect(
      await screen.findByRole('button', { name: 'Create Key' }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('name')).toBeInTheDocument();
    expect(screen.queryByText('Key created')).not.toBeInTheDocument();
    expect(screen.queryByText('sk-PREVIOUS-SECRET')).not.toBeInTheDocument();
  });
});

describe('CreateKeyModal — team picker', () => {
  it('submits the selected team_id', async () => {
    setTeams([
      { id: 'default', alias: 'Default' },
      { id: 'run', alias: 'Run Squad' },
    ]);
    const mutateAsync = vi.fn().mockResolvedValue({ id: 'key-1', key: 'sk-secret' });
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    // The picker defaulted to the first team; override it to 'run'.
    fireEvent.change(screen.getByLabelText('team'), { target: { value: 'run' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalled());
    expect(mutateAsync).toHaveBeenCalledWith({ team_id: 'run' });
  });

  it('omits team_id and hides the picker when no teams load', async () => {
    setTeams([]);
    const mutateAsync = vi.fn().mockResolvedValue({ id: 'key-1', key: 'sk-secret' });
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    // No teams -> the picker is not rendered.
    expect(screen.queryByLabelText('team')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalled());
    // Single-team fallback: body carries no team_id.
    expect(mutateAsync).toHaveBeenCalledWith({});
  });
});
