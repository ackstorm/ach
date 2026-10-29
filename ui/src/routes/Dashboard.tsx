// Dashboard.tsx — the Keys page (#/keys): the shared page header (CatalogHead)
// with the `+ New Key` CTA, the KeysTable, and the prop-driven DeleteKeyModal
// (this page owns `keyToDelete`). Usage figures live on Overview and Analytics;
// the gateway endpoint chip lives on How-to.
//
// The CREATE modal + Toaster are mounted in AppShell; this page only triggers
// openModal() on the store-driven modal.

import { useState } from 'react';

import { CatalogHead } from '@/components/catalog/Catalog';
import { DeleteKeyModal } from '@/components/keys/DeleteKeyModal';
import { KeysTable } from '@/components/keys/KeysTable';
import { useKeys } from '@/hooks/use-keys';
import type { KeyRow } from '@/lib/api-types';
import { selectKeyRows } from '@/lib/keys';
import { useCreateKeyModalStore } from '@/stores/create-key-modal';

const PAGE_SUB =
  'Keys let your scripts and tools call the ACH gateway. Each key is scoped to one environment and gets its models, MCP servers and agents. The full key is shown once, at creation; revoke it at any time.';

export function Dashboard() {
  const openModal = useCreateKeyModalStore((s) => s.openModal);
  // KeysTable's per-row revoke action sets the target; DeleteKeyModal is prop-driven by it.
  const [keyToDelete, setKeyToDelete] = useState<KeyRow | null>(null);
  // Same query as KeysTable (TanStack dedupes). Revoked keys are hidden by default, so they don't count.
  const count = selectKeyRows(useKeys().data).filter((k) => k.status !== 'revoked').length;

  return (
    <div className="catalog-page flex flex-col gap-6">
      <CatalogHead title="Keys" description={PAGE_SUB} count={count}>
        <button type="button" className="btn primary" onClick={() => openModal()}>+ New Key</button>
      </CatalogHead>
      <KeysTable onDelete={setKeyToDelete} />
      <DeleteKeyModal keyToDelete={keyToDelete} onClose={() => setKeyToDelete(null)} />
    </div>
  );
}
