// resolve-state.test.ts — vitest unit suite for the pure resolveState() state
// machine. Runs in the default node environment (no jsdom) because resolveState
// is pure (no DOM/network refs). Ported verbatim from src/ui/state.test.js.
import { describe, it, expect } from 'vitest';
import { resolveState } from './resolve-state';

describe('resolveState — five shell states', () => {
  it('null status before the first response resolves -> loading', () => {
    expect(resolveState(null, false)).toBe('loading');
    // The source resolveState() also maps `undefined` -> loading. The public
    // type is `number | null`, so the undefined case is cast to mirror the
    // original test faithfully without widening the signature.
    expect(resolveState(undefined as unknown as number | null, false)).toBe(
      'loading',
    );
  });

  it('200 -> authed', () => {
    expect(resolveState(200, false)).toBe('authed');
    expect(resolveState(200, true)).toBe('authed');
  });

  it('401 on a cold load (hasLoaded false) -> signin (NOT redirect)', () => {
    expect(resolveState(401, false)).toBe('signin');
  });

  it('401 after a successful load (hasLoaded true) -> expired (silent redirect)', () => {
    expect(resolveState(401, true)).toBe('expired');
  });

  it('non-401 errors and network failure -> error', () => {
    expect(resolveState(502, false)).toBe('error');
    expect(resolveState(503, true)).toBe('error');
    expect(resolveState(0, false)).toBe('error'); // network/fetch failure
    expect(resolveState(500, false)).toBe('error');
    expect(resolveState(404, true)).toBe('error');
  });

  it('is pure — same inputs always yield the same output', () => {
    expect(resolveState(401, false)).toBe(resolveState(401, false));
    expect(resolveState(401, true)).toBe(resolveState(401, true));
  });
});
