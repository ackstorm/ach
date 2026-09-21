// config.ts — Zustand presentation-config store, ported from src/ui/app.js.
//
// Holds the public, non-secret presentation config (brand strings, BACKED-BY
// provider chips, real-links) that GET /api/config supplies. The store boots
// from the built-in DEFAULT_CONFIG and overlays the server response ONCE.
//
// loadConfig() reproduces app.js's boot useEffect verbatim:
//   1. await getJson<AppConfig>('/api/config') (never-throws; status 0 on
//      network failure);
//   2. on `status === 200 && data` -> set config = { ...DEFAULT_CONFIG, ...data }
//      — a SHALLOW merge OVER the defaults (object spread, like app.js). A
//      returned `links` object REPLACES the default `links` entirely; it is NOT
//      deep-merged, so the real-links-only rule (D-02/D-03) holds: until the
//      server supplies real links, `links` stays {} and no nav/footer anchor
//      renders;
//   3. on ANY other status (0/4xx/5xx) or null data -> do nothing; config stays
//      DEFAULT_CONFIG. The never-throw getJson contract guarantees this never
//      blocks render (threat T-14-01).

import { create } from 'zustand';
import { getJson } from '../lib/api';
import type { AppConfig } from '../lib/api-types';

// Built-in presentation defaults — ported VERBATIM from src/ui/app.js
// DEFAULT_CONFIG. A successful fetch is merged OVER these; a missing `links`
// stays {} so the real-links-only rule (D-02/D-03) drops every nav/footer anchor
// by default.
export const DEFAULT_CONFIG: AppConfig = {
  brand: 'alitellm-auth',
  brand_short: 'LiteLLM',
  tagline: '',
  accent_segment: '-auth',
  public_host: '',
  chat_public_url: '',
  providers: [{ label: 'Google' }, { label: 'Dex' }, { label: 'OIDC' }],
  links: {},
};

export interface ConfigState {
  config: AppConfig;
  loadConfig: () => Promise<void>;
}

/** Initial state — the built-in defaults. Exported so tests can reset the store. */
export const initialConfigState = {
  config: DEFAULT_CONFIG,
} as const;

export const useConfigStore = create<ConfigState>((set) => ({
  ...initialConfigState,

  loadConfig: async () => {
    const { status, data } = await getJson<AppConfig>('/api/config');

    // SHALLOW merge OVER the defaults on success only. Object spread means a
    // returned `links` REPLACES the default `links` wholesale (no deep-merge).
    if (status === 200 && data) {
      set({ config: { ...DEFAULT_CONFIG, ...data } });
    }

    // Any other status (0/4xx/5xx) or null data: leave config = DEFAULT_CONFIG.
    // Never throws, never blocks (T-14-01).
  },
}));
