// KeysTable.test.tsx — vitest suite for the keys data-table view (jsdom).
//
// The data hook (useKeys) is fully mocked so NO real fetch happens; each test
// programs its return to drive the loading/error/empty/populated branches. The
// table renders no secret material and no reveal/copy actions (those were
// removed — the sk- is shown once, at mint time, in the create-key modal); the
// only per-row action is Revoke.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult, UseMutationResult } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';

import type { BlockKeyResponse, KeyRow, MakeDefaultResponse } from '@/lib/api-types';
import { relativeTime } from '@/lib/relative-time';

// Mock the data hook — each test sets useKeys's return value.
vi.mock('@/hooks/use-keys', () => ({
  useKeys: vi.fn(),
  useMakeDefault: vi.fn(),
  useToggleKeyBlock: vi.fn(),
  useChangeKeyTeam: vi.fn(),
  KEYS_QUERY_KEY: ['session', 'keys'],
}));

// Mock the teams hook — KeysTable reads it to populate the Change-team picker
// and to decide whether to surface the action at all.
vi.mock('@/hooks/use-teams', () => ({
  useTeams: vi.fn(),
}));

import {
  useChangeKeyTeam,
  useKeys,
  useMakeDefault,
  useToggleKeyBlock,
} from '@/hooks/use-keys';
import { useTeams } from '@/hooks/use-teams';
import type { ChangeKeyTeamResponse, Team } from '@/lib/api-types';
import type { UseQueryResult as UseQueryResultTeams } from '@tanstack/react-query';
import { KeysTable } from './KeysTable';

const useKeysMock = vi.mocked(useKeys);
const useMakeDefaultMock = vi.mocked(useMakeDefault);
const useToggleKeyBlockMock = vi.mocked(useToggleKeyBlock);
const useChangeKeyTeamMock = vi.mocked(useChangeKeyTeam);
const useTeamsMock = vi.mocked(useTeams);

const TEAMS: Team[] = [
  { id: 'team-alpha', alias: 'Alpha' },
  { id: 'team-beta', alias: 'Beta' },
];

// Reusable mutation stubs; reset per test via beforeEach.
let makeDefaultMutate: ReturnType<typeof vi.fn>;
let toggleBlockMutate: ReturnType<typeof vi.fn>;
let changeTeamMutate: ReturnType<typeof vi.fn>;
beforeEach(() => {
  makeDefaultMutate = vi.fn();
  useMakeDefaultMock.mockReturnValue({
    mutate: makeDefaultMutate,
  } as unknown as UseMutationResult<MakeDefaultResponse, Error, string>);
  toggleBlockMutate = vi.fn();
  useToggleKeyBlockMock.mockReturnValue({
    mutate: toggleBlockMutate,
  } as unknown as UseMutationResult<BlockKeyResponse, Error, { id: string; blocked: boolean }>);
  changeTeamMutate = vi.fn();
  useChangeKeyTeamMock.mockReturnValue({
    mutate: changeTeamMutate,
    isPending: false,
  } as unknown as UseMutationResult<
    ChangeKeyTeamResponse,
    Error,
    { id: string; teamId: string }
  >);
  // Default: two teams available -> Change team action is offered.
  useTeamsMock.mockReturnValue({
    data: TEAMS,
  } as unknown as UseQueryResultTeams<Team[]>);
});

// A minimal projected /keys row factory (mirrors the api-types KeyRow contract).
function makeRow(overrides: Partial<KeyRow> = {}): KeyRow {
  return {
    id: 'key-abc123',
    key_alias: 'my-key',
    spend: 0,
    budget: null,
    tpm_limit: null,
    rpm_limit: null,
    models: null,
    team_id: null,
    created_at: '2026-03-01T10:00:00+00:00',
    expires: null,
    last_used: null,
    is_default: false,
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
    expect(screen.queryByRole('button', { name: 'Revoke' })).not.toBeInTheDocument();
  });
});

