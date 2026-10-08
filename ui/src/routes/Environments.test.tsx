// Environments.test.tsx — vitest for the #/environments page (jsdom).
//
// useEnvironments is mocked so NO real fetch happens: the team-scoped call
// ({} / no arg) and the admin inventory ({ all: true }) return programmed rows.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { UseQueryResult } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import type { EnvironmentRow, SessionMe } from '@/lib/api-types';

vi.mock('@/hooks/use-environments', () => ({ useEnvironments: vi.fn() }));

import { useEnvironments } from '@/hooks/use-environments';
import { initialSessionState, useSessionStore } from '@/stores/session';
import {
  initialCreateKeyModalState,
  useCreateKeyModalStore,
} from '@/stores/create-key-modal';
import { Environments } from './Environments';

const useEnvironmentsMock = vi.mocked(useEnvironments);

const OWN: EnvironmentRow = {
  name: 'platform-dev',
  status: 'Available',
  description: 'Platform team workspace.',
  authorizedTeams: ['platform'],
  runtime: { models: ['claude-sonnet-5-5', 'gpt-5'], mcpServers: ['github'] },
  context: { skills: ['pdf'] },
};
const OTHER: EnvironmentRow = { name: 'k8s-doctor', status: 'Available', authorizedTeams: ['agents'] };

function result(data: EnvironmentRow[] | undefined): UseQueryResult<EnvironmentRow[]> {
  return { data, isPending: data === undefined } as unknown as UseQueryResult<EnvironmentRow[]>;
}

function program(own: EnvironmentRow[] | undefined, all: EnvironmentRow[] = [OWN, OTHER]): void {
  useEnvironmentsMock.mockImplementation((opts) => result(opts?.all ? all : own));
}

function setAdmin(isAdmin: boolean): void {
  useSessionStore.setState({
    ...initialSessionState,
    me: { email: 'me@x.example', name: 'Me', is_admin: isAdmin } as SessionMe,
  });
}

beforeEach(() => {
  useEnvironmentsMock.mockReset();
  useCreateKeyModalStore.setState({ ...initialCreateKeyModalState });
  setAdmin(false);
});

afterEach(cleanup);

describe('Environments page', () => {
  it('renders one collapsed card per environment with status and counts', () => {
    program([OWN]);
    const { container } = render(<Environments />);

    expect(screen.getByText('platform-dev')).toBeTruthy();
    expect(screen.getByText('Available')).toBeTruthy();
    expect(screen.getByText('2 models')).toBeTruthy();
    expect(screen.getByText('1 MCP')).toBeTruthy();
    expect(screen.getByText('0 agents')).toBeTruthy();
    expect(container.querySelector('details')?.open).toBe(false);
  });

  it('expanded body lists names, the hydrate command, and preselects the env on Create key', () => {
    program([OWN]);
    render(<Environments />);

    expect(screen.getByText('claude-sonnet-5-5')).toBeTruthy();
    expect(screen.getByText('ach-cli env hydrate platform-dev --dir ./platform-dev')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /create key in this env/i }));
    const state = useCreateKeyModalStore.getState();
    expect(state.open).toBe(true);
    expect(state.environment).toBe('platform-dev');
  });

  it('shows the empty state when no environment is visible', () => {
    program([]);
    render(<Environments />);
    expect(screen.getByText('No environments yet')).toBeTruthy();
  });

  it('hides the admin toggle from non-admins and never requests the inventory', () => {
    program([OWN]);
    render(<Environments />);

    expect(screen.queryByLabelText(/show all environments/i)).toBeNull();
    const allCalls = useEnvironmentsMock.mock.calls.filter(([opts]) => opts?.all);
    expect(allCalls.length).toBeGreaterThan(0);
    for (const [opts] of allCalls) expect(opts?.enabled).toBe(false);
  });

  it('does not badge rows while the admin\'s own list is still loading', () => {
    setAdmin(true);
    program(undefined);
    render(<Environments />);
    fireEvent.click(screen.getByLabelText(/show all environments/i));

    expect(screen.queryByText('k8s-doctor')).toBeNull();
    expect(screen.queryByText('Not in your teams')).toBeNull();
  });

  it('admin toggle shows every environment and badges the ones outside their teams', () => {
    setAdmin(true);
    program([OWN]);
    render(<Environments />);

    expect(screen.queryByText('k8s-doctor')).toBeNull();
    fireEvent.click(screen.getByLabelText(/show all environments/i));

    expect(screen.getByText('k8s-doctor')).toBeTruthy();
    expect(screen.getAllByText('Not in your teams')).toHaveLength(1);
    expect(screen.getByText(/1 outside your teams/)).toBeTruthy();
    // Only the own-team row keeps the Create key button.
    expect(screen.getAllByRole('button', { name: /create key in this env/i })).toHaveLength(1);
  });
});
