// CreateKeyModal.test.tsx — vitest suite for the create-key modal (jsdom).
//
// useCreateKey is fully mocked so NO real fetch happens; each test programs its
// `mutateAsync` (resolve / reject) + `isPending`. useEnvironments is mocked too
// (each test programs the environment list; default [] so the picker is empty).
// The clipboard is stubbed so the result-view copy can be observed. Both the
// open-state store and the fresh-keys store are reset between tests; the modal
// is rendered with the store already opened.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

// Mock the create-key mutation hook — each test sets mutateAsync + isPending.
vi.mock('@/hooks/use-keys', () => ({
  useCreateKey: vi.fn(),
}));

// Mock the environments hook — each test programs the environment list.
vi.mock('@/hooks/use-environments', () => ({
  useEnvironments: vi.fn(),
}));

import { useCreateKey } from '@/hooks/use-keys';
import { useEnvironments } from '@/hooks/use-environments';
import type { EnvironmentRow } from '@/lib/api-types';
import { CreateKeyModal } from './CreateKeyModal';
import { ALIAS_ERROR, NAME_REQUIRED_ERROR } from '@/lib/key-validation';
import {
  initialCreateKeyModalState,
  useCreateKeyModalStore,
} from '@/stores/create-key-modal';
import {
  initialFreshKeysState,
  useFreshKeysStore,
} from '@/stores/fresh-keys';
import { initialSessionState, useSessionStore } from '@/stores/session';

const useCreateKeyMock = vi.mocked(useCreateKey);
const useEnvironmentsMock = vi.mocked(useEnvironments);

/** Program useCreateKey with a given mutateAsync + pending flag. */
function setMutation(mutateAsync: ReturnType<typeof vi.fn>, isPending = false): void {
  useCreateKeyMock.mockReturnValue({
    mutateAsync,
    isPending,
  } as unknown as ReturnType<typeof useCreateKey>);
}

/** Program useEnvironments with a given environment list (defaults to [] in beforeEach). */
function setEnvironments(environments: EnvironmentRow[]): void {
  useEnvironmentsMock.mockReturnValue({
    data: environments,
  } as unknown as ReturnType<typeof useEnvironments>);
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
  const { loadSession, markExpired } = useSessionStore.getState();
  useSessionStore.setState({ ...initialSessionState, loadSession, markExpired }, true);
  // Default to no environments -> submit is blocked by the required picker.
  setEnvironments([]);
});

afterEach(() => {
  vi.clearAllMocks();
  Reflect.deleteProperty(navigator, 'clipboard');
});

