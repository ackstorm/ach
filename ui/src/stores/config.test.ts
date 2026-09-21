// config.test.ts — vitest unit suite for the Zustand presentation-config store.
//
// The api module is fully mocked (vi.mock) so NO real fetch happens; each test
// programs getJson's resolved { status, data } and asserts the store's `config`
// after loadConfig(). The store is reset to its initial state before every test
// (setState(..., true) replaces the whole state, but we re-supply loadConfig so
// the action survives the replace — same pattern as session.test.ts).

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AppConfig } from '../lib/api-types';

// Mock the api module: getJson is a vi.fn() whose resolution each test programs.
vi.mock('../lib/api', () => ({
  getJson: vi.fn(),
}));

import { getJson } from '../lib/api';
import { DEFAULT_CONFIG, initialConfigState, useConfigStore } from './config';

const getJsonMock = vi.mocked(getJson);

beforeEach(() => {
  getJsonMock.mockReset();
  // Replace the whole state back to the initial defaults, but keep the
  // loadConfig action (the `true` replace flag drops it otherwise).
  const { loadConfig } = useConfigStore.getState();
  useConfigStore.setState({ ...initialConfigState, loadConfig }, true);
});

describe('config store', () => {
  it('initial config equals DEFAULT_CONFIG', () => {
    expect(useConfigStore.getState().config).toEqual(DEFAULT_CONFIG);
  });

  it('loadConfig 200 with partial server config -> shallow-merged over defaults; links REPLACED', async () => {
    // Partial server payload: overrides `brand` and supplies a `links` object.
    const server: Partial<AppConfig> = {
      brand: 'ACKStorm',
      links: { status: 'https://x' },
    };
    getJsonMock.mockResolvedValue({ status: 200, data: server as AppConfig });

    await useConfigStore.getState().loadConfig();

    const { config } = useConfigStore.getState();
    // Server field overrides the default.
    expect(config.brand).toBe('ACKStorm');
    // Unspecified fields fall back to the defaults.
    expect(config.brand_short).toBe(DEFAULT_CONFIG.brand_short);
    expect(config.tagline).toBe(DEFAULT_CONFIG.tagline);
    expect(config.accent_segment).toBe(DEFAULT_CONFIG.accent_segment);
    expect(config.public_host).toBe(DEFAULT_CONFIG.public_host);
    expect(config.providers).toEqual(DEFAULT_CONFIG.providers);
    // `links` is REPLACED wholesale by the server's (NOT deep-merged):
    // the result is exactly the server's links, not defaults {} merged with it.
    expect(config.links).toEqual({ status: 'https://x' });
  });

  it('loadConfig failure (status 0) -> config stays exactly DEFAULT_CONFIG (never-throw)', async () => {
    getJsonMock.mockResolvedValue({ status: 0, data: null });

    await expect(useConfigStore.getState().loadConfig()).resolves.toBeUndefined();

    expect(useConfigStore.getState().config).toEqual(DEFAULT_CONFIG);
  });

  it('loadConfig failure (status 500) -> config stays exactly DEFAULT_CONFIG (never-throw)', async () => {
    // A 5xx may still carry a body; it must NOT be merged (status !== 200).
    getJsonMock.mockResolvedValue({
      status: 500,
      data: { brand: 'should-be-ignored' } as AppConfig,
    });

    await expect(useConfigStore.getState().loadConfig()).resolves.toBeUndefined();

    expect(useConfigStore.getState().config).toEqual(DEFAULT_CONFIG);
  });

  it('loadConfig 200 with null data -> stays DEFAULT_CONFIG', async () => {
    getJsonMock.mockResolvedValue({ status: 200, data: null });

    await useConfigStore.getState().loadConfig();

    expect(useConfigStore.getState().config).toEqual(DEFAULT_CONFIG);
  });
});
