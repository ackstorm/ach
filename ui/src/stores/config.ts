// config.ts — Zustand presentation-config store.
//
// ACH has no /api/config: the presentation config (brand strings, provider
// chips, real-links) is the compile-time DEFAULT_CONFIG. The store keeps the
// same `useConfigStore((s) => s.config)` read so the components are untouched.
// `links` stays {} — the real-links-only rule (D-02/D-03) drops every
// nav/footer anchor; `providers` is empty so ProviderChips renders nothing.

import { create } from 'zustand';
import type { AppConfig } from '../lib/api-types';

export const DEFAULT_CONFIG: AppConfig = {
  brand: 'ACH',
  brand_short: 'ACH',
  tagline: 'Agent Capability Hub',
  accent_segment: '',
  public_host: '',
  providers: [],
  links: {},
};

export interface ConfigState {
  config: AppConfig;
}

/** Initial state — the built-in defaults. Exported so tests can reset the store. */
export const initialConfigState = {
  config: DEFAULT_CONFIG,
} as const;

export const useConfigStore = create<ConfigState>(() => ({
  ...initialConfigState,
}));
