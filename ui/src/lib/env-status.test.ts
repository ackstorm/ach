import { describe, it, expect } from 'vitest';
import { isKeyable, degradedReason } from './env-status';

describe('isKeyable', () => {
  it('is keyable when AccessGroupSynced=True, regardless of the collapsed status', () => {
    expect(
      isKeyable({
        name: 'prod',
        status: 'UnresolvedReferences',
        conditions: [{ type: 'AccessGroupSynced', status: 'True' }],
      }),
    ).toBe(true);
  });

  it('is not keyable when AccessGroupSynced=False', () => {
    expect(
      isKeyable({
        name: 'prod',
        status: 'Available',
        conditions: [{ type: 'AccessGroupSynced', status: 'False' }],
      }),
    ).toBe(false);
  });

  it('falls back to status === "Available" when conditions is absent', () => {
    expect(isKeyable({ name: 'prod', status: 'Available' })).toBe(true);
    expect(isKeyable({ name: 'staging', status: 'UnresolvedReferences' })).toBe(false);
  });

  it('falls back to status === "Available" when conditions is empty', () => {
    expect(isKeyable({ name: 'prod', status: 'Available', conditions: [] })).toBe(true);
  });
});

describe('degradedReason', () => {
  it('returns the Available condition reason when keyable but not Available', () => {
    expect(
      degradedReason({
        name: 'prod',
        status: 'UnresolvedReferences',
        conditions: [
          { type: 'AccessGroupSynced', status: 'True' },
          { type: 'Available', status: 'False', reason: 'ContentPending' },
        ],
      }),
    ).toBe('ContentPending');
  });

  it('is undefined when the environment is fully Available', () => {
    expect(
      degradedReason({
        name: 'prod',
        status: 'Available',
        conditions: [{ type: 'AccessGroupSynced', status: 'True' }],
      }),
    ).toBeUndefined();
  });

  it('is undefined when not keyable', () => {
    expect(
      degradedReason({
        name: 'staging',
        status: 'UnresolvedReferences',
        conditions: [{ type: 'AccessGroupSynced', status: 'False' }],
      }),
    ).toBeUndefined();
  });
});
