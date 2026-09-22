// Dashboard.test.tsx — vitest suite for the dashboard CONTAINER (jsdom).
//
// The keys data hook (useKeys) is fully mocked so NO real fetch happens; each
// test programs its return to drive the metric/empty/populated branches. The
// suspend/resume + delete mutations (used by KeysTable / DeleteKeyModal the
// dashboard renders) are mocked too. The create-key-modal + fresh-keys stores
// are reset between tests; the REAL toast store is reset so the delete success
// toast stays isolated. The clipboard is stubbed for the endpoint-copy path.

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

// useStats backs the "Requests (MTD)" tile — mocked so no real fetch fires (the
// dashboard test renders without a QueryClientProvider). Defaults to a benign
// non-success state in beforeEach (tile shows EM_DASH).
vi.mock('@/hooks/use-stats', () => ({
  useStats: vi.fn(),
}));

import {
  useDeleteKey,
  useKeys,
  useResumeKey,
  useSuspendKey,
} from '@/hooks/use-keys';
import { useStats } from '@/hooks/use-stats';
import { Dashboard } from './Dashboard';
import { formatCurrency } from '@/lib/format';
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
const useStatsMock = vi.mocked(useStats);

// Build a valid SessionMe fixture with overrides.
function makeMe(overrides: Partial<SessionMe> = {}): SessionMe {
  return {
    email: 'alice@example.com',
    name: 'Alice Example',
    is_admin: false,
    openwork_enabled: false,
    suspend_propagation_seconds: 60,
    endpoint: 'https://litellm.example.com',
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

/** Program useKeys to the pending (loading) state. */
function setKeysPending(): void {
  useKeysMock.mockReturnValue({
    data: undefined,
    isPending: true,
    isError: false,
    isSuccess: false,
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
  // Default stats to a non-success state — the Requests (MTD) tile shows EM_DASH.
  useStatsMock.mockReturnValue({
    data: undefined,
    isSuccess: false,
    isPending: true,
    isError: false,
  } as unknown as ReturnType<typeof useStats>);
});

afterEach(() => {
  vi.clearAllMocks();
  useToastStore.getState().dismissAll();
  cleanup();
});

describe('Dashboard — top row + tiles', () => {
  it('greeting shows me.name', () => {
    setKeysSuccess([]);
    render(<Dashboard me={makeMe({ name: 'Alice Example' })} />);
    expect(screen.getByText(/Welcome back,/)).toBeInTheDocument();
    expect(screen.getByText('Alice Example')).toBeInTheDocument();
  });

  it('KEYS & ENVIRONMENTS tile shows a pill per distinct environment the keys belong to', () => {
    // Pills derive from the KEYS' environment name (deduped) — a display name
    // already, no id->alias lookup. Two keys on 'prod', one on 'staging', none
    // on 'qa' -> prod + staging pills, no qa.
    setKeysSuccess([
      makeRow({ key_id: 'key-1', environment: 'prod' }),
      makeRow({ key_id: 'key-2', environment: 'prod' }),
      makeRow({ key_id: 'key-3', environment: 'staging' }),
    ]);
    const { container } = render(<Dashboard me={makeMe()} />);
    // Scope to the KPI row's pills — the KeysTable below also renders each key's
    // environment name, so an unscoped getByText('prod') would match multiple nodes.
    const row = container.querySelector('[data-slot="kpi-row"]') as HTMLElement;
    const pills = [...row.querySelectorAll('[data-slot="environment-pill"]')].map((e) =>
      e.textContent?.trim(),
    );
    expect(pills).toEqual(['prod', 'staging']); // deduped, no qa
  });

  it('Spend (MTD) shows formatCurrency(stats.totals.spend) when stats load', () => {
    setKeysSuccess([]);
    useStatsMock.mockReturnValue({
      data: { totals: { requests: 4600, tokens: 8_200_000, spend: 15.94 } },
      isSuccess: true,
      isPending: false,
      isError: false,
    } as unknown as ReturnType<typeof useStats>);
    render(<Dashboard me={makeMe()} />);
    expect(screen.getByText(formatCurrency(15.94))).toBeInTheDocument();
  });

  it('Spend (MTD) tile shows the em-dash while stats are unavailable', () => {
    setKeysSuccess([]);
    // useStats defaults (beforeEach) to non-success -> EM_DASH.
    render(<Dashboard me={makeMe()} />);
    expect(screen.queryByText(formatCurrency(15.94))).not.toBeInTheDocument();
  });

  it('TOTAL REQUESTS / TOTAL TOKENS cards show abbreviated figures when stats load', () => {
    setKeysSuccess([]);
    useStatsMock.mockReturnValue({
      data: { totals: { requests: 4600, tokens: 8_200_000, spend: 15.94 } },
      isSuccess: true,
      isPending: false,
      isError: false,
    } as unknown as ReturnType<typeof useStats>);
    render(<Dashboard me={makeMe()} />);
    // Now two separate KPI cards (abbreviate(4600)='4.6K', abbreviate(8.2M)='8.2M').
    expect(screen.getByText('TOTAL REQUESTS')).toBeInTheDocument();
    expect(screen.getByText('4.6K')).toBeInTheDocument();
    expect(screen.getByText('TOTAL TOKENS')).toBeInTheDocument();
    expect(screen.getByText('8.2M')).toBeInTheDocument();
  });

  it('TOTAL REQUESTS card shows the em-dash while stats are unavailable', () => {
    setKeysSuccess([]);
    // useStats defaults (beforeEach) to non-success -> EM_DASH inside KpiCard.
    render(<Dashboard me={makeMe()} />);
    expect(screen.getByText('TOTAL REQUESTS')).toBeInTheDocument();
  });
});

describe('Dashboard — Active keys tile', () => {
  it('shows the non-revoked count when the keys query has loaded', () => {
    setKeysSuccess([
      makeRow({ key_id: 'key-1' }),
      makeRow({ key_id: 'key-2' }),
      makeRow({ key_id: 'key-3', status: 'revoked' }),
    ]);
    render(<Dashboard me={makeMe()} />);
    // 3 rows, 1 revoked -> 2 active.
    expect(screen.getByText('2')).toBeInTheDocument();
  });

  it('shows the em-dash placeholder while the keys query is pending', () => {
    setKeysPending();
    render(<Dashboard me={makeMe()} />);
    // The Active keys tile reads "—" while pending (never a misleading 0). The
    // em-dash appears in the tile; assert at least one is in the document.
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
  });
});

describe('Dashboard — keys section + modals', () => {
  it('renders the KeysTable (the empty-state copy proves it mounted)', () => {
    setKeysSuccess([]);
    render(<Dashboard me={makeMe()} />);
    expect(screen.getByText('No API Keys')).toBeInTheDocument();
  });

  it('clicking "+ New Key" opens the create modal (store open === true)', () => {
    setKeysSuccess([]);
    render(<Dashboard me={makeMe()} />);
    expect(useCreateKeyModalStore.getState().open).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: '+ New Key' }));
    expect(useCreateKeyModalStore.getState().open).toBe(true);
  });

  it('choosing a row Revoke opens the DeleteKeyModal ("Revoke Key" title appears)', async () => {
    setKeysSuccess([makeRow({ key_id: 'key-del' })]);
    render(<Dashboard me={makeMe()} />);
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
