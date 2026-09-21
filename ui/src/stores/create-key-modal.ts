// create-key-modal.ts — Zustand open-state store for the single create-key
// modal. Replaces the old registerCreateOpener callback indirection: the keys
// table "+ New Key" CTA and the sidebar "Create key" shortcut (both wired in
// Task 3.7) call openModal() to flip the SAME modal open without prop-drilling
// an `open`/`onOpen` pair through the layout.
//
// Idiom matches stores/session.ts / config.ts / fresh-keys.ts: a plain
// `create<T>((set) => ...)` with an exported `initialCreateKeyModalState` for
// test resets. The modal itself owns its transient form state (alias/duration/
// errors/result); this store holds ONLY the open boolean.

import { create } from 'zustand';

export interface CreateKeyModalState {
  open: boolean;
  openModal: () => void;
  closeModal: () => void;
}

/** Initial (closed) state. Exported so tests can reset the store to it. */
export const initialCreateKeyModalState = {
  open: false,
} as const;

export const useCreateKeyModalStore = create<CreateKeyModalState>((set) => ({
  ...initialCreateKeyModalState,

  openModal: () => set({ open: true }),
  closeModal: () => set({ open: false }),
}));
