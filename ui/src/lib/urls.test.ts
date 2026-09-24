import { describe, expect, it } from 'vitest';

import { loginUrl } from './urls';

describe('loginUrl', () => {
  it('points at the console session login with the return path URL-encoded in next', () => {
    expect(loginUrl('/')).toBe('/platform/console/session/login?next=%2F');
    expect(loginUrl('/#/stats')).toBe('/platform/console/session/login?next=%2F%23%2Fstats');
  });
});
