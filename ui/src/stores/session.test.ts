// session.test.ts — vitest unit suite for the Zustand session store.
//
// The api module is fully mocked (vi.mock) so NO real fetch happens; each test
// programs getJson's resolved { status, data } and asserts the store's
// {status, me, hasLoaded} after loadSession(). The store is reset to its
// initial cold-load state before every test (setState(..., true) replaces the
// whole state, but we re-supply loadSession so the action survives the replace).

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SessionMe } from '../lib/api-types';

// Mock the api module: getJson is a vi.fn() whose resolution each test programs.
vi.mock('../lib/api', () => ({
  getJson: vi.fn(),
}));

import { getJson } from '../lib/api';
import { initialSessionState, useSessionStore } from './session';

const getJsonMock = vi.mocked(getJson);

// A representative SessionMe payload (mirrors the api-types contract).
const ME: SessionMe = {
  email: 'alice@example.com',
  name: 'Alice Example',
  team_id: 'team-platform',
  endpoint: 'https://litellm.example.com',
  limits: null,
  spend: { current: 0, source: 'user' },
};

beforeEach(() => {
  getJsonMock.mockReset();
  // Replace the whole state back to the cold-load initial, but keep the
  // loadSession action (the `true` replace flag drops it otherwise).
  const { loadSession, markExpired } = useSessionStore.getState();
  useSessionStore.setState(
    { ...initialSessionState, loadSession, markExpired },
    true,
  );
});

describe('session store', () => {
  it('initial state is { status: null, me: null, hasLoaded: false }', () => {
    const { status, me, hasLoaded } = useSessionStore.getState();
    expect(status).toBeNull();
    expect(me).toBeNull();
    expect(hasLoaded).toBe(false);
  });

  it('loadSession sets status:null first, then status to the response status', async () => {
    // Capture the intermediate status while the fetch is in flight: getJson
    // resolves on a later microtask, so the synchronous set({status:null})
    // has already run by the time getJson is awaited.
    let inFlightStatus: number | null = -1;
    getJsonMock.mockImplementation(async () => {
      inFlightStatus = useSessionStore.getState().status;
      return { status: 200, data: ME };
    });

    await useSessionStore.getState().loadSession();

    expect(inFlightStatus).toBeNull(); // status was reset to null before the await resolved
    expect(useSessionStore.getState().status).toBe(200);
  });

  it('200 + SessionMe -> { status:200, me:<that>, hasLoaded:true }', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: ME });

    await useSessionStore.getState().loadSession();

    const { status, me, hasLoaded } = useSessionStore.getState();
    expect(status).toBe(200);
    expect(me).toEqual(ME);
    expect(hasLoaded).toBe(true);
  });

  it('cold-load 401 (hasLoaded was false) -> { status:401, me:null, hasLoaded:false } (App resolves signin)', async () => {
    getJsonMock.mockResolvedValue({ status: 401, data: null });

    await useSessionStore.getState().loadSession();

    const { status, me, hasLoaded } = useSessionStore.getState();
    expect(status).toBe(401);
    expect(me).toBeNull();
    expect(hasLoaded).toBe(false); // stays false -> resolveState(401, false) === 'signin'
  });

  it('mid-session expiry: 200 then 401 -> { status:401, hasLoaded:true } (App resolves expired); me is NOT cleared', async () => {
    // First load: 200 -> me set, hasLoaded flips true.
    getJsonMock.mockResolvedValueOnce({ status: 200, data: ME });
    await useSessionStore.getState().loadSession();
    expect(useSessionStore.getState().hasLoaded).toBe(true);

    // Second load: 401. hasLoaded stays true (monotonic) -> resolveState(401,
    // true) === 'expired'. Faithful to app.js, `me` is NOT cleared on a non-200.
    getJsonMock.mockResolvedValueOnce({ status: 401, data: null });
    await useSessionStore.getState().loadSession();

    const { status, me, hasLoaded } = useSessionStore.getState();
    expect(status).toBe(401);
    expect(hasLoaded).toBe(true);
    expect(me).toEqual(ME); // preserved — app.js only ever calls setMe on 200
  });

  it('network failure (status 0) -> { status:0 } (App resolves error)', async () => {
    getJsonMock.mockResolvedValue({ status: 0, data: null });

    await useSessionStore.getState().loadSession();

    const { status, me, hasLoaded } = useSessionStore.getState();
    expect(status).toBe(0);
    expect(me).toBeNull();
    expect(hasLoaded).toBe(false);
  });
});

describe('markExpired', () => {
  it('no-ops when hasLoaded is false (cold load stays signin)', () => {
    // initial cold-load state: status null, hasLoaded false.
    useSessionStore.getState().markExpired();
    const { status, hasLoaded } = useSessionStore.getState();
    expect(status).toBeNull(); // unchanged
    expect(hasLoaded).toBe(false);
  });

  it('flips status to 401 when hasLoaded is true (mid-session -> expired)', () => {
    useSessionStore.setState({ status: 200, hasLoaded: true, me: ME });
    useSessionStore.getState().markExpired();
    const { status, me, hasLoaded } = useSessionStore.getState();
    expect(status).toBe(401); // resolveState(401, true) === 'expired'
    expect(hasLoaded).toBe(true);
    expect(me).toEqual(ME); // me is NOT cleared
  });
});