describe('CreateKeyModal — form view', () => {
  it('renders the form: title "Create Key" + all three fields with helper text', () => {
    setMutation(vi.fn());
    render(<CreateKeyModal />);

    expect(screen.getByRole('heading', { name: 'Create Key' })).toBeInTheDocument();
    expect(screen.getByLabelText('environment')).toBeInTheDocument();
    expect(screen.getByLabelText('name')).toBeInTheDocument();
    expect(screen.getByLabelText('expires')).toBeInTheDocument();
    expect(
      screen.getByText(
        'Letters, numbers, dash, underscore, dot. Up to 128 characters.',
      ),
    ).toBeInTheDocument();
  });

  it('disables create when the key allowance is exhausted', () => {
    useSessionStore.setState({
      status: 200,
      hasLoaded: true,
      me: {
        email: 'alice@example.com',
        name: 'Alice Example',
        is_admin: false,
        openwork_enabled: false,
        suspend_propagation_seconds: 60,
        keys_used: 2,
        max_keys: 2,
        endpoint: window.location.origin,
      },
    });
    setMutation(vi.fn());
    render(<CreateKeyModal />);

    expect(screen.getByRole('button', { name: 'Create Key' })).toBeDisabled();
    expect(screen.getByText('Key limit reached (2 of 2 in use). Ask an admin to raise it.')).toBeInTheDocument();
  });

  it('offers the four expiry presets: Never, 7 days, 30 days, 90 days', () => {
    setMutation(vi.fn());
    render(<CreateKeyModal />);

    const select = screen.getByLabelText('expires') as HTMLSelectElement;
    const options = [...select.options].map((o) => o.textContent);
    expect(options).toEqual(['Never', '7 days', '30 days', '90 days']);
    expect(select.value).toBe('never');
  });

  it('only Available environments are selectable; others render disabled', () => {
    setMutation(vi.fn());
    setEnvironments([
      { name: 'prod', status: 'Available' },
      { name: 'staging', status: 'UnresolvedReferences' },
    ]);
    render(<CreateKeyModal />);

    const select = screen.getByLabelText('environment') as HTMLSelectElement;
    const prodOption = [...select.options].find((o) => o.value === 'prod');
    const stagingOption = [...select.options].find((o) => o.value === 'staging');
    expect(prodOption?.disabled).toBe(false);
    expect(stagingOption?.disabled).toBe(true);
  });

  it('defaults the picker to the first Available environment', () => {
    setMutation(vi.fn());
    setEnvironments([
      { name: 'blocked', status: 'UnresolvedReferences' },
      { name: 'prod', status: 'Available' },
    ]);
    render(<CreateKeyModal />);

    const select = screen.getByLabelText('environment') as HTMLSelectElement;
    expect(select.value).toBe('prod');
  });
});

