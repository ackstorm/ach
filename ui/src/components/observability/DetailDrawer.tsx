import * as Dialog from '@radix-ui/react-dialog';
import { useRef, type ReactNode } from 'react';

/** Scoped side panel with modal keyboard navigation and return focus. */
export function DetailDrawer({ open, onClose, title, description, children }: { open: boolean; onClose: () => void; title: string; description?: string; children: ReactNode }) {
  const returnFocus = useRef<HTMLElement | null>(null);
  return <Dialog.Root open={open} onOpenChange={(value) => { if (!value) onClose(); }}>
    <Dialog.Portal container={document.querySelector('.console-ledger')}>
      <Dialog.Overlay className="drawer-scrim on" />
      <Dialog.Content onOpenAutoFocus={() => { returnFocus.current = document.activeElement as HTMLElement; }} onCloseAutoFocus={(event) => { event.preventDefault(); if (returnFocus.current?.isConnected) returnFocus.current.focus(); }} className="drawer on" {...(!description ? { 'aria-describedby': undefined } : {})}>
        <div className="drawer-head"><div><Dialog.Title>{title}</Dialog.Title>{description && <Dialog.Description>{description}</Dialog.Description>}</div><Dialog.Close className="close" aria-label="Close details">×</Dialog.Close></div>
        <div className="drawer-body">{children}</div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
