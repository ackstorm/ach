// Dashboard.test.tsx — vitest suite for the Keys page (jsdom).
//
// The keys data hook (useKeys) is fully mocked so NO real fetch happens; each
// test programs its return to drive the empty/populated branches. The
// suspend/resume + delete mutations (used by KeysTable / DeleteKeyModal the
// page renders) are mocked too. The create-key-modal + fresh-keys stores are
// reset between tests; the REAL toast store is reset so the delete success
// toast stays isolated.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult, UseMutationResult } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import type { KeyRow, SessionMe } from '@/lib/api-types';

// Mock the keys hooks module — useKeys drives the dashboard metrics/table,
// useDeleteKey backs the DeleteKeyModal, useSuspendKey/useResumeKey back the
// KeysTable kebab.
vi.mock('@/hooks/use-keys', () => ({
  useKeys: vi.fn(),
  useDeleteKey: vi.fn(),
  useSuspendKey: vi.fn(),
  useResumeKey: vi.fn(),
  KEYS_QUERY_KEY: vi.fn(() => ['keys', null]),
}));

import {
  useDeleteKey,
  useKeys,
  useResumeKey,
  useSuspendKey,
} from '@/hooks/use-keys';
import { Dashboard } from './Dashboard';
import {
  initialCreateKeyModalState,
  useCreateKeyModalStore,
} from '@/stores/create-key-modal';
import {
  initialFreshKeysState,
  useFreshKeysStore,
} from '@/stores/fresh-keys';
import { initialSessionState, useSessionStore } from '@/stores/session';
import { initialToastState, useToastStore } from '@/hooks/use-toast';

const useKeysMock = vi.mocked(useKeys);
const useDeleteKeyMock = vi.mocked(useDeleteKey);
const useSuspendKeyMock = vi.mocked(useSuspendKey);
const useResumeKeyMock = vi.mocked(useResumeKey);

// Build a valid SessionMe fixture with overrides.
function makeMe(overrides: Partial<SessionMe> = {}): SessionMe {
  return {
    email: 'alice@example.com',
    name: 'Alice Example',
    is_admin: false,
    enterprise: false,
    openwork_enabled: false,
    suspend_propagation_seconds: 60,
    keys_used: null,
    max_keys: null,
    endpoint: 'https://litellm.example.com',
    chat_url: '',
    provider_name: 'ai-platform',
    ...overrides,
  };
}

// A minimal /platform/keys row factory (mirrors render.KeyListRow).
function makeRow(overrides: Partial<KeyRow> = {}): KeyRow {
  return {
    key_id: 'ekid_abc123',
    type: 'ek',
    owner_email: 'alice@example.com',
    environment: 'prod',
    name: 'my-key',
    status: 'active',
    created_at: '2026-03-01T10:00:00Z',
    expires_at: null,
    ...overrides,
  };
}

/** Program useKeys to a populated success state with `rows`. */
function setKeysSuccess(rows: KeyRow[]): void {
  useKeysMock.mockReturnValue({
    data: rows,
    isPending: false,
    isError: false,
    isSuccess: true,
  } as unknown as UseQueryResult<KeyRow[]>);
}

beforeEach(() => {
  // Reset the create-key-modal store to closed, preserving its actions.
  const { openModal, closeModal } = useCreateKeyModalStore.getState();
  useCreateKeyModalStore.setState(
    { ...initialCreateKeyModalState, openModal, closeModal },
    true,
  );
  // Reset the fresh-keys store to empty.
  const { setFresh, dropFresh } = useFreshKeysStore.getState();
  useFreshKeysStore.setState({ ...initialFreshKeysState, setFresh, dropFresh }, true);
  // Reset the real toast store.
  const { toast, dismiss, dismissAll } = useToastStore.getState();
  useToastStore.setState({ ...initialToastState, toast, dismiss, dismissAll }, true);
  // Seed the session store (KeysTable reads suspend_propagation_seconds).
  const { loadSession, markExpired } = useSessionStore.getState();
  useSessionStore.setState(
    { ...initialSessionState, me: makeMe(), loadSession, markExpired },
    true,
  );
  // Default the delete mutation to a no-op resolved mutation.
  useDeleteKeyMock.mockReturnValue({
    mutateAsync: vi.fn().mockResolvedValue(null),
    isPending: false,
  } as unknown as ReturnType<typeof useDeleteKey>);
  // Default suspend/resume to no-op mutations.
  useSuspendKeyMock.mockReturnValue({
    mutate: vi.fn(),
  } as unknown as UseMutationResult<null, Error, string>);
  useResumeKeyMock.mockReturnValue({
    mutate: vi.fn(),
  } as unknown as UseMutationResult<null, Error, string>);
});

afterEach(() => {
  vi.clearAllMocks();
  useToastStore.getState().dismissAll();
  cleanup();
});

describe('Dashboard — Keys header', () => {
  it('uses the shared page header: "Keys", a description and + New Key; no stats, no greeting', () => {
    setKeysSuccess([]);
    const { container } = render(<Dashboard />);
    expect(screen.getByRole('heading', { level: 1, name: 'Keys' })).toBeInTheDocument();
    expect(screen.getByText(/Keys let your scripts and tools call the ACH gateway/)).toBeInTheDocument();
    expect(container.querySelector('.head')).toContainElement(screen.getByRole('button', { name: '+ New Key' }));
    expect(screen.queryByText('This month')).toBeNull();
    expect(screen.queryByText(/Welcome back/)).toBeNull();
    expect(screen.queryByText(/endpoint/i)).toBeNull();
  });

  it('counts the non-revoked keys in the title', () => {
    setKeysSuccess([makeRow({ key_id: 'a' }), makeRow({ key_id: 'b' }), makeRow({ key_id: 'c', status: 'revoked' })]);
    render(<Dashboard />);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Keys (2)');
  });
});

describe('Dashboard — keys section + modals', () => {
  it('renders the KeysTable (the empty-state copy proves it mounted)', () => {
    setKeysSuccess([]);
    render(<Dashboard />);
    expect(screen.getByText('No API Keys')).toBeInTheDocument();
  });

  it('clicking "+ New Key" opens the create modal (store open === true)', () => {
    setKeysSuccess([]);
    render(<Dashboard />);
    expect(useCreateKeyModalStore.getState().open).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: '+ New Key' }));
    expect(useCreateKeyModalStore.getState().open).toBe(true);
  });

  it('choosing a row Revoke opens the DeleteKeyModal ("Revoke Key" title appears)', async () => {
    setKeysSuccess([makeRow({ key_id: 'key-del' })]);
    render(<Dashboard />);
    // No delete modal until a row's revoke action (in the kebab) fires.
    expect(screen.queryByText('Revoke Key')).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), {
      key: 'Enter',
    });
    const item = await screen.findByRole('menuitem', { name: 'Revoke key' });
    fireEvent.keyDown(item, { key: 'Enter' });
    expect(screen.getByText('Revoke Key')).toBeInTheDocument();
  });
});