describe('CreateKeyModal — client validation (no request)', () => {
  it('an empty name shows NAME_REQUIRED_ERROR and does NOT call mutateAsync', () => {
    const mutateAsync = vi.fn();
    setMutation(mutateAsync);
    setEnvironments([{ name: 'prod', status: 'Available' }]);
    render(<CreateKeyModal />);

    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(screen.getByText(NAME_REQUIRED_ERROR)).toBeInTheDocument();
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it('an invalid name shows ALIAS_ERROR and does NOT call mutateAsync', () => {
    const mutateAsync = vi.fn();
    setMutation(mutateAsync);
    setEnvironments([{ name: 'prod', status: 'Available' }]);
    render(<CreateKeyModal />);

    fireEvent.change(screen.getByLabelText('name'), {
      target: { value: 'bad name!' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(screen.getByText(ALIAS_ERROR)).toBeInTheDocument();
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it('no environment selected shows a required error and does NOT call mutateAsync', () => {
    const mutateAsync = vi.fn();
    setMutation(mutateAsync);
    // No Available environment -> the picker stays at '' (placeholder).
    setEnvironments([{ name: 'staging', status: 'UnresolvedReferences' }]);
    render(<CreateKeyModal />);

    fireEvent.change(screen.getByLabelText('name'), { target: { value: 'my-key' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(screen.getByText('Select an environment.')).toBeInTheDocument();
    expect(mutateAsync).not.toHaveBeenCalled();
  });
});

describe('CreateKeyModal — valid submit + one-time reveal', () => {
  it('submits {environment,name} (no expires_at for "never") -> result view shows the warning + full plaintext, copy writes it', async () => {
    const writeText = stubClipboard();
    setEnvironments([{ name: 'prod', status: 'Available' }]);
    const mutateAsync = vi
      .fn()
      .mockResolvedValue({ key_id: 'ekid_1', plaintext: 'ek-secret' });
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    fireEvent.change(screen.getByLabelText('name'), { target: { value: 'my-key' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(mutateAsync).toHaveBeenCalledWith({ environment: 'prod', name: 'my-key' });

    // The view switches to the shown-once result.
    expect(await screen.findByText('Key created')).toBeInTheDocument();
    expect(
      screen.getByText(
        (_content, el) =>
          el?.tagName === 'P' &&
          el.textContent ===
            "Save this secret key somewhere safe and accessible. For security reasons, you won't be able to view it again. If you lose it, you'll need to generate a new one.",
      ),
    ).toBeInTheDocument();
    const emphasis = screen.getByText("you won't be able to view it again.");
    expect(emphasis.tagName).toBe('STRONG');
    expect(emphasis).toHaveClass('font-semibold');
    expect(screen.getByText('ek-secret')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'copy' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('ek-secret'));
  });

  it('a non-"never" preset sends an absolute expires_at (RFC3339)', async () => {
    setEnvironments([{ name: 'prod', status: 'Available' }]);
    const mutateAsync = vi
      .fn()
      .mockResolvedValue({ key_id: 'ekid_1', plaintext: 'ek-secret' });
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    fireEvent.change(screen.getByLabelText('name'), { target: { value: 'my-key' } });
    fireEvent.change(screen.getByLabelText('expires'), { target: { value: '30d' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalled());
    const body = mutateAsync.mock.calls[0][0];
    expect(body.environment).toBe('prod');
    expect(body.name).toBe('my-key');
    expect(typeof body.expires_at).toBe('string');
    // A future instant, RFC3339 with a trailing Z.
    expect(body.expires_at).toMatch(/Z$/);
    expect(new Date(body.expires_at).getTime()).toBeGreaterThan(Date.now());
  });
});

describe('CreateKeyModal — server error routing', () => {
  it('a rejection with a detail shows it as the form error (form stays open)', async () => {
    setEnvironments([{ name: 'prod', status: 'Available' }]);
    const mutateAsync = vi
      .fn()
      .mockRejectedValue(
        Object.assign(new Error(), {
          status: 403,
          detail: 'caller is not a member of any authorized team',
        }),
      );
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    fireEvent.change(screen.getByLabelText('name'), { target: { value: 'my-key' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(
      await screen.findByText('caller is not a member of any authorized team'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create Key' })).toBeInTheDocument();
    expect(screen.queryByText('Key created')).not.toBeInTheDocument();
  });

  it('a 502 rejection (detail null) shows CREATE_502_ERROR', async () => {
    setEnvironments([{ name: 'prod', status: 'Available' }]);
    const mutateAsync = vi
      .fn()
      .mockRejectedValue(Object.assign(new Error(), { status: 502, detail: null }));
    setMutation(mutateAsync);
    render(<CreateKeyModal />);

    fireEvent.change(screen.getByLabelText('name'), { target: { value: 'my-key' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));

    expect(
      await screen.findByText("Couldn't create the key. Try again in a moment."),
    ).toBeInTheDocument();
  });
});

describe('CreateKeyModal — reopen clears the shown-once key', () => {
  it('reopening after a successful create shows a fresh form, not the previous key', async () => {
    stubClipboard();
    setEnvironments([{ name: 'prod', status: 'Available' }]);
    const mutateAsync = vi
      .fn()
      .mockResolvedValue({ key_id: 'ekid_1', plaintext: 'ek-PREVIOUS-SECRET' });
    setMutation(mutateAsync);
    // Mount ONCE — the component owns `result` state, so reopen must toggle the
    // store (not remount) to prove handleClose actually cleared the secret.
    render(<CreateKeyModal />);

    fireEvent.change(screen.getByLabelText('name'), { target: { value: 'my-key' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create Key' }));
    expect(await screen.findByText('Key created')).toBeInTheDocument();
    expect(screen.getByText('ek-PREVIOUS-SECRET')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'done' }));
    await waitFor(() =>
      expect(screen.queryByText('Key created')).not.toBeInTheDocument(),
    );

    act(() => {
      useCreateKeyModalStore.getState().openModal();
    });

    expect(
      await screen.findByRole('button', { name: 'Create Key' }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('name')).toHaveValue('');
    expect(screen.queryByText('Key created')).not.toBeInTheDocument();
    expect(screen.queryByText('ek-PREVIOUS-SECRET')).not.toBeInTheDocument();
  });
});
