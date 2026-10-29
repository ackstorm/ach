// fresh-keys.test.ts — vitest unit suite for the in-memory fresh-key store.
//
// The store holds the id -> full `sk-` map of keys minted THIS browser session.
// These tests assert the merge/drop semantics and, crucially, the stable-
// identity no-op of dropFresh on an absent id (mirrors dashboard.js onDeleted's
// `if (!(id in prev)) return prev`). The store is reset to its initial empty
// state before every test (setState(..., true) replaces the whole state, but we
// re-supply the actions so the replace flag does not drop them).

import { beforeEach, describe, expect, it } from 'vitest';
import { initialFreshKeysState, useFreshKeysStore } from './fresh-keys';

beforeEach(() => {
  const { setFresh, dropFresh } = useFreshKeysStore.getState();
  useFreshKeysStore.setState({ ...initialFreshKeysState, setFresh, dropFresh }, true);
});

describe('fresh-keys store', () => {
  it('initial state has an empty freshKeys map', () => {
    expect(useFreshKeysStore.getState().freshKeys).toEqual({});
  });

  it('setFresh adds an id -> sk- entry', () => {
    useFreshKeysStore.getState().setFresh('key-1', 'sk-abc');
    expect(useFreshKeysStore.getState().freshKeys).toEqual({ 'key-1': 'sk-abc' });
  });

  it('a second setFresh MERGES (does not drop the first)', () => {
    useFreshKeysStore.getState().setFresh('key-1', 'sk-abc');
    useFreshKeysStore.getState().setFresh('key-2', 'sk-def');
    expect(useFreshKeysStore.getState().freshKeys).toEqual({
      'key-1': 'sk-abc',
      'key-2': 'sk-def',
    });
  });

  it('dropFresh removes ONLY the target id', () => {
    useFreshKeysStore.getState().setFresh('key-1', 'sk-abc');
    useFreshKeysStore.getState().setFresh('key-2', 'sk-def');
    useFreshKeysStore.getState().dropFresh('key-1');
    expect(useFreshKeysStore.getState().freshKeys).toEqual({ 'key-2': 'sk-def' });
  });

  it('dropFresh of an ABSENT id is a no-op that returns the SAME object reference', () => {
    useFreshKeysStore.getState().setFresh('key-1', 'sk-abc');
    const before = useFreshKeysStore.getState().freshKeys;
    useFreshKeysStore.getState().dropFresh('does-not-exist');
    const after = useFreshKeysStore.getState().freshKeys;
    // Stable identity — mirrors dashboard.js's `if (!(id in prev)) return prev`.
    expect(after).toBe(before);
  });
});
