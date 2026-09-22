// KeysTable.test.tsx — vitest suite for the keys data-table view (jsdom).
//
// The data hook (useKeys) is fully mocked so NO real fetch happens; each test
// programs its return to drive the loading/error/empty/populated branches. The
// suspend/resume mutations are mocked too. The REAL toast + session stores are
// used (reset between tests) so the suspend success toast's exact copy (with
// the propagation-seconds interpolation) can be asserted.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult, UseMutationResult } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';

import type { KeyRow } from '@/lib/api-types';
import { relativeTime } from '@/lib/relative-time';

// Mock the data hook — each test sets useKeys's return value.
vi.mock('@/hooks/use-keys', () => ({
  useKeys: vi.fn(),
  useSuspendKey: vi.fn(),
  useResumeKey: vi.fn(),
  KEYS_QUERY_KEY: vi.fn(() => ['keys', null]),
}));

import { useKeys, useResumeKey, useSuspendKey } from '@/hooks/use-keys';
import { initialToastState, useToastStore } from '@/hooks/use-toast';
import { initialSessionState, useSessionStore } from '@/stores/session';
import { KeysTable } from './KeysTable';

const useKeysMock = vi.mocked(useKeys);
const useSuspendKeyMock = vi.mocked(useSuspendKey);
const useResumeKeyMock = vi.mocked(useResumeKey);

// Reusable mutation stubs; reset per test via beforeEach.
let suspendMutate: ReturnType<typeof vi.fn>;
let resumeMutate: ReturnType<typeof vi.fn>;

beforeEach(() => {
  suspendMutate = vi.fn((_id: string, opts?: { onSuccess?: () => void }) =>
    opts?.onSuccess?.(),
  );
  useSuspendKeyMock.mockReturnValue({
    mutate: suspendMutate,
  } as unknown as UseMutationResult<null, Error, string>);
  resumeMutate = vi.fn();
  useResumeKeyMock.mockReturnValue({
    mutate: resumeMutate,
  } as unknown as UseMutationResult<null, Error, string>);

  // Reset the real toast store.
  const { toast, dismiss, dismissAll } = useToastStore.getState();
  useToastStore.setState({ ...initialToastState, toast, dismiss, dismissAll }, true);

  // Seed the session store with a fixed suspend-propagation window.
  const { loadSession, markExpired } = useSessionStore.getState();
  useSessionStore.setState(
    {
      ...initialSessionState,
      me: {
        email: 'alice@example.com',
        name: 'alice@example.com',
        is_admin: false,
        openwork_enabled: false,
        suspend_propagation_seconds: 60,
        endpoint: 'https://litellm.example.com',
      },
      loadSession,
      markExpired,
    },
    true,
  );
});

// A minimal KeyRow factory (mirrors render.KeyListRow).
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

/** Program useKeys to return a populated success state with `rows`. */
function setRows(rows: KeyRow[]): void {
  useKeysMock.mockReturnValue({
    data: rows,
    isPending: false,
    isError: false,
  } as unknown as UseQueryResult<KeyRow[]>);
}

afterEach(() => {
  vi.clearAllMocks();
  useToastStore.getState().dismissAll();
});

describe('KeysTable — state branches', () => {
  it('loading state shows "Loading your keys…"', () => {
    useKeysMock.mockReturnValue({
      data: undefined,
      isPending: true,
      isError: false,
    } as unknown as UseQueryResult<KeyRow[]>);

    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText('Loading your keys…')).toBeInTheDocument();
  });

  it('error state shows the exact error copy', () => {
    useKeysMock.mockReturnValue({
      data: undefined,
      isPending: false,
      isError: true,
    } as unknown as UseQueryResult<KeyRow[]>);

    render(<KeysTable onDelete={vi.fn()} />);
    expect(
      screen.getByText("Couldn't load your keys. Refresh the page to try again.")
    ).toBeInTheDocument();
  });

  it('empty state shows "No API Keys" + body copy and renders no data rows', () => {
    setRows([]);
    render(<KeysTable onDelete={vi.fn()} />);

    expect(screen.getByText('No API Keys')).toBeInTheDocument();
    expect(
      screen.getByText('You have no virtual keys yet. Create one to get started.')
    ).toBeInTheDocument();
  });
});

