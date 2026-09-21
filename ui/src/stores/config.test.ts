// config.test.ts — the presentation-config store is a compile-time constant
// (ACH has no /api/config); the only contract is the defaults themselves.

import { describe, expect, it } from 'vitest';
import { DEFAULT_CONFIG, useConfigStore } from './config';

describe('config store', () => {
  it('config equals DEFAULT_CONFIG', () => {
    expect(useConfigStore.getState().config).toEqual(DEFAULT_CONFIG);
  });

  it('defaults carry no links and no provider chips (real-links-only rule)', () => {
    expect(DEFAULT_CONFIG.links).toEqual({});
    expect(DEFAULT_CONFIG.providers).toEqual([]);
  });
});
