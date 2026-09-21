// key-validation.test.ts — vitest unit suite for the client-side alias/duration
// validators (node-friendly; pure functions, no DOM).
//
// These validators MIRROR src/api/app/session.py:session_create_key (lifted
// verbatim from src/ui/create-key.js) so users see the locked field messages
// without a backend round-trip. Empty input is VALID (the field is optional ->
// the body omits it). Each validator returns an error string or null.

import { describe, expect, it } from 'vitest';

import {
  ALIAS_ERROR,
  DURATION_ERROR,
  validateAlias,
  validateDuration,
} from './key-validation';

describe('validateAlias', () => {
  it('empty string is valid (optional field) -> null', () => {
    expect(validateAlias('')).toBeNull();
  });

  it('a valid alias (letters, numbers, dash, underscore, dot) -> null', () => {
    expect(validateAlias('my-key_1.2')).toBeNull();
  });

  it('more than 128 chars -> ALIAS_ERROR', () => {
    expect(validateAlias('a'.repeat(129))).toBe(ALIAS_ERROR);
  });

  it('exactly 128 chars (boundary) -> null', () => {
    expect(validateAlias('a'.repeat(128))).toBeNull();
  });

  it('an alias with a space -> ALIAS_ERROR', () => {
    expect(validateAlias('bad name')).toBe(ALIAS_ERROR);
  });

  it('an alias with an @ -> ALIAS_ERROR', () => {
    expect(validateAlias('user@host')).toBe(ALIAS_ERROR);
  });
});

describe('validateDuration', () => {
  it('empty string is valid (no expiry) -> null', () => {
    expect(validateDuration('')).toBeNull();
  });

  it.each(['90d', '24h', '30m', '15s'])('valid duration %s -> null', (value) => {
    expect(validateDuration(value)).toBeNull();
  });

  it.each(['90', 'd', '90x', '1.5d'])(
    'bad duration %s -> DURATION_ERROR',
    (value) => {
      expect(validateDuration(value)).toBe(DURATION_ERROR);
    },
  );
});
