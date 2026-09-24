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

// A representative SessionMe as the store maps it from the bootstrap payload
// (name mirrors email, endpoint is this origin) — so feeding it back through
// getJson round-trips to itself.
const ME: SessionMe = {
  email: 'alice@example.com',
  name: 'alice@example.com',
  is_admin: false,
  openwork_enabled: false,
  suspend_propagation_seconds: 60,
  keys_used: 2,
  max_keys: 5,
  endpoint: window.location.origin,
  chat_url: '',
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

  it('200 on /platform/console/bootstrap -> me = bootstrap + name (email) + endpoint (origin); hasLoaded:true', async () => {
    // An empty display name (no IdP name claim, or a bearer caller) falls
    // back to the email; `endpoint` is this origin (ACH serves the console
    // and /v1 from one host).
    const bootstrap = {
      email: 'alice@example.com',
      name: '',
      is_admin: false,
      openwork_enabled: false,
      suspend_propagation_seconds: 60,
      keys_used: 2,
      max_keys: 5,
    };
    getJsonMock.mockResolvedValue({ status: 200, data: bootstrap });

    await useSessionStore.getState().loadSession();

    expect(getJsonMock).toHaveBeenCalledWith('/platform/console/bootstrap');
    const { status, me, hasLoaded } = useSessionStore.getState();
    expect(status).toBe(200);
    expect(me).toEqual({
      ...bootstrap,
      name: 'alice@example.com',
      endpoint: window.location.origin,
    });
    expect(hasLoaded).toBe(true);
  });

  it('keeps the IdP display name from bootstrap', async () => {
    getJsonMock.mockResolvedValue({
      status: 200,
      data: { email: 'alice@example.com', name: 'Alice Doe', is_admin: false },
    });

    await useSessionStore.getState().loadSession();

    expect(useSessionStore.getState().me?.name).toBe('Alice Doe');
  });

  it('carries the key allowance from bootstrap', async () => {
    getJsonMock.mockResolvedValue({
      status: 200,
      data: {
        email: 'alice@example.com',
        is_admin: false,
        openwork_enabled: false,
        suspend_propagation_seconds: 60,
        keys_used: 2,
        max_keys: 5,
      },
    });

    await useSessionStore.getState().loadSession();

    expect(useSessionStore.getState().me).toMatchObject({ keys_used: 2, max_keys: 5 });
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
