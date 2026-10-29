import { describe, expect, it } from 'vitest';

import { COMING_SOON, ENV_STATUS, KEY_STATUS, mcpStatus, targetStatus, traceOutcome } from './status';

describe('status label maps', () => {
  it('keys', () => {
    expect(Object.fromEntries(Object.entries(KEY_STATUS).map(([k, v]) => [k, `${v.label}:${v.tone}`]))).toEqual({
      active: 'Active:ok', suspended: 'Suspended:warn', expired: 'Expired:muted', invalid: 'Invalid:muted', revoked: 'Revoked:danger',
    });
  });

  it('environments', () => {
    expect(Object.values(ENV_STATUS).map((v) => `${v.label}:${v.tone}`)).toEqual([
      'Available:ok', 'Degraded:warn', 'Not ready:danger', 'Not in your teams:neutral',
    ]);
  });

  it('mcp', () => {
    expect(mcpStatus('healthy')).toEqual({ label: 'healthy', tone: 'ok' });
    expect(mcpStatus('Unhealthy')).toEqual({ label: 'unhealthy', tone: 'danger' });
    expect(mcpStatus(null)).toEqual({ label: 'unknown', tone: 'neutral' });
    expect(mcpStatus('')).toEqual({ label: 'unknown', tone: 'neutral' });
  });

  it('trace outcome (shared by Overview and Traces)', () => {
    expect(traceOutcome('success')).toEqual({ label: 'Success', tone: 'ok' });
    expect(traceOutcome('error')).toEqual({ label: 'Failed', tone: 'danger' });
    expect(traceOutcome('unknown')).toEqual({ label: 'Unknown', tone: 'neutral' });
    expect(traceOutcome(undefined)).toEqual({ label: 'Unknown', tone: 'neutral' });
  });

  it('overview targets', () => {
    expect(targetStatus('ok', 'Within target', 'Above target')).toEqual({ label: 'Within target', tone: 'ok' });
    expect(targetStatus('over', 'Within target', 'Above target')).toEqual({ label: 'Above target', tone: 'danger' });
    expect(targetStatus(null, 'Within target', 'Above target')).toEqual({ label: 'Unavailable', tone: 'neutral' });
  });

  it('coming soon', () => expect(COMING_SOON).toEqual({ label: 'Coming soon', tone: 'info' }));
});