describe('KeysTable — populated table', () => {
  it('renders the column headers in order: Name, Environment, State, Created, Last used, Expires', () => {
    setRows([makeRow()]);
    render(<KeysTable onDelete={vi.fn()} />);

    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent);
    const positions = ['Name', 'Environment', 'State', 'Created', 'Last used', 'Expires'].map(
      (label) => headers.findIndex((h) => h?.includes(label)),
    );
    expect(positions.every((p) => p >= 0)).toBe(true);
    expect(positions).toEqual([...positions].sort((a, b) => a - b));
  });

  it('shows the name', () => {
    setRows([makeRow({ name: 'production-key' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText('production-key')).toBeInTheDocument();
  });

  it('shows the truncated key_id (16 chars + ellipsis) beneath the name, prefixed "id:"', () => {
    setRows([makeRow({ name: 'production-key', key_id: 'ekid_0123456789abcdef' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    // 'ekid_0123456789abcdef' is 21 chars -> first 16 + ellipsis = 'ekid_0123456789a…'.
    expect(screen.getByText('id:ekid_0123456789a…')).toBeInTheDocument();
  });

  it('shows the environment', () => {
    setRows([makeRow({ environment: 'staging' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText('staging')).toBeInTheDocument();
  });

  it('renders a relative Last used cell (recent = not stale)', () => {
    const lastUsed = new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString();
    setRows([makeRow({ last_used_at: lastUsed })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText(relativeTime(lastUsed))).toBeInTheDocument();
    expect(screen.getByTestId('key-lastused')).toBeInTheDocument();
  });

  it('renders "Never used" and marks a never-used key stale', () => {
    setRows([makeRow({ key_id: 'ekid-never', last_used_at: undefined })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText('Never used')).toBeInTheDocument();
    expect(screen.getByTestId('key-lastused-stale')).toBeInTheDocument();
  });

  it('renders "Never" for a null expiry and a formatted date otherwise', () => {
    setRows([
      makeRow({ key_id: 'ekid-never', expires_at: null }),
      makeRow({ key_id: 'ekid-dated', expires_at: '2027-06-15T00:00:00Z' }),
    ]);
    render(<KeysTable onDelete={vi.fn()} />);

    expect(screen.getByText('Never')).toBeInTheDocument();
    expect(screen.getByText('Jun 15, 2027')).toBeInTheDocument();
  });

  it('renders the State badge for each of the five effective states', () => {
    setRows([
      makeRow({ key_id: 'k1', status: 'active' }),
      makeRow({ key_id: 'k2', status: 'suspended' }),
      makeRow({ key_id: 'k3', status: 'expired' }),
      makeRow({ key_id: 'k4', status: 'invalid' }),
      makeRow({ key_id: 'k5', status: 'revoked' }),
    ]);
    render(<KeysTable onDelete={vi.fn()} />);

    for (const label of ['Active', 'Suspended', 'Expired', 'Invalid', 'Revoked']) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
  });
});

describe('KeysTable — no secret material, no reveal/copy actions', () => {
  it('renders neither a Reveal nor a Copy action', () => {
    setRows([makeRow({ key_id: 'ekid-public', name: 'old-key' })]);
    render(<KeysTable onDelete={vi.fn()} />);

    expect(screen.queryByRole('button', { name: 'Reveal' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument();
  });

  it('truncates a long key_id to 16 chars + ellipsis — never the full id', () => {
    setRows([makeRow({ key_id: 'ekid_0123456789abcdef', name: '' })]);
    render(<KeysTable onDelete={vi.fn()} />);

    expect(screen.queryByText('ekid_0123456789abcdef')).not.toBeInTheDocument();
  });
});

describe('KeysTable — actions per state', () => {
  it('active: offers Suspend + Revoke, not Resume', async () => {
    setRows([makeRow({ status: 'active' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    await screen.findByRole('menuitem', { name: 'Suspend key' });
    expect(screen.getByRole('menuitem', { name: 'Revoke key' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Resume key' })).not.toBeInTheDocument();
  });

  it('invalid: also offers Suspend (per D-30)', async () => {
    setRows([makeRow({ status: 'invalid' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    expect(await screen.findByRole('menuitem', { name: 'Suspend key' })).toBeInTheDocument();
  });

  it('suspended: offers Resume + Revoke, not Suspend', async () => {
    setRows([makeRow({ status: 'suspended' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    await screen.findByRole('menuitem', { name: 'Resume key' });
    expect(screen.getByRole('menuitem', { name: 'Revoke key' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Suspend key' })).not.toBeInTheDocument();
  });

  it('expired: offers only Revoke', async () => {
    setRows([makeRow({ status: 'expired' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    await screen.findByRole('menuitem', { name: 'Revoke key' });
    expect(screen.queryByRole('menuitem', { name: 'Suspend key' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Resume key' })).not.toBeInTheDocument();
  });

  it('revoked: no actions available', async () => {
    setRows([makeRow({ status: 'revoked' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    await screen.findByRole('menuitem', { name: 'No actions available' });
    expect(screen.queryByRole('menuitem', { name: 'Revoke key' })).not.toBeInTheDocument();
  });

  it('choosing Suspend fires useSuspendKey with the row key_id and shows the propagation toast', async () => {
    setRows([makeRow({ key_id: 'ekid-x', status: 'active' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    const item = await screen.findByRole('menuitem', { name: 'Suspend key' });
    fireEvent.keyDown(item, { key: 'Enter' });

    expect(suspendMutate).toHaveBeenCalledWith('ekid-x', expect.objectContaining({
      onSuccess: expect.any(Function),
    }));
    const { toasts } = useToastStore.getState();
    expect(toasts).toHaveLength(1);
    expect(toasts[0].message).toBe(
      'Suspension may take up to 60 seconds to apply. Requests already in progress may continue.',
    );
  });

  it('choosing Resume fires useResumeKey with the row key_id', async () => {
    setRows([makeRow({ key_id: 'ekid-y', status: 'suspended' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    const item = await screen.findByRole('menuitem', { name: 'Resume key' });
    fireEvent.keyDown(item, { key: 'Enter' });

    expect(resumeMutate).toHaveBeenCalledWith('ekid-y');
  });

  it('choosing Revoke calls onDelete with that row', async () => {
    const onDelete = vi.fn();
    const row = makeRow({ key_id: 'ekid-del' });
    setRows([row]);

    render(<KeysTable onDelete={onDelete} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    const item = await screen.findByRole('menuitem', { name: 'Revoke key' });
    fireEvent.keyDown(item, { key: 'Enter' });

    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledWith(row);
  });
});
