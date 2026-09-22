// DeleteKeyModal.tsx — the confirm-before-revoke modal (DASH-05), ported from
// src/ui/delete-modal.js (Preact -> React + shadcn AlertDialog).
//
// A CONTROLLED leaf: unlike CreateKeyModal (store-driven, opened from the sidebar),
// this modal's opener is a table row inside the dashboard, so its open-state is
// prop-driven by the parent (Task 3.7 owns `keyToDelete` and renders this).
//
//   keyToDelete — the KeyRow to revoke, or null. Open iff non-null AND has `.key_id`.
//   onClose     — Keep-Key / dismiss / post-success close.
//
// Confirm DELETEs via useDeleteKey.mutateAsync(key_id). On a 204 we fire a
// success toast then onClose(); useDeleteKey.onSuccess ALREADY drops the fresh
// key from the store + invalidates the keys query, so we do NOT duplicate that
// here. On a 403/502/network rejection the modal STAYS OPEN and shows the
// locked in-modal error (the parent is not told to close).
//
// SECURITY:
//   - The key id is a PUBLIC identifier (not a secret — there is no sk- here);
//     interpolating it in the body is fine. It renders as a React text child
//     (React escapes — never raw HTML / no dangerouslySetInnerHTML sink).
//   - No console.*, no web Storage.

import { useCallback, useState } from 'react';

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { useDeleteKey, type ApiCallError } from '@/hooks/use-keys';
import { useToast } from '@/hooks/use-toast';
import type { KeyRow } from '@/lib/api-types';
import { maskKey } from '@/lib/format';

/** Locked in-modal error copy — lifted VERBATIM from delete-modal.js (note em-dash). */
export const DELETE_ERROR =
  "Couldn't revoke that key. It may already be gone — refresh and try again.";

/** Success toast copy (toast.js style: short, no trailing period). */
export const DELETE_SUCCESS = 'Key revoked';

interface DeleteKeyModalProps {
  /** The key to revoke. Modal is open iff non-null AND carries a `.key_id`. */
  keyToDelete: KeyRow | null;
  /** Keep-Key / dismiss / post-success close — clears the parent's target. */
  onClose: () => void;
}

export function DeleteKeyModal({ keyToDelete, onClose }: DeleteKeyModalProps) {
  const [error, setError] = useState<string | null>(null);

  const deleteKey = useDeleteKey();
  const isPending = deleteKey.isPending;
  const { toast } = useToast();

  // Open only when a key WITH a key_id is targeted. A key object missing a
  // key_id is treated as not-open (defensive, mirrors the onConfirm guard, WR-03).
  const open = Boolean(keyToDelete?.key_id);

  // What the body names: the human name when set, else the key_id MASKED to
  // prefix…last4 (same convention as the keys table). The raw id would
  // overflow the dialog box (see ref screenshot); this is short + bounded.
  const revokeLabel = keyToDelete?.name || maskKey(keyToDelete?.key_id);

  // Reset the transient error then bubble the dismiss up to the parent.
  const handleClose = useCallback(() => {
    setError(null);
    onClose();
  }, [onClose]);

  const onConfirm = useCallback(async () => {
    // Guard the absence of a key_id (malformed projection / future shape
    // change): without this we would fire DELETE .../undefined (WR-03).
    if (isPending || !keyToDelete?.key_id) return;
    setError(null);
    try {
      await deleteKey.mutateAsync(keyToDelete.key_id);
      // 204 — useDeleteKey.onSuccess ALREADY dropped the fresh key + invalidated
      // the list; here we only surface the success toast and close.
      toast({ message: DELETE_SUCCESS, variant: 'success' });
      onClose();
    } catch (err) {
      // A specific server rejection (e.g. a 409 state conflict) surfaces its
      // own detail. Any other rejection (403 / 502 / network) shows the locked
      // generic copy. Keep the modal OPEN either way.
      const e = err as Partial<ApiCallError>;
      if (e && typeof e.detail === 'string') {
        setError(e.detail);
      } else {
        setError(DELETE_ERROR);
      }
    }
  }, [deleteKey, keyToDelete, isPending, toast, onClose]);

  return (
    <AlertDialog
      open={open}
      onOpenChange={(o) => {
        if (!o && !isPending) handleClose();
      }}
    >
      <AlertDialogContent className="max-w-md">
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke Key</AlertDialogTitle>
          <AlertDialogDescription className="leading-relaxed">
            This will permanently revoke{' '}
            <span
              className="text-foreground inline-block max-w-full truncate align-bottom font-medium"
              title={revokeLabel}
            >
              {revokeLabel}
            </span>
            . This cannot be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error ? <p className="text-xs text-destructive">{error}</p> : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={isPending}>Keep Key</AlertDialogCancel>
          {/* Plain destructive Button (NOT AlertDialogAction): Radix's Action
              auto-closes on click; we must keep the modal OPEN on error and
              close it ourselves only on success. */}
          <Button
            type="button"
            variant="destructive"
            onClick={() => void onConfirm()}
            disabled={isPending}
          >
            Confirm Revoke
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
