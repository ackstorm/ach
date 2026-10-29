// key-validation.test.ts — vitest unit suite for the client-side name
// validator + expiry-preset conversion (node-friendly; pure functions, no DOM).

import { describe, expect, it } from 'vitest';

import {
  ALIAS_ERROR,
  NAME_REQUIRED_ERROR,
  presetToExpiresAt,
  validateName,
} from './key-validation';

describe('validateName', () => {
  it('empty string -> NAME_REQUIRED_ERROR (name is required, unlike the old alias)', () => {
    expect(validateName('')).toBe(NAME_REQUIRED_ERROR);
  });

  it('a valid name (letters, numbers, dash, underscore, dot) -> null', () => {
    expect(validateName('my-key_1.2')).toBeNull();
  });

  it('more than 128 chars -> ALIAS_ERROR', () => {
    expect(validateName('a'.repeat(129))).toBe(ALIAS_ERROR);
  });

  it('exactly 128 chars (boundary) -> null', () => {
    expect(validateName('a'.repeat(128))).toBeNull();
  });

  it('a name with a space -> ALIAS_ERROR', () => {
    expect(validateName('bad name')).toBe(ALIAS_ERROR);
  });

  it('a name with an @ -> ALIAS_ERROR', () => {
    expect(validateName('user@host')).toBe(ALIAS_ERROR);
  });
});

describe('presetToExpiresAt', () => {
  const now = new Date('2026-01-01T00:00:00.000Z');

  it("'never' -> null (perpetual, field omitted)", () => {
    expect(presetToExpiresAt('never', now)).toBeNull();
  });

  it("'7d' -> now + 7 days, RFC3339 UTC", () => {
    expect(presetToExpiresAt('7d', now)).toBe('2026-01-08T00:00:00.000Z');
  });

  it("'30d' -> now + 30 days, RFC3339 UTC", () => {
    expect(presetToExpiresAt('30d', now)).toBe('2026-01-31T00:00:00.000Z');
  });

  it("'90d' -> now + 90 days, RFC3339 UTC", () => {
    expect(presetToExpiresAt('90d', now)).toBe('2026-04-01T00:00:00.000Z');
  });
});