describe('KeysTable — populated table', () => {
  it('renders the column headers in order', () => {
    setRows([makeRow()]);
    render(<KeysTable onDelete={vi.fn()} />);

    for (const label of ['Key ID', 'Created', 'Last used', 'Expires', 'Status']) {
      expect(screen.getByRole('columnheader', { name: label })).toBeInTheDocument();
    }
  });

  it('shows the key_alias when present', () => {
    setRows([makeRow({ key_alias: 'production-key' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText('production-key')).toBeInTheDocument();
  });

  it('shows the id verbatim (<= 16 chars) when key_alias is null', () => {
    setRows([makeRow({ key_alias: null, id: 'key-abc123' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    // 'key-abc123' is 10 chars (<= 16) -> shown in full, no ellipsis.
    expect(screen.getByText('key-abc123')).toBeInTheDocument();
  });

  it('renders a relative Last used cell (recent = not stale)', () => {
    // 2h ago — deterministic relative to the test clock and well under the
    // 30-day stale threshold, so the non-stale variant renders.
    const lastUsed = new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString();
    setRows([makeRow({ last_used: lastUsed })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText(relativeTime(lastUsed))).toBeInTheDocument();
    expect(screen.getByTestId('key-lastused')).toBeInTheDocument();
  });

  it('renders relative last-used and marks stale keys', () => {
    // One never-used row (null) and one used long ago — both are stale.
    setRows([
      makeRow({ id: 'key-never', last_used: null }),
      makeRow({ id: 'key-old', last_used: '2020-01-01T00:00:00+00:00' }),
    ]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText('Never used')).toBeInTheDocument();
    expect(screen.getAllByTestId('key-lastused-stale')).toHaveLength(2);
  });

  it('shows the truncated id (16 chars + ellipsis) beneath the alias, prefixed "id:"', () => {
    setRows([makeRow({ key_alias: 'production-key', id: 'key-0123456789abcdef' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    // 'key-0123456789abcdef' is 20 chars -> first 16 + ellipsis = 'key-0123456789ab…'.
    expect(screen.getByText('id:key-0123456789ab…')).toBeInTheDocument();
  });

  it('status pill honors Revoked > Expired > Active precedence', () => {
    const past = '2000-01-01T00:00:00+00:00';
    setRows([
      makeRow({ id: 'key-revoked', revoked: true, expires: past }),
      makeRow({ id: 'key-expired', expires: past }),
      makeRow({ id: 'key-active' }),
    ]);
    render(<KeysTable onDelete={vi.fn()} />);

    expect(screen.getByText('Revoked')).toBeInTheDocument();
    expect(screen.getByText('Expired')).toBeInTheDocument();
    expect(screen.getByText('Active')).toBeInTheDocument();
  });

  it('renders a rate-limit column (em-dash when a limit is unset)', () => {
    setRows([makeRow({ id: 'key-lim', tpm_limit: 1000, rpm_limit: null })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(
      screen.getByText(
        (_content, el) => el?.textContent === '1,000 / —' && el.tagName === 'SPAN',
      ),
    ).toBeInTheDocument();
  });

  it('renders "Never" for a null expiry and a formatted date otherwise', () => {
    setRows([
      makeRow({ id: 'key-never', expires: null }),
      makeRow({ id: 'key-dated', expires: '2027-06-15T00:00:00+00:00' }),
    ]);
    render(<KeysTable onDelete={vi.fn()} />);

    expect(screen.getByText('Never')).toBeInTheDocument();
    expect(screen.getByText('Jun 15, 2027')).toBeInTheDocument();
  });
});

describe('KeysTable — no secret material, no reveal/copy actions', () => {
  it('renders neither a Reveal nor a Copy action — Revoke lives in the kebab', async () => {
    setRows([makeRow({ id: 'key-public', key_alias: 'old-key' })]);
    render(<KeysTable onDelete={vi.fn()} />);

    expect(screen.queryByRole('button', { name: 'Reveal' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), {
      key: 'Enter',
    });
    expect(
      await screen.findByRole('menuitem', { name: 'Revoke key' })
    ).toBeInTheDocument();
  });

  it('truncates a long id to 16 chars + ellipsis — never the full id', () => {
    setRows([makeRow({ id: 'key-0123456789abcdef', key_alias: null })]);
    render(<KeysTable onDelete={vi.fn()} />);

    expect(screen.getByText('key-0123456789ab…')).toBeInTheDocument();
    expect(screen.queryByText('key-0123456789abcdef')).not.toBeInTheDocument();
  });
});

describe('KeysTable — delete action', () => {
  it('choosing Revoke in the kebab calls onDelete with that row', async () => {
    const onDelete = vi.fn();
    const row = makeRow({ id: 'key-del' });
    setRows([row]);

    render(<KeysTable onDelete={onDelete} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), {
      key: 'Enter',
    });
    const item = await screen.findByRole('menuitem', { name: 'Revoke key' });
    fireEvent.keyDown(item, { key: 'Enter' });

    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledWith(row);
  });
});

describe('KeysTable — default key', () => {
  it('shows a DEFAULT badge on the default key only', () => {
    setRows([
      makeRow({ id: 'key-default', key_alias: 'default-key', is_default: true }),
      makeRow({ id: 'key-other', key_alias: 'other-key', is_default: false }),
    ]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getAllByText('DEFAULT')).toHaveLength(1);
  });

  it('a non-default key offers "Set as default" in its kebab menu', async () => {
    setRows([makeRow({ id: 'key-other', is_default: false })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), {
      key: 'Enter',
    });
    const item = await screen.findByRole('menuitem', { name: 'Set as default' });
    expect(item).toBeInTheDocument();
  });

  it('choosing "Set as default" fires the mutation with the row id', async () => {
    setRows([makeRow({ id: 'key-other', is_default: false })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), {
      key: 'Enter',
    });
    const item = await screen.findByRole('menuitem', { name: 'Set as default' });
    fireEvent.keyDown(item, { key: 'Enter' });
    expect(makeDefaultMutate).toHaveBeenCalledWith('key-other');
  });

  it('the default key omits "Set as default" and disables Revoke', async () => {
    setRows([makeRow({ id: 'key-default', is_default: true })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), {
      key: 'Enter',
    });
    await screen.findByRole('menuitem', { name: 'Default key' });
    expect(
      screen.queryByRole('menuitem', { name: 'Set as default' })
    ).not.toBeInTheDocument();
    // Revoke is present but disabled on the default key (Chat needs a default).
    expect(screen.getByRole('menuitem', { name: /Revoke/ })).toHaveAttribute(
      'data-disabled'
    );
  });

  it('an unmanaged (foreign) key locks its kebab to Disable only', async () => {
    setRows([makeRow({ id: 'ekid_01', managed: false, is_default: false })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), {
      key: 'Enter',
    });
    await screen.findByRole('menuitem', { name: 'Managed externally' });
    // Disable is still allowed; the three management actions are gone.
    expect(screen.getByRole('menuitem', { name: 'Disable key' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Set as default' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: /Change team/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: /Revoke/ })).not.toBeInTheDocument();
  });

  it('nudges to set a default when there are keys but none is default', () => {
    setRows([
      makeRow({ id: 'key-1', is_default: false }),
      makeRow({ id: 'key-2', is_default: false }),
    ]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText(/No default key set/i)).toBeInTheDocument();
  });

  it('hides the nudge once a default exists', () => {
    setRows([
      makeRow({ id: 'key-1', is_default: true }),
      makeRow({ id: 'key-2', is_default: false }),
    ]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.queryByText(/No default key set/i)).not.toBeInTheDocument();
  });

  it('shows no nudge in the empty state', () => {
    setRows([]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.queryByText(/No default key set/i)).not.toBeInTheDocument();
  });
});

describe('KeysTable — disable / enable (LiteLLM block)', () => {
  it('an enabled key offers "Disable key"; choosing it fires toggleBlock(blocked:true)', async () => {
    setRows([makeRow({ id: 'key-x', blocked: false })]);
    render(<KeysTable onDelete={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    const item = await screen.findByRole('menuitem', { name: 'Disable key' });
    fireEvent.keyDown(item, { key: 'Enter' });
    expect(toggleBlockMutate).toHaveBeenCalledWith({ id: 'key-x', blocked: true });
  });

  it('dims the disabled key row so it reads as inactive', () => {
    setRows([makeRow({ id: 'key-x', blocked: true })]);
    const { container } = render(<KeysTable onDelete={vi.fn()} />);
    const row = container.querySelector('[data-slot="data-table-row"]');
    expect(row?.className).toContain('opacity-40');
  });

  it('a disabled key shows the "Disabled" status and offers "Enable key"', async () => {
    setRows([makeRow({ id: 'key-x', blocked: true })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText('Disabled')).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), { key: 'Enter' });
    const item = await screen.findByRole('menuitem', { name: 'Enable key' });
    fireEvent.keyDown(item, { key: 'Enter' });
    expect(toggleBlockMutate).toHaveBeenCalledWith({ id: 'key-x', blocked: false });
  });
});

describe('KeysTable — team column + change team', () => {
  it('shows the team alias (resolved from team_id) in the Team column', () => {
    setRows([makeRow({ id: 'key-t', team_id: 'team-alpha' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByRole('columnheader', { name: 'Team' })).toBeInTheDocument();
    // team-alpha resolves to its alias "Alpha" (matching the picker/dialog/tile).
    expect(screen.getByText('Alpha')).toBeInTheDocument();
    expect(screen.queryByText('team-alpha')).not.toBeInTheDocument();
  });

  it('falls back to the raw team_id when it has no alias match', () => {
    setRows([makeRow({ id: 'key-u', team_id: 'team-unknown' })]);
    render(<KeysTable onDelete={vi.fn()} />);
    expect(screen.getByText('team-unknown')).toBeInTheDocument();
  });

  it('opens change-team dialog and calls useChangeKeyTeam.mutate with picked team', async () => {
    setRows([makeRow({ id: 'key-x', team_id: 'team-alpha' })]);
    render(<KeysTable onDelete={vi.fn()} />);

    // Open the kebab and choose "Change team…" (Radix content is portaled).
    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), {
      key: 'Enter',
    });
    const item = await screen.findByRole('menuitem', { name: /Change team/ });
    fireEvent.keyDown(item, { key: 'Enter' });

    // The dialog (also portaled) carries a native <select> defaulting to the
    // key's current team; Save is disabled until a different team is picked.
    const select = await screen.findByRole('combobox');
    fireEvent.change(select, { target: { value: 'team-beta' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(changeTeamMutate).toHaveBeenCalledWith({ id: 'key-x', teamId: 'team-beta' });
  });

  it('hides the Change team item when no teams are loaded', async () => {
    useTeamsMock.mockReturnValue({
      data: [],
    } as unknown as UseQueryResultTeams<Team[]>);
    setRows([makeRow({ id: 'key-x', team_id: null })]);
    render(<KeysTable onDelete={vi.fn()} />);

    fireEvent.keyDown(screen.getByRole('button', { name: 'More actions' }), {
      key: 'Enter',
    });
    // The menu opened (Disable key is present) but Change team is absent.
    await screen.findByRole('menuitem', { name: 'Disable key' });
    expect(
      screen.queryByRole('menuitem', { name: /Change team/ })
    ).not.toBeInTheDocument();
  });
});
