// create-key-modal.test.ts — vitest unit suite for the create-key modal
// open-state store. A tiny Zustand store replacing the old
// registerCreateOpener callback indirection: the table "+ New Key" CTA and the
// sidebar "Create key" shortcut both flip the SAME single modal open without
// prop-drilling. The store is reset to its initial closed state before each
// test (setState(..., true) replaces the whole state, re-supplying the actions
// so the replace flag does not drop them).

import { beforeEach, describe, expect, it } from 'vitest';
import {
  initialCreateKeyModalState,
  useCreateKeyModalStore,
} from './create-key-modal';

beforeEach(() => {
  const { openModal, closeModal } = useCreateKeyModalStore.getState();
  useCreateKeyModalStore.setState(
    { ...initialCreateKeyModalState, openModal, closeModal },
    true,
  );
});

describe('create-key-modal store', () => {
  it('starts closed', () => {
    expect(useCreateKeyModalStore.getState().open).toBe(false);
  });

  it('openModal() flips open to true', () => {
    useCreateKeyModalStore.getState().openModal();
    expect(useCreateKeyModalStore.getState().open).toBe(true);
  });

  it('closeModal() flips open back to false', () => {
    useCreateKeyModalStore.getState().openModal();
    useCreateKeyModalStore.getState().closeModal();
    expect(useCreateKeyModalStore.getState().open).toBe(false);
  });
});
